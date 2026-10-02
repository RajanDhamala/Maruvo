package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
)

const fileChunkSize = 64 << 10

type fileRequest struct {
	Action  string `json:"action"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Purpose string `json:"purpose"`
}

func fileView(file db.WorkspaceFile) db.ListWorkspaceFilesRow {
	return db.ListWorkspaceFilesRow{
		ID:         file.ID,
		PostID:     file.PostID,
		UploadedBy: file.UploadedBy,
		Name:       file.Name,
		Size:       file.Size,
		Sha256:     file.Sha256,
		CreatedAt:  file.CreatedAt,
		Purpose:    file.Purpose,
	}
}

func (c *Controller) WorkspaceFileSocket(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	postID, err := strconv.ParseInt(r.URL.Query().Get("post_id"), 10, 64)
	if err != nil || postID <= 0 {
		postJSON(w, 400, map[string]string{"error": "invalid post ID"})
		return
	}

	post, err := c.queries.GetPost(r.Context(), postID)
	if !workspaceAccess(w, r, c.queries, post, err, userID) {
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	conn.SetReadLimit(fileChunkSize)
	conn.SetReadDeadline(time.Now().Add(pongWait))

	write := func(event string, data any) error {
		conn.SetWriteDeadline(time.Now().Add(writeWait))
		return conn.WriteJSON(WsResponse{Event: event, Data: data})
	}

	var request fileRequest
	if err = conn.ReadJSON(&request); err != nil {
		return
	}

	var file db.WorkspaceFile

	switch request.Action {
	case "upload":
		file, err = c.receiveWorkspaceFile(ctx, r, conn, postID, userID, request)
	case "download":
		var id pgtype.UUID
		if id.Scan(request.ID) != nil {
			err = &workspaceFailure{400, "invalid file ID"}
			break
		}

		file, err = c.queries.GetWorkspaceFile(ctx, db.GetWorkspaceFileParams{ID: id, PostID: postID})
		if errors.Is(err, pgx.ErrNoRows) {
			err = &workspaceFailure{404, "file not found"}
		}

		if err != nil {
			break
		}

		if write("file.meta", fileView(file)) != nil {
			return
		}

		for offset := 0; offset < len(file.Content); offset += fileChunkSize {
			conn.SetWriteDeadline(time.Now().Add(writeWait))

			if conn.WriteMessage(
				websocket.BinaryMessage,
				file.Content[offset:min(len(file.Content), offset+fileChunkSize)],
			) != nil {
				return
			}
		}
	default:
		err = &workspaceFailure{400, "choose upload or download"}
	}

	if err != nil {
		code, message := 500, "file transfer failed"

		var failure *workspaceFailure
		if errors.As(err, &failure) {
			code, message = failure.code, failure.message
		}

		_ = write("error", map[string]any{"status": code, "message": message})

		return
	}

	_ = write("file.complete", fileView(file))
}

func (c *Controller) receiveWorkspaceFile(
	ctx context.Context,
	r *http.Request,
	conn *websocket.Conn,
	postID, userID int64,
	request fileRequest,
) (db.WorkspaceFile, error) {
	var empty db.WorkspaceFile

	hash, err := hex.DecodeString(request.SHA256)
	if !validFileName(request.Name) || request.Size <= 0 || request.Size > maxWorkspaceFile || err != nil ||
		len(hash) != sha256.Size {
		return empty, &workspaceFailure{400, "provide filename, size (1 byte to 10 MiB), and SHA-256"}
	}

	content := make([]byte, 0, request.Size)
	for int64(len(content)) < request.Size {
		conn.SetReadDeadline(time.Now().Add(pongWait))

		kind, chunk, err := conn.ReadMessage()
		if err != nil {
			return empty, err
		}

		if kind != websocket.BinaryMessage || len(chunk) == 0 ||
			int64(len(content)+len(chunk)) > request.Size {
			return empty, &workspaceFailure{400, "invalid file chunk"}
		}

		content = append(content, chunk...)
	}

	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != strings.ToLower(request.SHA256) {
		return empty, &workspaceFailure{400, "file integrity check failed"}
	}

	if _, err = utils.VerifyUserToken(
		strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "),
	); err != nil {
		return empty, &workspaceFailure{401, "session expired"}
	}

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	post, err := q.LockPost(ctx, postID)
	if err != nil {
		return empty, err
	}

	if !workspaceParticipant(post, userID) {
		reviewer, err := q.IsPostReviewer(ctx, db.IsPostReviewerParams{PostID: postID, UserID: userID})
		if err != nil {
			return empty, err
		}

		if !reviewer {
			return empty, &workspaceFailure{403, "workspace access denied"}
		}
	}

	if request.Purpose == "" {
		request.Purpose = "shared"
	}

	file, err := saveWorkspaceFile(ctx, q, post, userID, request.Name, request.Purpose, content)
	if err != nil {
		return empty, err
	}

	err = tx.Commit(ctx)

	return file, err
}
