package utils

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type OAuthStateClaims struct {
	Provider      string `json:"provider,omitempty"`
	LinkUserID    int64  `json:"link_user_id,omitempty"`
	RedirectURI   string `json:"cli_redirect_uri,omitempty"`
	CodeChallenge string `json:"code_challenge,omitempty"`
	CLIState      string `json:"cli_state,omitempty"`
	jwt.RegisteredClaims
}

func CreateProviderOAuthState(
	provider, redirectURI, challenge, cliState string,
	linkUserID int64,
) (string, error) {
	temp, err := signOAuthState(OAuthStateClaims{
		Provider:      provider,
		LinkUserID:    linkUserID,
		RedirectURI:   redirectURI,
		CodeChallenge: challenge,
		CLIState:      cliState,
	})

	return temp, err
}

func CreateOAuthState() (string, error) {
	return signOAuthState(OAuthStateClaims{})
}

func CreateCLIOAuthState(redirectURI, challenge string, cliState string) (string, error) {
	return signOAuthState(OAuthStateClaims{
		RedirectURI: redirectURI, CodeChallenge: challenge, CLIState: cliState,
	})
}

func ValidateCLILogin(redirectURI, challenge, cliState string) error {
	callback, err := url.Parse(redirectURI)
	if err != nil {
		return errors.New("invalid CLI callback")
	}

	port, err := strconv.Atoi(callback.Port())
	if err != nil || port < 1 || port > 65535 || callback.Scheme != "http" ||
		callback.Hostname() != "127.0.0.1" ||
		callback.Path != "/callback" ||
		callback.User != nil ||
		callback.RawQuery != "" ||
		callback.Fragment != "" {
		return errors.New("CLI callback must be a localhost HTTP callback")
	}

	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	if err != nil || len(decoded) != 32 || len(challenge) != 43 || len(cliState) < 16 || len(cliState) > 128 {
		return errors.New("invalid CLI login state or PKCE challenge")
	}

	return nil
}

func signOAuthState(claims OAuthStateClaims) (string, error) {
	secret := os.Getenv("OAUTH_SCRF_SECRET")
	if secret == "" {
		return "", errors.New("oauth_scrf_secret is missing")
	}

	claims.RegisteredClaims = jwt.RegisteredClaims{
		ID:        rand.Text(),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(10 * time.Minute)),
	}

	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

func VerifyOAuthState(state string) error {
	_, err := ParseOAuthState(state)
	return err
}

func ParseOAuthState(state string) (*OAuthStateClaims, error) {
	secret := os.Getenv("OAUTH_SCRF_SECRET")
	if secret == "" {
		return nil, errors.New("oauth_scrf_secret is missing")
	}

	claims := &OAuthStateClaims{}

	_, err := jwt.ParseWithClaims(state, claims, func(_ *jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}

	if claims.ID == "" {
		return nil, errors.New("missing OAuth state nonce")
	}

	if claims.RedirectURI != "" {
		if err := ValidateCLILogin(claims.RedirectURI, claims.CodeChallenge, claims.CLIState); err != nil {
			return nil, err
		}
	}

	return claims, nil
}
