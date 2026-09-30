package routes

import (
	"net/http"

	controller "github.com/rajandhamala/Maruvo/internal/controllers"
)

func UserRouter(app *http.ServeMux, ctrl *controller.Controller) {
	app.HandleFunc("GET /ws", ctrl.Test)
}
