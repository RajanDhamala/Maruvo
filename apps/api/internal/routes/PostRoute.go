package routes

import (
	"net/http"

	controller "github.com/rajandhamala/Maruvo/internal/controllers"
	middleware "github.com/rajandhamala/Maruvo/internal/middlewares"
)

func PostRoute(app *http.ServeMux, ctrl *controller.Controller) {
	app.HandleFunc("GET /posts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Post route is up and healthy \n"))
		return
	})

	// for better route readiblity and mangement we will later migrate to chi

	app.HandleFunc("POST /posts/create", middleware.Auth(ctrl.CreatePost))
	app.HandleFunc("DELETE /posts/del", middleware.Auth(ctrl.DeletePost))
	app.HandleFunc("POST /posts/urs", middleware.Auth(ctrl.GetUrPosts))
	app.HandleFunc("POST /posts/feed", middleware.Auth(ctrl.DescLevelPost))
	app.HandleFunc("POST /posts/status", middleware.Auth(ctrl.UpdatePostStatus))
	app.HandleFunc("POST /posts/accept", middleware.Auth(ctrl.AcceptPost))
	app.HandleFunc("POST /posts/info", middleware.Auth(ctrl.PostInfo))
	app.HandleFunc("POST /posts/fund", middleware.Auth(ctrl.PreparePostFunding))
	app.HandleFunc("POST /posts/fund/submit", middleware.Auth(ctrl.SubmitPostFunding))
	app.HandleFunc("GET /posts/{id}/workspace", middleware.Auth(ctrl.GetWorkspace))
	app.HandleFunc("POST /posts/{id}/messages", middleware.Auth(ctrl.WorkspaceMessage))
	app.HandleFunc("POST /posts/{id}/files", middleware.Auth(ctrl.UploadWorkspaceFile))
	app.HandleFunc("GET /ws/files", middleware.Auth(ctrl.WorkspaceFileSocket))
	app.HandleFunc("GET /posts/{id}/files/{file}", middleware.Auth(ctrl.DownloadWorkspaceFile))
	app.HandleFunc("POST /posts/{id}/submit", middleware.Auth(ctrl.SubmitWorkspace))
	app.HandleFunc("POST /posts/{id}/review/changes", middleware.Auth(ctrl.RequestPostChanges))
	app.HandleFunc("POST /posts/{id}/settle", middleware.Auth(ctrl.PreparePostSettlement))
	app.HandleFunc("POST /posts/{id}/settle/submit", middleware.Auth(ctrl.SubmitPostSettlement))
	app.HandleFunc("GET /wallet", middleware.Auth(ctrl.GetWallet))
	app.HandleFunc("POST /wallet/challenge", middleware.Auth(ctrl.WalletChallenge))
	app.HandleFunc("POST /wallet/link", middleware.Auth(ctrl.LinkWallet))
}
