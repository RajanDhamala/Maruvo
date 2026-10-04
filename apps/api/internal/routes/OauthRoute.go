package routes

import (
	"net/http"

	controller "github.com/rajandhamala/Maruvo/internal/controllers"
)

func OauthRoute(app *http.ServeMux, ctrl *controller.Controller) {
	app.HandleFunc("GET /oauth", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Oauth route is up and healthy \n"))
		return
	})
	app.HandleFunc("GET /oauth/google", ctrl.InitGoogleLogin)
	app.HandleFunc("GET /oauth/callback/google", ctrl.GoogleCallback)
	app.HandleFunc("GET /oauth/github", ctrl.InitGitHubLogin)
	app.HandleFunc("GET /oauth/callback/github", ctrl.GitHubCallback)
	app.HandleFunc("POST /oauth/github/link", ctrl.Auth(ctrl.InitGitHubLink))
	app.HandleFunc("POST /oauth/cli/token", ctrl.ExchangeCLIToken)
	app.HandleFunc("GET /me", ctrl.GetMe)
}
