package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"golang.org/x/oauth2"
)

type githubUser struct {
	ID     int64  `json:"id"`
	Login  string `json:"login"`
	Avatar string `json:"avatar_url"`
}

func githubProfileURL(login string) string {
	if login == "" {
		return ""
	}

	return "https://github.com/" + url.PathEscape(login)
}

func githubCookie(r *http.Request, name, value string, age int) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/oauth/callback/github",
		MaxAge:   age,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	}
}

func (ctrl *Controller) githubConfigured() bool {
	return ctrl.oauth != nil && ctrl.oauth.GitHub != nil &&
		ctrl.oauth.GitHub.ClientID != "" && ctrl.oauth.GitHub.ClientSecret != ""
}

func (ctrl *Controller) InitGitHubLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	if !ctrl.githubConfigured() {
		postJSON(w, 503, map[string]string{"error": "GitHub login is not configured on the API"})
		return
	}

	_, err := ctrl.queries.GetUser(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 401, map[string]string{"error": "sign in again before connecting GitHub"})
		return
	}

	if err != nil {
		postJSON(w, 503, map[string]string{"error": "profile unavailable"})
		return
	}

	var payload struct {
		RedirectURI string `json:"cli_redirect_uri"`
		Challenge   string `json:"code_challenge"`
		State       string `json:"cli_state"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil ||
		utils.ValidateCLILogin(payload.RedirectURI, payload.Challenge, payload.State) != nil {
		postJSON(w, 400, map[string]string{"error": "invalid CLI login request"})
		return
	}

	state, err := utils.CreateProviderOAuthState(
		"github",
		payload.RedirectURI,
		payload.Challenge,
		payload.State,
		userID,
	)
	if err != nil {
		postJSON(w, 500, map[string]string{"error": "failed to start GitHub connection"})
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	postJSON(w, 200, map[string]string{"url": "/oauth/github?request=" + url.QueryEscape(state)})
}

func (ctrl *Controller) InitGitHubLogin(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	state := query.Get("request")

	var (
		err    error
		claims *utils.OAuthStateClaims
	)
	if state != "" {
		claims, err = utils.ParseOAuthState(state)
		if err != nil || claims.Provider != "github" || claims.LinkUserID <= 0 || claims.RedirectURI == "" {
			http.Error(w, "invalid GitHub connection request", 400)
			return
		}
	} else {
		redirect := query.Get("cli_redirect_uri")
		challenge := query.Get("code_challenge")
		cliState := query.Get("cli_state")

		if redirect != "" || challenge != "" || cliState != "" {
			if err := utils.ValidateCLILogin(redirect, challenge, cliState); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}

		state, err = utils.CreateProviderOAuthState("github", redirect, challenge, cliState, 0)
		if err != nil {
			http.Error(w, "failed to create OAuth state", 500)
			return
		}

		claims, err = utils.ParseOAuthState(state)
		if err != nil {
			http.Error(w, "invalid OAuth state", 500)
			return
		}
	}

	if !ctrl.githubConfigured() {
		if claims.RedirectURI != "" {
			redirectCLILogin(w, r, claims, "", "GitHub login is not configured on the API")
		} else {
			http.Error(w, "GitHub login is not configured on the API", 503)
		}

		return
	}

	verifier := oauth2.GenerateVerifier()

	http.SetCookie(w, githubCookie(r, "oauth_github_state", state, 600))
	http.SetCookie(w, githubCookie(r, "oauth_github_pkce", verifier, 600))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")

	authURL := ctrl.oauth.GitHub.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (ctrl *Controller) GitHubCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")

	cookie, err := r.Cookie("oauth_github_state")
	if err != nil || state == "" || cookie.Value != state {
		http.Error(w, "invalid OAuth state", 400)
		return
	}

	claims, err := utils.ParseOAuthState(state)
	if err != nil || claims.Provider != "github" {
		http.Error(w, "invalid OAuth state", 400)
		return
	}

	pkce, err := r.Cookie("oauth_github_pkce")
	if err != nil || len(pkce.Value) != 43 {
		http.Error(w, "invalid OAuth verifier", 400)
		return
	}

	http.SetCookie(w, githubCookie(r, "oauth_github_state", "", -1))
	http.SetCookie(w, githubCookie(r, "oauth_github_pkce", "", -1))

	fail := func(message string, code int) {
		if claims.RedirectURI != "" {
			redirectCLILogin(w, r, claims, "", message)
		} else {
			http.Error(w, message, code)
		}
	}
	if r.URL.Query().Get("error") != "" {
		fail("GitHub login was cancelled or denied", 400)
		return
	}

	if !ctrl.githubConfigured() {
		fail("GitHub login is not configured on the API", 503)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		fail("missing code", 400)
		return
	}

	client := ctrl.oauthClient()
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, client)

	token, err := ctrl.oauth.GitHub.Exchange(ctx, code, oauth2.VerifierOption(pkce.Value))
	if err != nil {
		fail("failed to exchange GitHub code", 502)
		return
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		fail("failed to create GitHub profile request", 500)
		return
	}

	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	request.Header.Set("Accept", "application/vnd.github+json")

	response, err := client.Do(request)
	if err != nil {
		fail("failed to fetch GitHub profile", 502)
		return
	}
	defer response.Body.Close()

	var user githubUser
	if response.StatusCode != 200 ||
		json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&user) != nil ||
		user.ID <= 0 ||
		!validGitHubLogin(user.Login) {
		fail("invalid GitHub profile response", 502)
		return
	}

	id := pgtype.Text{String: strconv.FormatInt(user.ID, 10), Valid: true}

	var account db.User
	if claims.LinkUserID > 0 {
		account, err = ctrl.queries.GetUser(ctx, claims.LinkUserID)
		if errors.Is(err, pgx.ErrNoRows) {
			fail("Your Maruvo account no longer exists. Sign in again before connecting GitHub.", 401)
			return
		}

		if err != nil {
			fail("Your profile is temporarily unavailable. Try connecting GitHub again.", 503)
			return
		}

		if account.GithubID.Valid && account.GithubID.String != id.String {
			fail("This Maruvo account already has GitHub connected as @"+account.GithubLogin+
				". Use that GitHub account to sign in.", 409)

			return
		}

		account, err = ctrl.queries.LinkGitHub(ctx, db.LinkGitHubParams{
			ID:       claims.LinkUserID,
			GithubID: id, GithubLogin: user.Login,
		})
	} else {
		account, err = ctrl.queries.SignInGitHub(ctx, db.SignInGitHubParams{
			GithubID:    id,
			GithubLogin: user.Login, Avatar: pgtype.Text{String: user.Avatar, Valid: user.Avatar != ""},
		})
	}

	if err != nil {
		var databaseError *pgconn.PgError
		if errors.Is(err, pgx.ErrNoRows) {
			fail("Your account changed while connecting GitHub. Sign in again and retry.", 409)
		} else if errors.As(err, &databaseError) && databaseError.Code == "23505" &&
			databaseError.ConstraintName == "users_github_id_key" {
			fail("This GitHub account belongs to another Maruvo account. Sign in with GitHub to use it, "+
				"or choose a different GitHub account to connect here.", 409)
		} else {
			log.Printf("GitHub identity: %v", err)
			fail("failed to save GitHub identity", 503)
		}

		return
	}

	ctrl.finishOAuthLogin(w, r, claims, account, "GitHub")
}

func validGitHubLogin(login string) bool {
	if len(login) == 0 || len(login) > 39 {
		return false
	}

	for _, r := range login {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' {
			return false
		}
	}

	return true
}
