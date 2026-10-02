package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/rajandhamala/Maruvo/pb"
)

func (c *Controller) Demo(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload); err != nil {
		postJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid demo payload"})
		return
	}

	payload.Message = strings.TrimSpace(payload.Message)
	if payload.Message == "" || len(payload.Message) > 256 {
		postJSON(
			w,
			http.StatusBadRequest,
			map[string]string{"error": "message must contain 1 to 256 bytes after trimming"},
		)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	response, err := pb.NewSolanaServiceClient(c.rpc).Demo(ctx, &pb.DemoRequest{Message: payload.Message})
	if err != nil {
		postJSON(w, http.StatusBadGateway, map[string]string{"error": "Rust service unavailable"})
		return
	}

	postJSON(w, http.StatusOK, map[string]string{"message": response.Message, "service": response.Service})
}
