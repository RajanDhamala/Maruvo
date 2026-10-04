package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestProviderLoginAndGitHubLinkHandoff(t *testing.T) {
	for _, test := range []struct{ provider, linkToken string }{
		{"github", ""}, {"google", ""}, {"github", "existing-maruvo-session"},
	} {
		t.Run(test.provider+test.linkToken, func(t *testing.T) {
			var (
				lock                       sync.Mutex
				redirect, challenge, state string
			)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				lock.Lock()
				defer lock.Unlock()

				switch r.URL.Path {
				case "/oauth/github/link":
					if r.Header.Get("Authorization") != "Bearer "+test.linkToken {
						t.Error("missing authenticated linking request")
					}

					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("invalid link request")
					}

					redirect, challenge, state = body["cli_redirect_uri"], body["code_challenge"], body["cli_state"]

					json.NewEncoder(w).Encode(map[string]string{"url": "/oauth/github?request=one-time-link"})
				case "/oauth/cli/token":
					var body map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("invalid redemption")
					}

					hash := sha256.Sum256([]byte(body["code_verifier"]))
					if body["code"] != "one-time-code" ||
						base64.RawURLEncoding.EncodeToString(hash[:]) != challenge {
						t.Error("handoff did not prove the original PKCE verifier")
					}

					json.NewEncoder(w).Encode(map[string]string{"token": "maruvo-session"})
				default:
					t.Error("unexpected API request")
				}
			}))
			defer server.Close()

			open := func(address string) error {
				loginURL, err := url.Parse(address)
				if err != nil {
					return err
				}

				if loginURL.Path != "/oauth/"+test.provider {
					t.Errorf("wrong provider path: %s", loginURL.Path)
				}

				lock.Lock()
				if test.linkToken == "" {
					redirect, challenge, state = loginURL.Query().
						Get("cli_redirect_uri"),
						loginURL.Query().
							Get("code_challenge"),
						loginURL.Query().
							Get("cli_state")
				}

				callback, err := url.Parse(redirect)
				query := url.Values{"code": {"one-time-code"}, "state": {state}}
				lock.Unlock()

				if err != nil {
					return err
				}

				for _, invalid := range []url.Values{
					{"code": {"one-time-code"}, "state": {"wrong-state"}},
					{"state": {query.Get("state")}},
				} {
					callback.RawQuery = invalid.Encode()

					response, err := http.Get(callback.String())
					if err != nil {
						return err
					}

					body, err := io.ReadAll(response.Body)
					response.Body.Close()

					if err != nil {
						return err
					}

					if response.StatusCode != 400 ||
						!strings.Contains(string(body), "Try signing in again.") {
						t.Error("invalid callback must show a retry page without completing login")
					}
				}

				lock.Lock()
				query.Set("state", state)
				lock.Unlock()

				callback.RawQuery = query.Encode()

				response, err := http.Get(callback.String())
				if err != nil {
					return err
				}

				body, err := io.ReadAll(response.Body)
				response.Body.Close()

				if err != nil {
					return err
				}

				if response.StatusCode != 200 ||
					response.Header.Get("Content-Type") != "text/html; charset=utf-8" ||
					!strings.Contains(string(body), "Return to your terminal.") ||
					strings.Contains(string(body), "one-time-code") ||
					strings.Contains(string(body), state) {
					t.Error("callback must render an HTML handoff page without exposing login secrets")
				}

				if response.Header.Get("Cache-Control") != "no-store" ||
					response.Header.Get("Referrer-Policy") != "no-referrer" {
					t.Error("callback page must preserve login privacy headers")
				}

				return nil
			}

			token, err := login(
				context.Background(),
				api.NewClient(server.URL),
				test.provider,
				test.linkToken,
				open,
			)
			if err != nil || token != "maruvo-session" {
				t.Fatalf("login failed: %v", err)
			}
		})
	}
}

func TestProviderErrorShowsRetryPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a denied login must not exchange a code")
	}))
	defer server.Close()

	open := func(address string) error {
		loginURL, err := url.Parse(address)
		if err != nil {
			return err
		}

		callback, err := url.Parse(loginURL.Query().Get("cli_redirect_uri"))
		if err != nil {
			return err
		}

		callback.RawQuery = url.Values{
			"state": {loginURL.Query().Get("cli_state")},
			"error": {"access_denied <script>secret</script>"},
		}.Encode()

		response, err := http.Get(callback.String())
		if err != nil {
			return err
		}
		defer response.Body.Close()

		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}

		if !strings.Contains(string(body), "Try signing in again.") ||
			strings.Contains(string(body), "Return to your terminal.") ||
			strings.Contains(string(body), "<script>") || strings.Contains(string(body), "access_denied") {
			t.Error("provider denial must show a generic retry page without reflecting the query")
		}

		return nil
	}

	token, err := login(context.Background(), api.NewClient(server.URL), "github", "", open)
	if token != "" || err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("the terminal must receive the provider error without a token: %v", err)
	}
}
