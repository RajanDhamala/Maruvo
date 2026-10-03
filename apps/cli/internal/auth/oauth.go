package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type loginCallback struct {
	code string
	err  error
}

func Login(ctx context.Context, client *api.Client) (string, error) {
	return LoginWithProvider(ctx, client, "github")
}

func LoginWithProvider(ctx context.Context, client *api.Client, provider string) (string, error) {
	return login(ctx, client, provider, "", OpenBrowser)
}

func LinkGitHub(ctx context.Context, client *api.Client, token string) (string, error) {
	return login(ctx, client, "github", token, OpenBrowser)
}

func login(
	ctx context.Context,
	client *api.Client,
	provider, linkToken string,
	openBrowser func(string) error,
) (string, error) {
	if provider != "github" && provider != "google" {
		return "", errors.New("login provider must be github or google")
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("start login callback: %w", err)
	}
	defer listener.Close()

	// genrates 32 bytes cryptographically secure secret
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(verifierBytes); err != nil {
		return "", err
	}

	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	hash := sha256.Sum256([]byte(verifier))

	// take the whole array and create a slice & encode it
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	state := rand.Text()

	callbackURL := "http://" + listener.Addr().String() + "/callback"

	results := make(chan loginCallback, 1)

	mux := http.NewServeMux()

	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")

		if r.Host != listener.Addr().String() ||
			subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(state)) != 1 {
			writeLoginPage(w, http.StatusBadRequest, true)
			return
		}

		result := loginCallback{
			code: r.URL.Query().Get("code"),
		}

		if message := r.URL.Query().Get("error"); message != "" {
			result.err = fmt.Errorf("%s login: %s", provider, message)
		} else if result.code == "" {
			writeLoginPage(w, http.StatusBadRequest, true)
			return
		}

		select {
		case results <- result:
			writeLoginPage(w, http.StatusOK, result.err != nil)
		default:
			writeLoginPage(w, http.StatusConflict, true)
		}
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	defer server.Close()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	loginURL, err := url.Parse(client.LoginURL(provider))
	if err != nil {
		return "", err
	}

	query := loginURL.Query()

	query.Set("cli_redirect_uri", callbackURL)

	query.Set("code_challenge", challenge)

	query.Set("cli_state", state)

	loginURL.RawQuery = query.Encode()

	browserURL := loginURL.String()
	if linkToken != "" {
		browserURL, err = client.GitHubLinkURL(ctx, linkToken, callbackURL, challenge, state)
		if err != nil {
			return "", err
		}
	}

	if err := openBrowser(browserURL); err != nil {
		return "", fmt.Errorf("open %s login: %w", provider, err)
	}

	select {
	case result := <-results:
		if result.err != nil {
			return "", result.err
		}

		return client.ExchangeCLICode(ctx, result.code, verifier)
	case err := <-serverErrors:
		return "", fmt.Errorf("login callback stopped: %w", err)
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%s login timed out; try again", provider)
		}

		return "", ctx.Err()
	}
}
