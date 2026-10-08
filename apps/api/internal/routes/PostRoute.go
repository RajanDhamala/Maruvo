package routes

import (
	"net/http"

	controller "github.com/rajandhamala/Maruvo/internal/controllers"
)

func PostRoute(app *http.ServeMux, ctrl *controller.Controller) {
	app.HandleFunc("GET /agent/inbox", ctrl.Auth(ctrl.RemoteInbox))
	app.HandleFunc("POST /posts/{id}/activity", ctrl.Auth(ctrl.UpdateRemoteActivity))
	app.HandleFunc("GET /posts/{id}/agent-control", ctrl.Auth(ctrl.GetAgentControl))
	app.HandleFunc("PUT /posts/{id}/agent-control", ctrl.Auth(ctrl.SetAgentControl))
	app.HandleFunc("GET /agent/chats", ctrl.Auth(ctrl.ListAgentChats))
	app.HandleFunc("GET /agent/chats/{chat}", ctrl.Auth(ctrl.GetAgentChat))
	app.HandleFunc("PUT /agent/chats/{chat}", ctrl.Auth(ctrl.SaveAgentChat))
	app.HandleFunc("GET /agent-offers", ctrl.Auth(ctrl.ListAgentOffers))
	app.HandleFunc("GET /agent-offers/mine", ctrl.Auth(ctrl.OwnAgentOffer))
	app.HandleFunc("PUT /agent-offers/mine", ctrl.Auth(ctrl.SaveAgentOffer))
	app.HandleFunc("POST /agent-offers/mine/lease", ctrl.Auth(ctrl.LeaseAgentOffer))
	app.HandleFunc("DELETE /agent-offers/mine/lease", ctrl.Auth(ctrl.LeaseAgentOffer))
	app.HandleFunc("GET /posts", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Post route is up and healthy \n"))
		return
	})

	// for better route readiblity and mangement we will later migrate to chi

	app.HandleFunc("POST /posts/create", ctrl.Auth(ctrl.CreatePost))
	app.HandleFunc("DELETE /posts/del", ctrl.Auth(ctrl.DeletePost))
	app.HandleFunc("POST /posts/urs", ctrl.Auth(ctrl.GetUrPosts))
	app.HandleFunc("POST /posts/feed", ctrl.Auth(ctrl.DescLevelPost))
	app.HandleFunc("POST /posts/status", ctrl.Auth(ctrl.UpdatePostStatus))
	app.HandleFunc("POST /posts/accept", ctrl.Auth(ctrl.AcceptPost))
	app.HandleFunc("POST /posts/recover", ctrl.Auth(ctrl.RecoverPost))
	app.HandleFunc("POST /posts/info", ctrl.Auth(ctrl.PostInfo))
	app.HandleFunc("POST /posts/fund", ctrl.Auth(ctrl.PreparePostFunding))
	app.HandleFunc("POST /posts/fund/submit", ctrl.Auth(ctrl.SubmitPostFunding))
	app.HandleFunc("GET /posts/{id}/workspace", ctrl.Auth(ctrl.GetWorkspace))
	app.HandleFunc("GET /posts/{id}/history", ctrl.Auth(ctrl.WorkspaceHistory))
	app.HandleFunc("POST /posts/{id}/messages", ctrl.Auth(ctrl.WorkspaceMessage))
	app.HandleFunc("POST /posts/{id}/files", ctrl.Auth(ctrl.UploadWorkspaceFile))
	app.HandleFunc("GET /ws/files", ctrl.Auth(ctrl.WorkspaceFileSocket))
	app.HandleFunc("GET /posts/{id}/files/{file}", ctrl.Auth(ctrl.DownloadWorkspaceFile))
	app.HandleFunc("POST /posts/{id}/submit", ctrl.Auth(ctrl.SubmitWorkspace))
	app.HandleFunc("POST /posts/{id}/review/changes", ctrl.Auth(ctrl.RequestPostChanges))
	app.HandleFunc("POST /posts/{id}/settle", ctrl.Auth(ctrl.PreparePostSettlement))
	app.HandleFunc("POST /posts/{id}/settle/submit", ctrl.Auth(ctrl.SubmitPostSettlement))
	app.HandleFunc("POST /posts/{id}/agents", ctrl.Auth(ctrl.CreateAgentGrant))
	app.HandleFunc("GET /posts/{id}/agents", ctrl.Auth(ctrl.ListAgentGrants))
	app.HandleFunc("POST /agents/{grant}/revoke", ctrl.Auth(ctrl.RevokeAgentGrant))
	app.HandleFunc("GET /wallet", ctrl.Auth(ctrl.GetWallet))
	app.HandleFunc("POST /wallet/challenge", ctrl.Auth(ctrl.WalletChallenge))
	app.HandleFunc("POST /wallet/link", ctrl.Auth(ctrl.LinkWallet))
}
