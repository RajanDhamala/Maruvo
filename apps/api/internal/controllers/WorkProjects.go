package controller

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/rajandhamala/Maruvo/internal/work"
	"github.com/redis/go-redis/v9"
)

const workTokenPrefix = "mru_work_"
const workActorKey utils.Key = "work_actor"

type workGrant struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"project_id"`
	OwnerID        int64     `json:"owner_id"`
	Name           string    `json:"name"`
	Permissions    []string  `json:"permissions"`
	AllowedMembers []int64   `json:"allowed_members"`
	ExpiresAt      time.Time `json:"expires_at"`
}

func workError(w http.ResponseWriter, err error) {
	var f *work.Failure
	if errors.As(err, &f) {
		postJSON(w, f.Status, map[string]string{"error": f.Message})
		return
	}
	workspaceError(w, err)
}
func requestWorkActor(r *http.Request) work.Actor {
	if a, ok := r.Context().Value(workActorKey).(work.Actor); ok {
		return a
	}
	u, _ := r.Context().Value(utils.UserKey).(*utils.UserJWT)
	if u == nil {
		return work.Actor{}
	}
	id, _ := strconv.ParseInt(u.ID, 10, 64)
	return work.Actor{UserID: id}
}
func (c *Controller) workAuth(next http.HandlerFunc, w http.ResponseWriter, r *http.Request, token string) {
	if !strings.HasPrefix(r.URL.Path, "/work/") && r.URL.Path != "/work/projects" {
		postJSON(w, 403, map[string]string{"error": "work credentials only authorize project operations"})
		return
	}
	var g workGrant
	err := c.pool.QueryRow(r.Context(), `SELECT id::text,project_id::text,owner_id,name,permissions,allowed_members,expires_at FROM work_grants WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>NOW()`, tokenHash(token)).Scan(&g.ID, &g.ProjectID, &g.OwnerID, &g.Name, &g.Permissions, &g.AllowedMembers, &g.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 401, map[string]string{"error": "work credential expired, revoked, or invalid"})
		return
	}
	if err != nil {
		workError(w, err)
		return
	}
	if r.PathValue("project") != g.ProjectID {
		postJSON(w, 403, map[string]string{"error": "work credential is bound to another project"})
		return
	}
	a := work.Actor{UserID: g.OwnerID, GrantID: g.ID, Permissions: g.Permissions, AllowedMembers: g.AllowedMembers}
	ctx := context.WithValue(r.Context(), workActorKey, a)
	ctx = context.WithValue(ctx, utils.UserKey, &utils.UserJWT{ID: fmt.Sprint(a.UserID)})
	next(w, r.WithContext(ctx))
}
func workProject(ctx context.Context, db interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string, lock bool) (*work.Project, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, &work.Failure{Status: 400, Message: "invalid project ID"}
	}
	sql := "SELECT state FROM work_projects WHERE id=$1"
	if lock {
		sql += " FOR UPDATE"
	}
	var data []byte
	err := db.QueryRow(ctx, sql, id).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, &work.Failure{Status: 404, Message: "project not found"}
	}
	if err != nil {
		return nil, err
	}
	var p work.Project
	err = json.Unmarshal(data, &p)
	return &p, err
}
func (c *Controller) WorkProjects(w http.ResponseWriter, r *http.Request) {
	a := requestWorkActor(r)
	if r.Method == "POST" {
		c.WorkOperation(w, r)
		return
	}
	rows, err := c.pool.Query(r.Context(), `SELECT state FROM work_projects WHERE state->'members' @> $1::jsonb ORDER BY updated_at DESC LIMIT 100`, fmt.Sprintf(`[{"user_id":%d}]`, a.UserID))
	if err != nil {
		workError(w, err)
		return
	}
	defer rows.Close()
	result := []work.Project{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			break
		}
		var p work.Project
		if err = json.Unmarshal(data, &p); err != nil {
			break
		}
		if p.Role(a.UserID) != "" {
			p.Expire(time.Now())
			p.RefreshDependencies()
			result = append(result, p.View(a))
		} else {
			for _, m := range p.Members {
				if m.UserID == a.UserID && m.Status == "invited" {
					result = append(result, work.Project{ID: p.ID, Name: p.Name, OwnerID: p.OwnerID, Members: []work.Member{m}})
				}
			}
		}
	}
	if err == nil {
		err = rows.Err()
	}
	if err != nil {
		workError(w, err)
		return
	}
	postJSON(w, 200, map[string]any{"projects": result})
}
func (c *Controller) WorkProject(w http.ResponseWriter, r *http.Request) {
	p, err := workProject(r.Context(), c.pool, r.PathValue("project"), false)
	if err == nil {
		err = p.CheckAccess(requestWorkActor(r))
	}
	if err != nil {
		workError(w, err)
		return
	}
	p.Expire(time.Now())
	p.RefreshDependencies()
	if err = c.workObligations(r.Context(), c.pool, p); err != nil {
		workError(w, err)
		return
	}
	postJSON(w, 200, map[string]any{"project": p.View(requestWorkActor(r))})
}
func decodeWork(w http.ResponseWriter, r *http.Request) (work.Request, error) {
	var req work.Request
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 15<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, &work.Failure{Status: 400, Message: "invalid work payload: " + err.Error()}
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return req, &work.Failure{Status: 400, Message: "provide one JSON object"}
	}
	if !work.ValidID(req.OperationID) {
		return req, &work.Failure{Status: 400, Message: "operation_id requires 1-128 letters, digits, underscores or hyphens"}
	}
	return req, nil
}
func (c *Controller) WorkOperation(w http.ResponseWriter, r *http.Request) {
	req, err := decodeWork(w, r)
	if err != nil {
		workError(w, err)
		return
	}
	a := requestWorkActor(r)
	projectID := r.PathValue("project")
	if projectID == "" {
		req.Action = "create"
	} else if req.Action == "create" {
		postJSON(w, 400, map[string]string{"error": "create projects at /work/projects"})
		return
	}
	data, _ := json.Marshal(req)
	hash := sha256.Sum256(append([]byte(projectID+"\n"), data...))
	digest := hex.EncodeToString(hash[:])
	tx, err := c.pool.Begin(r.Context())
	if err != nil {
		workError(w, err)
		return
	}
	defer tx.Rollback(r.Context())
	// Serialize identical operation IDs even when two requests race across projects.
	_, err = tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", fmt.Sprint(a.UserID)+":"+req.OperationID)
	if err != nil {
		workError(w, err)
		return
	}
	var p *work.Project
	if req.Action != "create" {
		p, err = workProject(r.Context(), tx, projectID, true)
		if err != nil {
			workError(w, err)
			return
		}
		if req.Action != "join" {
			err = p.CheckAccess(a)
		}
		if err != nil {
			workError(w, err)
			return
		}
	}
	if err = workOperationPermission(a, p, req); err != nil {
		workError(w, err)
		return
	}
	// Grant revocation and mutations use the same project lock. Recheck after acquiring it.
	if a.GrantID != "" {
		var active bool
		err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM work_grants WHERE id=$1 AND revoked_at IS NULL AND expires_at>NOW())", a.GrantID).Scan(&active)
		if err != nil {
			workError(w, err)
			return
		}
		if !active {
			postJSON(w, 401, map[string]string{"error": "work credential expired or revoked"})
			return
		}
	}
	var oldHash, oldProject string
	var receipt []byte
	err = tx.QueryRow(r.Context(), "SELECT request_hash,project_id::text,response FROM work_receipts WHERE owner_id=$1 AND operation_id=$2", a.UserID, req.OperationID).Scan(&oldHash, &oldProject, &receipt)
	if err == nil {
		if oldHash != digest {
			postJSON(w, 409, map[string]string{"error": "operation_id was used with a different request"})
			return
		}
		if p == nil {
			p, err = workProject(r.Context(), tx, oldProject, true)
			if err == nil {
				err = p.CheckAccess(a)
			}
		}
		if err != nil {
			workError(w, err)
			return
		}
		c.workReceipt(w, req, a, oldProject, receipt)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		workError(w, err)
		return
	}
	err = nil
	var result any
	switch req.Action {
	case "create":
		if !a.Human() {
			err = &work.Failure{Status: 403, Message: "operators create projects"}
			break
		}
		p, err = work.New(uuid.NewString(), a.UserID, req)
		if err != nil {
			break
		}
		projectID = p.ID
		if p.PostID != 0 {
			var lead int64
			err = tx.QueryRow(r.Context(), "SELECT accepted_by FROM posts WHERE id=$1 AND status IN ('negotiating','in_progress') FOR UPDATE", p.PostID).Scan(&lead)
			if err != nil || lead != a.UserID {
				err = &work.Failure{Status: 403, Message: "only the accepted worker may attach this task"}
				break
			}
		}
		state, _ := json.Marshal(p)
		_, err = tx.Exec(r.Context(), "INSERT INTO work_projects(id,owner_id,post_id,state) VALUES($1,$2,NULLIF($3,0),$4)", p.ID, a.UserID, p.PostID, state)
		result = p
	case "upload", "import":
		result, err = c.workArtifact(r, tx, p, a, req)
	case "grant":
		result, err = c.issueWorkGrant(r, tx, p, a, req)
	case "revoke":
		if !a.Human() {
			err = &work.Failure{Status: 403, Message: "operators revoke their own harness grants"}
			break
		}
		tag, e := tx.Exec(r.Context(), "UPDATE work_grants SET revoked_at=COALESCE(revoked_at,NOW()) WHERE id::text=$1 AND project_id=$2 AND owner_id=$3", req.GrantID, p.ID, a.UserID)
		err = e
		if err == nil && tag.RowsAffected() == 0 {
			err = &work.Failure{Status: 404, Message: "grant not found"}
		}
		result = map[string]string{"grant_id": req.GrantID, "status": "revoked"}
	case "delegate":
		result, err = c.workDelegate(r, tx, p, a, req)
	case "collect":
		result, err = c.workCollect(r, tx, p, a, req)
	case "deliver":
		result, err = c.workDeliver(r, tx, p, a, req)
	default:
		if req.Action == "claim" || req.Action == "renew" || req.Action == "submit" || req.Action == "upload" {
			err = c.workFunding(r, tx, p.PostID)
		}
		if err == nil && req.Action == "invite" {
			var exists bool
			err = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)", req.UserID).Scan(&exists)
			if err == nil && !exists {
				err = &work.Failure{Status: 404, Message: "operator not found"}
			}
		}
		if err == nil && req.Action == "accept" {
			err = c.workCheckChild(r, tx, p.Assignment(req.AssignmentID))
		}
		if err == nil {
			result, err = p.Apply(a, req, time.Now().UTC(), uuid.NewString)
		}
		if err == nil && (req.Action == "remove" || req.Action == "control") {
			if req.Action == "remove" {
				_, err = tx.Exec(r.Context(), "UPDATE work_grants SET revoked_at=COALESCE(revoked_at,NOW()) WHERE project_id=$1 AND owner_id=$2", p.ID, req.UserID)
			} else if req.Mode != "active" {
				_, err = tx.Exec(r.Context(), "UPDATE work_grants SET revoked_at=COALESCE(revoked_at,NOW()) WHERE project_id=$1", p.ID)
			}
		}
	}
	if err != nil {
		workError(w, err)
		return
	}
	if err = c.workObligations(r.Context(), tx, p); err != nil {
		workError(w, err)
		return
	}
	p.Cursor++
	state, err := json.Marshal(p)
	if err == nil && len(state) > 3<<20 {
		err = &work.Failure{Status: 409, Message: "project history limit reached; start another project"}
	}
	if err != nil {
		workError(w, err)
		return
	}
	_, err = tx.Exec(r.Context(), "UPDATE work_projects SET state=$2,updated_at=NOW() WHERE id=$1", p.ID, state)
	if err == nil {
		_, err = tx.Exec(r.Context(), "INSERT INTO work_events(project_id,id,assignment_id,kind,actor_id,grant_id) VALUES($1,$2,$3,$4,$5,$6)", p.ID, p.Cursor, req.AssignmentID, req.Action, a.UserID, a.GrantID)
	}
	receipt, marshalErr := json.Marshal(map[string]any{"contract_version": "1", "operation_id": req.OperationID, "project_id": p.ID, "cursor": p.Cursor, "result": result})
	if err == nil {
		err = marshalErr
	}
	if err == nil {
		_, err = tx.Exec(r.Context(), "INSERT INTO work_receipts(owner_id,operation_id,request_hash,project_id,response) VALUES($1,$2,$3,$4,$5)", a.UserID, req.OperationID, digest, p.ID, receipt)
	}
	if err == nil {
		err = tx.Commit(r.Context())
	}
	if err != nil {
		workError(w, err)
		return
	}
	// Redis is a wakeup channel. Durable replay always comes from PostgreSQL.
	if c.redis != nil {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		_ = c.redis.Publish(ctx, "maruvo:work:"+p.ID, strconv.FormatInt(p.Cursor, 10)).Err()
	}
	c.workReceipt(w, req, a, p.ID, receipt)
}
func workSecret(a work.Actor, project, operation string) string {
	mac := hmac.New(sha256.New, []byte(os.Getenv("JWT_TOKEN")))
	mac.Write([]byte("maruvo-work-grant\x00" + project + "\x00" + fmt.Sprint(a.UserID) + "\x00" + operation))
	return workTokenPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (c *Controller) workReceipt(w http.ResponseWriter, r work.Request, a work.Actor, project string, data []byte) {
	if r.Action == "grant" {
		var receipt map[string]any
		_ = json.Unmarshal(data, &receipt)
		receipt["token"] = workSecret(a, project, r.OperationID)
		receipt["api_url_required"] = true
		postJSON(w, 200, receipt)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
func (c *Controller) issueWorkGrant(r *http.Request, tx pgx.Tx, p *work.Project, a work.Actor, req work.Request) (any, error) {
	if !a.Human() || p.Role(a.UserID) == "" {
		return nil, &work.Failure{Status: 403, Message: "operators issue credentials for their own harness"}
	}
	if req.ExpiresInSeconds == 0 {
		req.ExpiresInSeconds = 3600
	}
	if len(req.Name) == 0 || len(req.Name) > 80 || req.ExpiresInSeconds < 60 || req.ExpiresInSeconds > 604800 || os.Getenv("JWT_TOKEN") == "" {
		return nil, &work.Failure{Status: 400, Message: "name and expiry of 60 through 604800 seconds required"}
	}
	permissions := slices.Clone(req.Permissions)
	if !slices.Contains(permissions, "read") {
		permissions = append(permissions, "read")
	}
	for _, permission := range permissions {
		if !slices.Contains([]string{"read", "manage", "execute", "upload", "message"}, permission) {
			return nil, &work.Failure{Status: 400, Message: "invalid project permission"}
		}
	}
	if slices.Contains(permissions, "manage") {
		if !p.Manages(a) || len(req.AllowedMembers) == 0 {
			return nil, &work.Failure{Status: 403, Message: "coordinator grants require an authorized manager and explicit allowed_members"}
		}
		for _, id := range req.AllowedMembers {
			if p.Role(id) == "" {
				return nil, &work.Failure{Status: 403, Message: "allowed members must have active membership"}
			}
		}
	}
	if p.Status != "active" {
		return nil, &work.Failure{Status: 409, Message: "project is paused or cancelled"}
	}
	g := workGrant{uuid.NewString(), p.ID, a.UserID, req.Name, permissions, req.AllowedMembers, time.Now().UTC().Add(time.Duration(req.ExpiresInSeconds) * time.Second)}
	if g.AllowedMembers == nil {
		g.AllowedMembers = []int64{}
	}
	_, err := tx.Exec(r.Context(), "INSERT INTO work_grants(id,project_id,owner_id,name,token_hash,permissions,allowed_members,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", g.ID, p.ID, g.OwnerID, g.Name, tokenHash(workSecret(a, p.ID, req.OperationID)), g.Permissions, g.AllowedMembers, g.ExpiresAt)
	return g, err
}
func (c *Controller) workFunding(r *http.Request, tx pgx.Tx, post int64) error {
	if post == 0 {
		return nil
	}
	var funded int64
	err := tx.QueryRow(r.Context(), "SELECT p.id FROM posts p JOIN post_escrows e ON e.post_id=p.id WHERE p.id=$1 AND p.status='in_progress' AND e.state='confirmed' FOR UPDATE OF p,e", post).Scan(&funded)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return &work.Failure{Status: 409, Message: "wait for confirmed task funding before execution"}
	}
	return nil
}
func (c *Controller) workArtifact(r *http.Request, tx pgx.Tx, p *work.Project, a work.Actor, req work.Request) (any, error) {
	if p.Status != "active" || !a.Can("upload") {
		return nil, &work.Failure{Status: 403, Message: "active project and upload permission required"}
	}
	var sourcePost int64
	if req.Action == "import" {
		if !a.Human() || a.UserID != p.OwnerID || p.PostID == 0 {
			return nil, &work.Failure{Status: 403, Message: "lead must explicitly approve redistribution of each requester file"}
		}
		sourcePost = p.PostID
		var id pgtype.UUID
		if id.Scan(req.SourceFile) != nil {
			return nil, &work.Failure{Status: 400, Message: "invalid task file ID"}
		}
		q := db.New(tx)
		post, err := q.LockPost(r.Context(), p.PostID)
		if err != nil {
			return nil, err
		}
		file, err := q.GetWorkspaceFile(r.Context(), db.GetWorkspaceFileParams{ID: id, PostID: p.PostID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, &work.Failure{Status: 404, Message: "task artifact not found"}
		}
		if err != nil {
			return nil, err
		}
		if err = recordWorkspaceFileAccess(r, q, post, file, a.UserID); err != nil {
			return nil, err
		}
		req.ArtifactName, req.Content = file.Name, file.Content
	}
	if !work.ValidFilename(req.ArtifactName) || len(req.Content) == 0 || len(req.Content) > 10<<20 {
		return nil, &work.Failure{Status: 400, Message: "safe filename and 1 byte through 10 MiB content required"}
	}
	total := 0
	for _, art := range p.Artifacts {
		total += int(art.Size)
	}
	if len(p.Artifacts) >= 100 || total+len(req.Content) > 100<<20 {
		return nil, &work.Failure{Status: 409, Message: "project artifact limit reached (100 files / 100 MiB)"}
	}
	if req.AssignmentID != "" {
		s := p.Assignment(req.AssignmentID)
		if s == nil {
			return nil, &work.Failure{Status: 404, Message: "assignment not found"}
		}
		if _, err := p.LiveAttempt(a, s, req.AttemptID, time.Now()); err != nil {
			return nil, err
		}
		if err := c.workFunding(r, tx, p.PostID); err != nil {
			return nil, err
		}
	} else if !p.Manages(a) {
		return nil, &work.Failure{Status: 403, Message: "contributors upload outputs only to their active attempt"}
	}
	sum := sha256.Sum256(req.Content)
	art := work.Artifact{ID: uuid.NewString(), Name: req.ArtifactName, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(req.Content)), OwnerID: a.UserID, AssignmentID: req.AssignmentID, AttemptID: req.AttemptID, SourcePost: sourcePost, SourceFile: req.SourceFile}
	_, err := tx.Exec(r.Context(), "INSERT INTO work_artifacts(id,project_id,content) VALUES($1,$2,$3)", art.ID, p.ID, req.Content)
	if err == nil {
		p.Artifacts = append(p.Artifacts, art)
	}
	return art, err
}
func (c *Controller) WorkDownload(w http.ResponseWriter, r *http.Request) {
	p, err := workProject(r.Context(), c.pool, r.PathValue("project"), false)
	if err != nil {
		workError(w, err)
		return
	}
	id := r.PathValue("artifact")
	if !p.CanReadArtifact(requestWorkActor(r), id) {
		postJSON(w, 403, map[string]string{"error": "artifact was not shared with this assignment"})
		return
	}
	var content []byte
	err = c.pool.QueryRow(r.Context(), "SELECT content FROM work_artifacts WHERE id::text=$1 AND project_id=$2", id, p.ID).Scan(&content)
	if err != nil {
		workError(w, err)
		return
	}
	art := p.Artifact(id)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-SHA256", art.SHA256)
	w.Header().Set("Content-Length", fmt.Sprint(len(content)))
	_, _ = w.Write(content)
}
func (c *Controller) WorkEvents(w http.ResponseWriter, r *http.Request) {
	cursor, err := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	if r.URL.Query().Get("cursor") == "" {
		cursor = 0
		err = nil
	}
	wait, waitErr := strconv.Atoi(r.URL.Query().Get("wait"))
	if r.URL.Query().Get("wait") == "" {
		wait = 0
		waitErr = nil
	}
	if err != nil || cursor < 0 || waitErr != nil || wait < 0 || wait > 5 {
		postJSON(w, 400, map[string]string{"error": "cursor must be nonnegative; wait is 0 through 5 seconds"})
		return
	}
	var wake <-chan *redis.Message
	if c.redis != nil && wait > 0 {
		sub := c.redis.Subscribe(r.Context(), "maruvo:work:"+r.PathValue("project"))
		defer sub.Close()
		wake = sub.Channel()
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	a := requestWorkActor(r)
	for {
		p, e := workProject(r.Context(), c.pool, r.PathValue("project"), false)
		if e == nil {
			e = p.CheckAccess(a)
		}
		if e != nil {
			workError(w, e)
			return
		}
		if a.GrantID != "" {
			var valid bool
			e = c.pool.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM work_grants WHERE id=$1 AND revoked_at IS NULL AND expires_at>NOW())", a.GrantID).Scan(&valid)
			if e != nil {
				workError(w, e)
				return
			}
			if !valid {
				postJSON(w, 401, map[string]string{"error": "work credential revoked or expired"})
				return
			}
		}
		events := []map[string]any{}
		resync := cursor > p.Cursor
		rows, e := c.pool.Query(r.Context(), "SELECT id,assignment_id,kind,actor_id,grant_id,created_at FROM work_events WHERE project_id=$1 AND id>$2 ORDER BY id LIMIT 100", p.ID, cursor)
		if e != nil {
			workError(w, e)
			return
		}
		last := cursor
		for rows.Next() {
			var id, actor int64
			var assignment, kind, grant string
			var created time.Time
			e = rows.Scan(&id, &assignment, &kind, &actor, &grant, &created)
			if e != nil {
				break
			}
			last = id
			s := p.Assignment(assignment)
			visible := p.Manages(a) || p.OwnerID == a.UserID || s != nil && s.Assignee == a.UserID || kind == "control"
			if !visible && slices.Contains([]string{"accept", "retry", "cancel", "assign", "collect"}, kind) {
				for _, mine := range p.Assignments {
					if mine.Assignee == a.UserID && slices.Contains(mine.Dependencies, assignment) {
						visible = true
					}
				}
			}
			if visible {
				events = append(events, map[string]any{"id": id, "assignment_id": assignment, "kind": kind, "actor_id": actor, "grant_id": grant, "created_at": created})
			}
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			workError(w, e)
			return
		}
		if last > cursor || resync || !time.Now().Before(deadline) {
			if resync {
				last = p.Cursor
			}
			postJSON(w, 200, map[string]any{"events": events, "cursor": last, "resync_required": resync, "snapshot_command": "work show --project " + p.ID})
			return
		}
		// The timer also recovers lost wakeups and rechecks access during Redis outages.
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-wake:
			timer.Stop()
		}
	}
}

func workOperationPermission(a work.Actor, p *work.Project, req work.Request) error {
	if a.Human() {
		return nil
	}
	permission := ""
	switch req.Action {
	case "claim", "renew", "block", "fail", "submit":
		permission = "execute"
	case "upload":
		permission = "upload"
	case "message":
		permission = "message"
	case "plan", "assign", "accept", "retry", "cancel", "integrate", "deliver":
		permission = "manage"
	default:
		return &work.Failure{Status: 403, Message: "operator approval required"}
	}
	if !a.Can(permission) {
		return &work.Failure{Status: 403, Message: permission + " permission required"}
	}
	if permission == "manage" {
		if p != nil && (req.Action == "integrate" || req.Action == "deliver") {
			ids := req.Contributions
			if req.Action == "deliver" {
				for _, integration := range p.Integrations {
					if integration.ID == req.IntegrationID {
						ids = integration.Contributions
					}
				}
			}
			for _, id := range ids {
				for _, assignment := range p.Assignments {
					for _, cp := range assignment.Contributions {
						if cp.ID == id && !slices.Contains(a.AllowedMembers, cp.OperatorID) {
							return &work.Failure{Status: 403, Message: "integration contribution is outside coordinator limits"}
						}
					}
				}
			}
		}
		if p == nil || !p.Manages(a) {
			return &work.Failure{Status: 403, Message: "management role required"}
		}
		if s := p.Assignment(req.AssignmentID); s != nil && !slices.Contains(a.AllowedMembers, s.Assignee) {
			return &work.Failure{Status: 403, Message: "assignment is outside coordinator limits"}
		}
		for _, spec := range req.Assignments {
			if !slices.Contains(a.AllowedMembers, spec.Assignee) {
				return &work.Failure{Status: 403, Message: "assignee is outside coordinator limits"}
			}
		}
	}
	return nil
}
func (c *Controller) workObligations(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, p *work.Project) error {
	rows, err := q.Query(ctx, `SELECT s.post_id FROM work_subjobs s JOIN posts p ON p.id=s.post_id LEFT JOIN post_escrows e ON e.post_id=p.id WHERE s.project_id=$1 AND COALESCE(e.state,'') NOT IN ('released','refunded') AND NOT(p.status='cancelled' AND COALESCE(e.state,'') IN ('','prepared','failed','expired')) ORDER BY s.post_id`, p.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	p.OutstandingChildren = []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return err
		}
		p.OutstandingChildren = append(p.OutstandingChildren, id)
	}
	return rows.Err()
}
