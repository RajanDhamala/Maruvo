package utils

import (
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/github"
	"golang.org/x/oauth2/google"
)

type OAuthConfig struct {
	Google *oauth2.Config
	GitHub *oauth2.Config
	Client *http.Client
}

func NewOAuthConfig() *OAuthConfig {
	temp := OAuthConfig{
		Client: &http.Client{Timeout: 10 * time.Second},
		GitHub: &oauth2.Config{
			ClientID:     os.Getenv("GITHUB_CLIENT_ID"),
			ClientSecret: os.Getenv("GITHUB_CLIENT_SECRET"),
			RedirectURL:  callbackURL("GITHUB_REDIRECT_URL", "github"),
			Scopes:       []string{"read:user"},
			Endpoint:     github.Endpoint,
		},
		Google: &oauth2.Config{
			ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
			ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
			RedirectURL:  callbackURL("GOOGLE_REDIRECT_URL", "google"),

			Scopes: []string{
				"openid",
				"email",
				"profile",
			},
			Endpoint: google.Endpoint,
		},
	}

	return &temp
}

func callbackURL(key, provider string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return "http://127.0.0.1:3000/oauth/callback/" + provider
}
