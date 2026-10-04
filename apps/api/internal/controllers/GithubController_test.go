package controller

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rajandhamala/Maruvo/internal/utils"
)

func TestGitHubCallbackRejectsUnboundState(t *testing.T) {
	t.Setenv("OAUTH_SCRF_SECRET", "isolated-test-state-secret")

	state, err := utils.CreateProviderOAuthState("github", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}

	google, err := utils.CreateOAuthState()
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct{ state, cookie, verifier string }{
		{state, "", strings.Repeat("a", 43)},
		{state, "different", strings.Repeat("a", 43)},
		{google, google, strings.Repeat("a", 43)},
		{state, state, "short"},
	} {
		request := httptest.NewRequest(
			"GET",
			"/oauth/callback/github?state="+url.QueryEscape(test.state)+"&code=unused",
			nil,
		)
		request.AddCookie(&http.Cookie{Name: "oauth_github_state", Value: test.cookie})
		request.AddCookie(&http.Cookie{Name: "oauth_github_pkce", Value: test.verifier})

		response := httptest.NewRecorder()
		(&Controller{}).GitHubCallback(response, request)

		if response.Code != 400 {
			t.Fatalf("unbound state reached provider: %d", response.Code)
		}
	}
}

func TestGitHubMissingConfigReturnsToCLI(t *testing.T) {
	t.Setenv("OAUTH_SCRF_SECRET", "isolated-test-state-secret")

	values := url.Values{"cli_redirect_uri": {"http://127.0.0.1:12345/callback"},
		"code_challenge": {strings.Repeat("a", 43)}, "cli_state": {strings.Repeat("b", 20)}}
	response := httptest.NewRecorder()
	(&Controller{}).InitGitHubLogin(
		response,
		httptest.NewRequest("GET", "/oauth/github?"+values.Encode(), nil),
	)

	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil || response.Code != 303 || location.Query().Get("error") == "" ||
		location.Query().Get("state") != values.Get("cli_state") {
		t.Fatal("unconfigured login must return an error to the waiting CLI")
	}
}
