package routes

import (
	"net/http"

	controller "github.com/rajandhamala/Maruvo/internal/controllers"
)

func UserRouter(app *http.ServeMux, ctrl *controller.Controller) {
	app.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("User route is up and healthy \n"))
		return
	})

	app.HandleFunc("GET /ws", ctrl.Auth(ctrl.WsHandler))
	app.HandleFunc("POST /demo", ctrl.Demo)
}
