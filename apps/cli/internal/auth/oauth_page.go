package auth

import (
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed oauth_page.html
var loginPageHTML string

var loginPageTemplate = template.Must(template.New("login").Parse(loginPageHTML))

type loginPage struct {
	Status, Heading, Message string
	Failed                   bool
}

func writeLoginPage(w http.ResponseWriter, status int, failed bool) {
	page := loginPage{
		Status:  "Response received",
		Heading: "Return to your terminal.",
		Message: "Maruvo received your sign-in response. Continue in your terminal to finish signing in.",
	}
	if failed {
		page = loginPage{
			Status:  "Sign-in interrupted",
			Heading: "Try signing in again.",
			Message: "Maruvo couldn't complete this sign-in. Return to your terminal to retry.",
			Failed:  true,
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
	w.WriteHeader(status)
	_ = loginPageTemplate.Execute(w, page)
}
