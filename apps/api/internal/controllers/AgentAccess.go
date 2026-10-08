package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/middlewares"
	"github.com/rajandhamala/Maruvo/internal/utils"
)

const agentTokenPrefix = "mru_agent_"

type agentGrantView struct {
	utils.AgentAccess
	CreatedAt time.Time  `json:"created_at"`
	RevokedAt *time.Time `json:"revoked_at"`
}

func grantView(grant db.AgentGrant) agentGrantView {
	view := agentGrantView{
		AgentAccess: utils.AgentAccess{
			ID: uuid.UUID(grant.ID.Bytes).String(), OwnerID: grant.OwnerID, PostID: grant.PostID,
			Name: grant.Name, Permissions: grant.Permissions, ExpiresAt: grant.ExpiresAt.Time,
		},
		CreatedAt: grant.CreatedAt.Time,
	}
	if grant.RevokedAt.Valid {
		view.RevokedAt = &grant.RevokedAt.Time
	}

	return view
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func requestAgent(ctx context.Context) *utils.AgentAccess {
	grant, _ := ctx.Value(utils.AgentKey).(*utils.AgentAccess)
	return grant
}

func (c *Controller) resolveAgent(ctx context.Context, token string) (db.AgentGrant, error) {
	var empty db.AgentGrant

	secret, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, agentTokenPrefix))
	if !strings.HasPrefix(token, agentTokenPrefix) || err != nil || len(secret) != 32 {
		return empty, &workspaceFailure{401, "invalid agent credential"}
	}

	grant, err := c.queries.ResolveAgentGrant(ctx, tokenHash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, &workspaceFailure{401, "agent credential expired, revoked, or invalid"}
	}

	if err != nil {
		return empty, &workspaceFailure{503, "agent access unavailable"}
	}

	return grant, nil
}

func agentRequestAllowed(grant utils.AgentAccess, r *http.Request) bool {
	permission := ""

	switch r.Pattern {
	case "GET /posts/{id}/workspace",
		"GET /posts/{id}/history",
		"GET /posts/{id}/files/{file}",
		"GET /ws",
		"GET /ws/files",
		"POST /posts/info":
		permission = "read"
	case "POST /posts/{id}/messages", "POST /posts/{id}/activity":
		permission = "message"
	case "POST /posts/{id}/files":
		permission = "upload"
	case "POST /posts/{id}/submit":
		permission = "submit"
	case "POST /posts/{id}/review/changes":
		permission = "request-changes"
	}

	if permission == "" || !slices.Contains(grant.Permissions, permission) {
		return false
	}

	if r.Pattern == "POST /posts/info" {
		return true // The decoded body ID is checked by postIDPayload.
	}

	id := r.PathValue("id")
	if r.Pattern == "GET /ws" || r.Pattern == "GET /ws/files" {
		id = r.URL.Query().Get("post_id")
	}

	postID, err := strconv.ParseInt(id, 10, 64)

	return err == nil && postID == grant.PostID
}

func (c *Controller) Auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || scheme != "Bearer" || !strings.HasPrefix(token, agentTokenPrefix) {
			middlewares.Auth(next)(w, r)
			return
		}

		grant, err := c.resolveAgent(r.Context(), token)
		if err != nil {
			workspaceError(w, err)
			return
		}

		access := grantView(grant).AgentAccess
		if !agentRequestAllowed(access, r) {
			postJSON(
				w,
				403,
				map[string]string{"error": "agent credential does not allow this action or task"},
			)

			return
		}

		ctx := context.WithValue(r.Context(), utils.AgentKey, &access)
		ctx = context.WithValue(ctx, utils.UserKey, &utils.UserJWT{ID: strconv.FormatInt(grant.OwnerID, 10)})
		next(w, r.WithContext(ctx))
	}
}

func (c *Controller) checkSession(ctx context.Context, r *http.Request) error {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if requestAgent(r.Context()) != nil {
		_, err := c.resolveAgent(ctx, token)
		return err
	}

	if _, err := utils.VerifyUserToken(token); err != nil {
		return &workspaceFailure{401, "session expired"}
	}

	return nil
}

func lockAgent(ctx context.Context, q *db.Queries) error {
	access := requestAgent(ctx)
	if access == nil {
		return nil
	}

	var id pgtype.UUID
	if err := id.Scan(access.ID); err != nil {
		return err
	}

	_, err := q.LockActiveAgentGrant(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return &workspaceFailure{401, "agent credential expired or revoked"}
	}

	if err != nil {
		return &workspaceFailure{503, "agent access unavailable"}
	}

	return nil
}

func (c *Controller) watchAgentSession(ctx context.Context, cancel context.CancelFunc, r *http.Request) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if c.checkSession(ctx, r) != nil {
				cancel()
				return
			}
		}
	}
}

func agentData(ctx context.Context, data any) (json.RawMessage, error) {
	payload, err := json.Marshal(data)
	if err != nil || requestAgent(ctx) == nil {
		return payload, err
	}

	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}

	access := requestAgent(ctx)
	fields["agent_grant_id"], fields["agent_name"] = access.ID, access.Name

	return json.Marshal(fields)
}

func (c *Controller) CreateAgentGrant(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := postUserID(w, r)
	if !ok {
		return
	}

	postID, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	var payload struct {
		Name             string   `json:"name"`
		Permissions      []string `json:"permissions"`
		ExpiresInSeconds int64    `json:"expires_in_seconds"`
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()

	if decoder.Decode(&payload) != nil || !validTaskText(payload.Name, 80, 320) ||
		strings.TrimSpace(
			payload.Name,
		) == "" || payload.ExpiresInSeconds < 60 || payload.ExpiresInSeconds > 604800 {
		postJSON(
			w,
			400,
			map[string]string{"error": "provide a name (1-80 characters) and expiry (60-604800 seconds)"},
		)

		return
	}

	permissions := slices.Clone(payload.Permissions)
	slices.Sort(permissions)
	permissions = slices.Compact(permissions)
	allowed := []string{"read", "message", "upload", "submit", "request-changes"}

	if !slices.Contains(permissions, "read") {
		postJSON(w, 400, map[string]string{"error": "agent grants require read permission"})
		return
	}

	for _, permission := range permissions {
		if !slices.Contains(allowed, permission) {
			postJSON(w, 400, map[string]string{"error": "unsupported agent permission: " + permission})
			return
		}
	}

	if _, err := c.queries.GetUser(r.Context(), ownerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			postJSON(w, 401, map[string]string{"error": "account no longer exists"})
		} else {
			postJSON(w, 503, map[string]string{"error": "agent access unavailable"})
		}

		return
	}

	post, err := c.queries.GetPost(r.Context(), postID)
	if !workspaceAccess(w, r, c.queries, post, err, ownerID) {
		return
	}

	if (post.Status == db.PostStatusCompleted || post.Status == db.PostStatusCancelled) &&
		(len(permissions) != 1 || permissions[0] != "read") {
		postJSON(w, 409, map[string]string{"error": "closed tasks allow read-only agent access"})
		return
	}

	if slices.Contains(permissions, "submit") && post.AcceptedBy.Int64 != ownerID {
		postJSON(w, 403, map[string]string{"error": "only the accepted worker can delegate submission"})
		return
	}

	if slices.Contains(permissions, "request-changes") {
		reviewer, err := c.queries.IsPostReviewer(
			r.Context(),
			db.IsPostReviewerParams{PostID: postID, UserID: ownerID},
		)
		if err != nil {
			workspaceError(w, err)
			return
		}

		if !reviewer {
			postJSON(w, 403, map[string]string{"error": "only the reviewer can delegate revision requests"})
			return
		}
	}

	tx, err := c.pool.Begin(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	q := db.New(tx)

	control, err := lockAgentControl(r.Context(), q, postID, ownerID)
	if err != nil {
		workspaceError(w, err)
		return
	}

	if control.Mode == "manual" {
		postJSON(
			w,
			409,
			map[string]string{
				"error": "agent access is paused for this task; switch to agent mode to resume",
			},
		)

		return
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		workspaceError(w, err)
		return
	}

	token := agentTokenPrefix + base64.RawURLEncoding.EncodeToString(secret)

	grant, err := q.CreateAgentGrant(r.Context(), db.CreateAgentGrantParams{
		ID:          pgtype.UUID{Bytes: uuid.New(), Valid: true},
		OwnerID:     ownerID,
		PostID:      postID,
		Name:        strings.TrimSpace(payload.Name),
		TokenHash:   tokenHash(token),
		Permissions: permissions,
		ExpiresAt: pgtype.Timestamptz{
			Time:  time.Now().UTC().Add(time.Duration(payload.ExpiresInSeconds) * time.Second),
			Valid: true,
		},
	})
	if err != nil {
		workspaceError(w, err)
		return
	}

	if err = tx.Commit(r.Context()); err != nil {
		workspaceError(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	postJSON(w, 201, map[string]any{"grant": grantView(grant), "token": token})
}

func (c *Controller) ListAgentGrants(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := postUserID(w, r)
	if !ok {
		return
	}

	postID, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	grants, err := c.queries.ListAgentGrants(
		r.Context(),
		db.ListAgentGrantsParams{OwnerID: ownerID, PostID: postID},
	)
	if err != nil {
		workspaceError(w, err)
		return
	}

	views := make([]agentGrantView, 0, len(grants))
	for _, grant := range grants {
		views = append(views, grantView(grant))
	}

	postJSON(w, 200, views)
}

func (c *Controller) RevokeAgentGrant(w http.ResponseWriter, r *http.Request) {
	ownerID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var id pgtype.UUID
	if id.Scan(r.PathValue("grant")) != nil || !id.Valid {
		postJSON(w, 400, map[string]string{"error": "invalid grant ID"})
		return
	}

	grant, err := c.queries.RevokeAgentGrant(r.Context(), db.RevokeAgentGrantParams{ID: id, OwnerID: ownerID})
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "grant not found"})
		return
	}

	if err != nil {
		workspaceError(w, err)
		return
	}

	postJSON(w, 200, grantView(grant))
}
