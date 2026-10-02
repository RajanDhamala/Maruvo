package utils

import (
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

type OAuthConfig struct {
	Google *oauth2.Config
}

func NewOAuthConfig() *OAuthConfig {
	return &OAuthConfig{
		Google: &oauth2.Config{
			ClientID:     os.Getenv("GOOGLE_CLIENT_ID"),
			ClientSecret: os.Getenv("GOOGLE_CLIENT_SECRET"),
			RedirectURL:  "http://127.0.0.1:3000/oauth/callback/google",

			Scopes: []string{
				"openid",
				"email",
				"profile",
			},

			Endpoint: google.Endpoint,
		},
	}
}
