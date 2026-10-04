package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/redis/go-redis/v9"
	"golang.org/x/oauth2"
)

type oauthTransport func(*http.Request) (*http.Response, error)

func (f oauthTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubAccountsAndProfiles(t *testing.T) {
	databaseURL, redisURL := os.Getenv("AUTH_TEST_DATABASE_URL"), os.Getenv("AUTH_TEST_REDIS_URL")
	if databaseURL == "" || redisURL == "" {
		t.Skip("set AUTH_TEST_DATABASE_URL and AUTH_TEST_REDIS_URL to isolated, migrated services")
	}

	t.Setenv("OAUTH_SCRF_SECRET", "isolated-oauth-state-secret")
	t.Setenv("JWT_TOKEN", "isolated-jwt-secret")

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}

	cache := redis.NewClient(options)
	defer cache.Close()

	config := utils.NewOAuthConfig()
	config.GitHub.ClientID, config.GitHub.ClientSecret = "test-github-client", "test-github-secret"
	config.Google.ClientID, config.Google.ClientSecret = "test-google-client", "test-google-secret"
	identity := githubUser{
		ID:     101,
		Login:  "worker-one",
		Avatar: "https://avatars.githubusercontent.com/u/101",
	}
	profileStatus := 200
	expectedVerifier := ""
	config.Client = &http.Client{Transport: oauthTransport(func(r *http.Request) (*http.Response, error) {
		body, status := "", 200

		switch {
		case r.URL.Path == "/login/oauth/access_token":
			if err := r.ParseForm(); err != nil {
				return nil, err
			}

			if r.Form.Get("code_verifier") != expectedVerifier ||
				r.Form.Get("redirect_uri") != config.GitHub.RedirectURL {
				t.Error("GitHub exchange must send the original verifier and callback")
			}

			body = `{"access_token":"github-provider-token","token_type":"bearer","expires_in":28800,"refresh_token":"unused-refresh-token"}`
		case r.URL.Host == "api.github.com" && r.URL.Path == "/user":
			if r.Header.Get("Authorization") != "Bearer github-provider-token" {
				t.Error("profile request missing provider token")
			}

			encoded, _ := json.Marshal(identity)
			body, status = string(encoded), profileStatus
		case r.URL.Path == "/token":
			body = `{"access_token":"google-provider-token","token_type":"bearer"}`
		case r.URL.Host == "www.googleapis.com":
			body = `{"id":"google-owner","name":"Google Owner","email":"owner@example.test","picture":"https://example.test/avatar"}`
		default:
			t.Errorf("unexpected provider request %s", r.URL.Path)

			status = 500
		}

		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	ctrl := NewController(pool, nil, config, cache)
	verifier := oauth2.GenerateVerifier()
	challenge := oauth2.S256ChallengeFromVerifier(verifier)
	cliState := strings.Repeat("b", 24)
	callbackURL := "http://127.0.0.1:12345/callback"
	startValues := url.Values{
		"cli_redirect_uri": {callbackURL},
		"code_challenge":   {challenge},
		"cli_state":        {cliState},
	}
	start := func(linkID int64) *httptest.ResponseRecorder {
		address := "/oauth/github?" + startValues.Encode()

		if linkID > 0 {
			body, _ := json.Marshal(
				map[string]string{
					"cli_redirect_uri": callbackURL,
					"code_challenge":   challenge,
					"cli_state":        cliState,
				},
			)
			request := httptest.NewRequest("POST", "/oauth/github/link", strings.NewReader(string(body)))
			request = request.WithContext(
				context.WithValue(ctx, utils.UserKey, &utils.UserJWT{ID: strconv.FormatInt(linkID, 10)}),
			)
			response := httptest.NewRecorder()
			ctrl.InitGitHubLink(response, request)

			if response.Code != 200 {
				t.Fatalf("start connection: %s", response.Body.String())
			}

			var result map[string]string
			json.Unmarshal(response.Body.Bytes(), &result)
			address = result["url"]
		}

		response := httptest.NewRecorder()
		ctrl.InitGitHubLogin(response, httptest.NewRequest("GET", address, nil))

		location, _ := url.Parse(response.Header().Get("Location"))
		if response.Code != 303 || location.Query().Get("code_challenge_method") != "S256" ||
			location.Query().Get("scope") != "read:user" {
			t.Fatal("GitHub authorization must use PKCE and profile scope")
		}

		for _, cookie := range response.Result().Cookies() {
			if !cookie.HttpOnly || cookie.Path != "/oauth/callback/github" || cookie.MaxAge != 600 {
				t.Fatal("unbound OAuth cookie")
			}

			if cookie.Name == "oauth_github_pkce" {
				expectedVerifier = cookie.Value
				if oauth2.S256ChallengeFromVerifier(cookie.Value) != location.Query().Get("code_challenge") {
					t.Fatal("incorrect provider challenge")
				}
			}
		}

		return response
	}
	complete := func(started *httptest.ResponseRecorder, denied bool) (*utils.UserJWT, string) {
		location, _ := url.Parse(started.Header().Get("Location"))

		query := url.Values{"state": {location.Query().Get("state")}, "code": {"provider-code"}}
		if denied {
			query.Set("error", "access_denied")
		}

		request := httptest.NewRequest("GET", "/oauth/callback/github?"+query.Encode(), nil)
		for _, cookie := range started.Result().Cookies() {
			request.AddCookie(cookie)
		}

		response := httptest.NewRecorder()
		ctrl.GitHubCallback(response, request)

		redirect, err := url.Parse(response.Header().Get("Location"))
		if err != nil || response.Code != 303 || redirect.Query().Get("state") != cliState {
			t.Fatalf("callback: %d %s", response.Code, response.Body.String())
		}

		if message := redirect.Query().Get("error"); message != "" {
			return nil, message
		}

		code := redirect.Query().Get("code")
		if strings.Contains(redirect.String(), "provider-token") {
			t.Fatal("provider token leaked in callback")
		}

		if _, err := ctrl.cliLogins.Redeem(ctx, code, strings.Repeat("c", 43)); err == nil {
			t.Fatal("wrong handoff verifier accepted")
		}

		token, err := ctrl.cliLogins.Redeem(ctx, code, verifier)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := ctrl.cliLogins.Redeem(ctx, code, verifier); err == nil {
			t.Fatal("handoff redeemed twice")
		}

		user, err := utils.VerifyUserToken(token)
		if err != nil {
			t.Fatal(err)
		}

		return user, ""
	}

	checkConnections := func(user *utils.UserJWT, googleConnected, githubConnected bool) {
		t.Helper()

		token, _, err := utils.CreateUserToken(user)
		if err != nil {
			t.Fatal(err)
		}

		request := httptest.NewRequest("GET", "/me", nil)
		request.Header.Set("Authorization", "Bearer "+token)

		response := httptest.NewRecorder()
		ctrl.GetMe(response, request)

		var profile map[string]any
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &profile) != nil ||
			profile["id"] != user.ID || profile["google_connected"] != googleConnected ||
			profile["github_connected"] != githubConnected {
			t.Fatalf("incorrect connected accounts: %d %s", response.Code, response.Body.String())
		}
	}

	newUser, message := complete(start(0), false)
	if message != "" || newUser == nil || newUser.Email != "" {
		t.Fatalf("private-email GitHub login: %s", message)
	}

	checkConnections(newUser, false, true)

	firstID := newUser.ID
	identity.Login = "worker-renamed"

	renamed, message := complete(start(0), false)
	if message != "" || renamed.ID != firstID {
		t.Fatal("GitHub rename created another account")
	}

	google, err := ctrl.queries.RegisterUser(
		ctx,
		db.RegisterUserParams{Email: pgtype.Text{String: "owner@example.test", Valid: true},
			GoogleID: pgtype.Text{String: "google-owner", Valid: true}, Username: "Google Owner"},
	)
	if err != nil {
		t.Fatal(err)
	}

	checkConnections(&utils.UserJWT{ID: fmt.Sprint(google.ID)}, true, false)

	if _, err := ctrl.queries.LinkWallet(
		ctx,
		db.LinkWalletParams{UserID: google.ID, Address: strings.Repeat("1", 32)},
	); err != nil {
		t.Fatal(err)
	}

	post, err := ctrl.queries.CreatePost(
		ctx,
		db.CreatePostParams{
			UserID:          google.ID,
			Title:           "Owned before connecting GitHub",
			CostLamports:    1,
			EndTime:         pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
			Status:          db.PostStatusOpen,
			Level:           db.PostLevelEasy,
			Description:     "A description",
			InputFiles:      []string{},
			ExpectedOutputs: []string{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	identity = githubUser{ID: 202, Login: "requester"}

	linked, message := complete(start(google.ID), false)
	if message != "" || linked.ID != fmt.Sprint(google.ID) {
		t.Fatalf("linking changed account: %s", message)
	}

	checkConnections(linked, true, true)

	linkedAgain, message := complete(start(google.ID), false)
	if message != "" || linkedAgain.ID != linked.ID {
		t.Fatal("reconnecting the same GitHub identity changed the account")
	}

	signedIn, message := complete(start(0), false)
	if message != "" || signedIn.ID != linked.ID {
		t.Fatal("GitHub sign-in did not reuse linked Google account")
	}

	wallet, err := ctrl.queries.GetWallet(ctx, google.ID)
	if err != nil || wallet.Address != strings.Repeat("1", 32) {
		t.Fatal("linking changed wallet")
	}

	workerID, _ := strconv.ParseInt(firstID, 10, 64)
	pool.Exec(ctx, "UPDATE posts SET accepted_by=$2, status='negotiating' WHERE id=$1", post.ID, workerID)

	post, err = ctrl.queries.GetPost(ctx, post.ID)
	if err != nil || post.UserID != google.ID {
		t.Fatal("linking changed post ownership")
	}

	views, err := ctrl.postProfiles(ctx, []db.Post{post})
	if err != nil || views[0].Poster.GitHubLogin != "requester" ||
		views[0].Worker.GitHubLogin != "worker-renamed" {
		t.Fatal("poster/worker profile association missing")
	}

	encoded, _ := json.Marshal(views)
	if strings.Contains(string(encoded), "email") || strings.Contains(string(encoded), "google_id") ||
		strings.Contains(string(encoded), "github_id") {
		t.Fatal("public task leaked private identity fields")
	}

	identity.ID, identity.Login = 303, "different-account"
	if _, message := complete(
		start(google.ID),
		false,
	); !strings.Contains(
		message,
		"connected as @requester",
	) {
		t.Fatal("an existing connection must explain which GitHub account to use")
	}

	identity.ID, identity.Login = 202, "requester"

	if _, message := complete(start(workerID), false); message == "" {
		t.Fatal("GitHub identity attached to another account")
	}

	other, err := ctrl.queries.RegisterUser(ctx, db.RegisterUserParams{
		Email:    pgtype.Text{String: "other@example.test", Valid: true},
		GoogleID: pgtype.Text{String: "google-other", Valid: true}, Username: "Other Owner",
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, message := complete(start(other.ID), false); !strings.Contains(message, "another Maruvo account") ||
		!strings.Contains(
			message,
			"Sign in with GitHub",
		) || !strings.Contains(message, "different GitHub account") {
		t.Fatal("a duplicate-account conflict must explain how to proceed")
	}

	checkConnections(&utils.UserJWT{ID: fmt.Sprint(other.ID)}, true, false)
	checkConnections(linked, true, true)

	started := start(other.ID)
	if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id=$1", other.ID); err != nil {
		t.Fatal(err)
	}

	if _, message := complete(started, false); !strings.Contains(message, "no longer exists") {
		t.Fatal("a deleted account must not receive a linked GitHub session")
	}

	if _, message := complete(start(0), true); !strings.Contains(message, "denied") {
		t.Fatal("denial did not reach CLI")
	}

	profileStatus = 500

	if _, message := complete(start(0), false); message == "" {
		t.Fatal("provider failure created a session")
	}

	profileStatus = 200
	identity.ID = 0

	if _, message := complete(start(0), false); message == "" {
		t.Fatal("invalid provider ID created an account")
	}
	// Google login must still reuse its original account after GitHub is linked.
	googleState, err := utils.CreateOAuthState()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(
		"GET",
		"/oauth/callback/google?code=test&state="+url.QueryEscape(googleState),
		nil,
	)
	request.AddCookie(&http.Cookie{Name: "oauth_state", Value: googleState})

	response := httptest.NewRecorder()
	ctrl.GoogleCallback(response, request)

	var result map[string]string
	json.Unmarshal(response.Body.Bytes(), &result)

	googleUser, err := utils.VerifyUserToken(result["token"])
	if err != nil || googleUser.ID != linked.ID {
		t.Fatalf("Google login regression: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest("GET", "/me", nil)
	request.Header.Set("Authorization", "Bearer "+result["token"])

	response = httptest.NewRecorder()
	ctrl.GetMe(response, request)

	if response.Code != 200 || !strings.Contains(response.Body.String(), `"github_login":"requester"`) {
		t.Fatal("current profile absent")
	}
	// A valid token for a deleted account must fail during CLI session restoration.
	var unusedID int64
	pool.QueryRow(ctx, "INSERT INTO users(username) VALUES('temporary') RETURNING id").Scan(&unusedID)
	token, _, _ := utils.CreateUserToken(&utils.UserJWT{ID: fmt.Sprint(unusedID)})
	pool.Exec(ctx, "DELETE FROM users WHERE id=$1", unusedID)

	request = httptest.NewRequest("GET", "/me", nil)
	request.Header.Set("Authorization", "Bearer "+token)

	response = httptest.NewRecorder()
	ctrl.GetMe(response, request)

	if response.Code != 401 {
		t.Fatal("deleted account restored from JWT")
	}
}
