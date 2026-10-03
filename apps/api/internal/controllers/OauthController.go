package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"golang.org/x/oauth2"
)

type GoogleUser struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func (ctrl *Controller) InitGoogleLogin(w http.ResponseWriter, r *http.Request) {
	redirectURI := r.URL.Query().Get("cli_redirect_uri")
	challenge := r.URL.Query().Get("code_challenge")
	cliState := r.URL.Query().Get("cli_state")

	var (
		state string
		err   error
	)
	if redirectURI != "" || challenge != "" || cliState != "" {
		if err := utils.ValidateCLILogin(redirectURI, challenge, cliState); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		state, err = utils.CreateCLIOAuthState(redirectURI, challenge, cliState)
	} else {
		state, err = utils.CreateOAuthState()
	}

	if err != nil {
		http.Error(w, "failed to create OAuth state", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Value:    state,
		Path:     "/oauth/callback/google",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})

	authURL := ctrl.oauth.Google.AuthCodeURL(state)
	if redirectURI != "" {
		authURL = ctrl.oauth.Google.AuthCodeURL(state, oauth2.SetAuthURLParam("prompt", "select_account"))
	}

	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (ctrl *Controller) GetMe(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")

	scheme, token, ok := strings.Cut(authHeader, " ")
	if !ok || scheme != "Bearer" || token == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := utils.VerifyUserToken(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	id, err := strconv.ParseInt(user.ID, 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	profile, err := ctrl.queries.GetUser(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "account no longer exists; sign in again", http.StatusUnauthorized)
		return
	}

	if err != nil {
		http.Error(w, "profile unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	postJSON(w, 200, map[string]any{
		"id": user.ID, "username": profile.Username, "email": profile.Email.String,
		"avatar": profile.Avatar.String, "github_login": profile.GithubLogin,
		"github_url":       githubProfileURL(profile.GithubLogin),
		"google_connected": profile.GoogleID.Valid, "github_connected": profile.GithubID.Valid,
	})
}

func (ctrl *Controller) GoogleCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")

	cookie, err := r.Cookie("oauth_state")
	if err != nil || state == "" || cookie.Value != state {
		http.Error(w, "invalid OAuth state", http.StatusBadRequest)
		return
	}

	oauthState, err := utils.ParseOAuthState(state)
	if err != nil || (oauthState.Provider != "" && oauthState.Provider != "google") {
		http.Error(w, "invalid OAuth state", http.StatusBadRequest)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "oauth_state",
		Path:     "/oauth/callback/google",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})

	fail := func(message string, status int) {
		if oauthState.RedirectURI != "" {
			redirectCLILogin(w, r, oauthState, "", message)
			return
		}

		http.Error(w, message, status)
	}
	if r.URL.Query().Get("error") != "" {
		fail("Google login was cancelled or denied", http.StatusBadRequest)
		return
	}

	code := r.URL.Query().Get("code")

	if code == "" {
		fail("missing code", http.StatusBadRequest)
		return
	}

	client := ctrl.oauthClient()
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, client)

	token, err := ctrl.oauth.Google.Exchange(ctx, code)
	if err != nil {
		fail("failed to exchange code", http.StatusBadRequest)
		return
	}

	req, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodGet,
		"https://www.googleapis.com/oauth2/v2/userinfo",
		nil,
	)
	if err != nil {
		fail("failed to create userinfo request", http.StatusInternalServerError)
		return
	}

	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	res, err := client.Do(req)
	if err != nil {
		fail("failed to fetch google user", http.StatusBadGateway)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		fail("google userinfo failed", http.StatusBadGateway)
		return
	}

	var user GoogleUser

	if err := json.NewDecoder(res.Body).Decode(&user); err != nil {
		fail("failed to decode google user", http.StatusInternalServerError)
		return
	}

	if user.ID == "" || user.Email == "" {
		fail("invalid Google profile response", http.StatusBadGateway)
		return
	}

	dbUser, err := ctrl.queries.CheckIfUserExist(
		r.Context(),
		pgtype.Text{
			String: user.ID,
			Valid:  true,
		},
	)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			fail("failed to look up user", http.StatusInternalServerError)
			return
		}

		dbUser, err = ctrl.queries.RegisterUser(
			r.Context(),
			db.RegisterUserParams{
				Email: pgtype.Text{String: user.Email, Valid: true},
				GoogleID: pgtype.Text{
					String: user.ID,
					Valid:  true,
				},
				Username: user.Name,
				Avatar: pgtype.Text{
					String: user.Picture,
					Valid:  true,
				},
			},
		)
		if err != nil {
			fail("failed to register user", http.StatusInternalServerError)
			return
		}
	}

	ctrl.finishOAuthLogin(w, r, oauthState, dbUser, "Google")
}

func (ctrl *Controller) oauthClient() *http.Client {
	if ctrl.oauth.Client != nil {
		return ctrl.oauth.Client
	}

	return &http.Client{Timeout: 10 * time.Second}
}

func (ctrl *Controller) finishOAuthLogin(w http.ResponseWriter, r *http.Request,
	oauthState *utils.OAuthStateClaims, dbUser db.User, provider string) {
	fail := func(message string, status int) {
		if oauthState.RedirectURI != "" {
			redirectCLILogin(w, r, oauthState, "", message)
		} else {
			http.Error(w, message, status)
		}
	}
	tempUser := utils.UserJWT{
		ID:       strconv.FormatInt(dbUser.ID, 10),
		Email:    dbUser.Email.String,
		Username: dbUser.Username,
		Avtar:    dbUser.Avatar.String,
		GoogleId: dbUser.GoogleID.String,
	}

	stringToken, _, err := utils.CreateUserToken(&tempUser)
	if err != nil {
		fail("failed to create access token", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-store")

	if oauthState.RedirectURI != "" {
		code, err := ctrl.cliLogins.Issue(r.Context(), stringToken, oauthState.CodeChallenge)
		if err != nil {
			fail("CLI login is temporarily unavailable", http.StatusServiceUnavailable)
			return
		}

		redirectCLILogin(w, r, oauthState, code, "")

		return
	}

	response := map[string]string{
		"token":   stringToken,
		"message": provider + " login successful",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func redirectCLILogin(
	w http.ResponseWriter,
	r *http.Request,
	state *utils.OAuthStateClaims,
	code, loginError string,
) {
	callback, err := url.Parse(state.RedirectURI)
	if err != nil {
		http.Error(w, "invalid CLI callback", http.StatusInternalServerError)
		return
	}

	query := callback.Query()
	query.Set("state", state.CLIState)

	if code != "" {
		query.Set("code", code)
	} else {
		query.Set("error", loginError)
	}

	callback.RawQuery = query.Encode()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, callback.String(), http.StatusSeeOther)
}

type CLITokenPayload struct {
	Code         string `json:"code"`
	CodeVerifier string `json:"code_verifier"`
}

func (ctrl *Controller) ExchangeCLIToken(w http.ResponseWriter, r *http.Request) {
	var payload CLITokenPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload); err != nil {
		http.Error(w, "invalid CLI token payload", http.StatusBadRequest)
		return
	}

	token, err := ctrl.cliLogins.Redeem(r.Context(), payload.Code, payload.CodeVerifier)
	if err != nil {
		if errors.Is(err, utils.ErrInvalidCLILogin) {
			http.Error(w, "invalid or expired CLI login", http.StatusUnauthorized)
		} else {
			http.Error(w, "CLI login is temporarily unavailable", http.StatusServiceUnavailable)
		}

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"token": token, "message": "Login successful"})
}
