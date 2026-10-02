package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"id":       user.ID,
		"username": user.Username,
		"email":    user.Email,
		"avatar":   user.Avtar,
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
	if err != nil {
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

	token, err := ctrl.oauth.Google.Exchange(r.Context(), code)
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

	res, err := http.DefaultClient.Do(req)
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
				Email: user.Email,
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

	tempUser := utils.UserJWT{
		ID:       strconv.FormatInt(dbUser.ID, 10),
		Email:    dbUser.Email,
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
		code := ctrl.cliLogins.Issue(stringToken, oauthState.CodeChallenge)
		redirectCLILogin(w, r, oauthState, code, "")

		return
	}

	response := map[string]string{
		"token":   stringToken,
		"message": "Google login successful",
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

	token, err := ctrl.cliLogins.Redeem(payload.Code, payload.CodeVerifier)
	if err != nil {
		http.Error(w, "invalid or expired CLI login", http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"token": token, "message": "Google login successful"})
}
