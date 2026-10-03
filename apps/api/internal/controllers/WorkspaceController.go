package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"google.golang.org/grpc/status"
)

const maxWorkspaceFile = 10 << 20

type workspaceFailure struct {
	code    int
	message string
}

func (e *workspaceFailure) Error() string { return e.message }

func workspaceParticipant(post db.Post, userID int64) bool {
	return post.AcceptedBy.Valid && (post.UserID == userID || post.AcceptedBy.Int64 == userID)
}

func workspacePostID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		postJSON(w, 400, map[string]string{"error": "invalid post ID"})
		return 0, false
	}

	return id, true
}

func workspaceError(w http.ResponseWriter, err error) {
	var failure *workspaceFailure
	if errors.As(err, &failure) {
		postJSON(w, failure.code, map[string]string{"error": failure.message})
		return
	}

	if _, ok := status.FromError(err); ok {
		escrowError(w, err)
		return
	}

	log.Printf("workspace: %v", err)
	postJSON(w, 500, map[string]string{"error": "workspace unavailable"})
}

func workspaceAccess(
	w http.ResponseWriter,
	r *http.Request,
	q *db.Queries,
	post db.Post,
	err error,
	userID int64,
) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "post not found"})
		return false
	}

	if err != nil {
		workspaceError(w, err)
		return false
	}

	if !workspaceParticipant(post, userID) {
		reviewer, err := q.IsPostReviewer(
			r.Context(),
			db.IsPostReviewerParams{PostID: post.ID, UserID: userID},
		)
		if err != nil {
			workspaceError(w, err)
			return false
		}

		if !reviewer || !post.AcceptedBy.Valid {
			postJSON(
				w,
				403,
				map[string]string{
					"error": "workspace is private to its participants and authorized reviewer",
				},
			)

			return false
		}
	}

	return true
}

func (c *Controller) GetWorkspace(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	// Capture the stream before opening the database snapshot. Anything newer
	// is replayed by the WebSocket, including events published during this read.
	liveEvents, cursor, err := c.recentStreamEvents(r.Context(), id)
	if err != nil {
		workspaceError(w, err)
		return
	}

	tx, err := c.pool.BeginTx(
		r.Context(),
		pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly},
	)
	if err != nil {
		workspaceError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	q := db.New(tx)

	post, err := q.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, q, post, err, userID) {
		return
	}

	workspace, err := q.GetWorkspace(r.Context(), id)
	if err != nil {
		workspaceError(w, err)
		return
	}

	files, err := q.ListWorkspaceFiles(r.Context(), id)
	if err != nil {
		workspaceError(w, err)
		return
	}

	events, err := q.RecentWorkspaceEvents(r.Context(), id)
	if err != nil {
		workspaceError(w, err)
		return
	}

	funding := fundingView{State: "unfunded"}

	escrow, err := q.GetPostEscrow(r.Context(), id)
	if err == nil {
		funding = escrowView(escrow, false)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		workspaceError(w, err)
		return
	}

	canReview, err := q.IsPostReviewer(r.Context(), db.IsPostReviewerParams{PostID: id, UserID: userID})
	if err != nil {
		workspaceError(w, err)
		return
	}

	settlement := settlementView{}

	saved, err := q.GetPostSettlement(r.Context(), id)
	if err == nil {
		settlement = viewSettlement(saved, false)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		workspaceError(w, err)
		return
	}

	if err = tx.Commit(r.Context()); err != nil {
		workspaceError(w, err)
		return
	}

	if files == nil {
		files = []db.ListWorkspaceFilesRow{}
	}

	events = mergeWorkspaceEvents(events, liveEvents)

	c.writePost(
		w, r, 200, post,
		map[string]any{
			"workspace":  workspace,
			"files":      files,
			"events":     events,
			"cursor":     cursor,
			"escrow":     funding,
			"can_review": canReview,
			"settlement": settlement,
		},
	)
}

func (c *Controller) workspaceMutation(
	w http.ResponseWriter,
	r *http.Request,
	action func(*db.Queries, db.Post, int64) (any, error),
) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	tx, err := c.pool.Begin(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	defer tx.Rollback(r.Context())

	q := db.New(tx)

	post, err := q.LockPost(r.Context(), id)
	if !workspaceAccess(w, r, q, post, err, userID) {
		return
	}

	if post.Status == db.PostStatusCompleted || post.Status == db.PostStatusCancelled {
		postJSON(w, 409, map[string]string{"error": "this workspace is closed"})
		return
	}

	result, err := action(q, post, userID)
	if err != nil {
		workspaceError(w, err)
		return
	}

	if result == nil {
		return
	}

	if err = tx.Commit(r.Context()); err != nil {
		workspaceError(w, err)
		return
	}

	postJSON(w, 201, result)
}

func appendEvent(
	r *http.Request,
	q *db.Queries,
	postID, actorID int64,
	kind string,
	data any,
) (int64, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}

	return q.AppendWorkspaceEvent(
		r.Context(),
		db.AppendWorkspaceEventParams{
			PostID:  postID,
			ActorID: pgtype.Int8{Int64: actorID, Valid: true},
			Kind:    kind,
			Data:    payload,
		},
	)
}

func (c *Controller) WorkspaceMessage(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Text      string `json:"text"`
		MessageID string `json:"message_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<10)).Decode(&payload) != nil {
		postJSON(w, 400, map[string]string{"error": "invalid message"})
		return
	}

	payload.Text = strings.TrimSpace(payload.Text)
	if payload.Text == "" || !utf8.ValidString(payload.Text) || len([]rune(payload.Text)) > 4000 {
		postJSON(w, 400, map[string]string{"error": "message must contain 1 to 4000 characters"})
		return
	}

	if payload.MessageID == "" {
		payload.MessageID = uuid.NewString()
	}

	if len(payload.MessageID) > 128 || strings.Trim(payload.MessageID,
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
		postJSON(
			w,
			400,
			map[string]string{
				"error": "message_id must contain 1 to 128 letters, digits, underscores or hyphens",
			},
		)

		return
	}

	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	post, err := c.queries.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, c.queries, post, err, userID) {
		return
	}

	data, err := json.Marshal(payload)
	if err != nil {
		workspaceError(w, err)
		return
	}

	event := db.WorkspaceEvent{
		PostID:    id,
		ActorID:   pgtype.Int8{Int64: userID, Valid: true},
		Kind:      "message",
		Data:      data,
		CreatedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	}

	identity := fmt.Sprintf("chat:%d:%s", userID, payload.MessageID)
	canPublish := post.Status != db.PostStatusCompleted && post.Status != db.PostStatusCancelled

	cursor, err := c.publishEvent(r.Context(), event, identity, canPublish)
	if err != nil {
		if errors.Is(err, errMessageIDConflict) || errors.Is(err, errWorkspaceClosed) {
			postJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}

		postJSON(w, 503, map[string]string{"error": "chat is temporarily unavailable"})

		return
	}

	postJSON(w, 201, map[string]string{"message_id": payload.MessageID, "stream_id": cursor})
}

func mergeWorkspaceEvents(saved, live []db.WorkspaceEvent) []db.WorkspaceEvent {
	events := make([]db.WorkspaceEvent, 0, len(saved)+len(live))
	seen := make(map[string]bool)
	seenIDs := make(map[int64]bool)

	for _, event := range saved {
		events = append(events, event)

		seenIDs[event.ID] = true
		if event.StreamID.Valid {
			seen[event.StreamID.String] = true
		}
	}

	for _, event := range live {
		if !seen[event.StreamID.String] && (event.ID == 0 || !seenIDs[event.ID]) {
			events = append(events, event)
		}
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].CreatedAt.Time.Before(events[j].CreatedAt.Time)
	})

	if len(events) > 100 {
		events = events[len(events)-100:]
	}

	return events
}

func (c *Controller) SubmitWorkspace(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Note    string   `json:"note"`
		Version *int64   `json:"submission_version"`
		Files   []string `json:"files"`
		Inputs  []string `json:"input_files"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&payload) != nil {
		postJSON(w, 400, map[string]string{"error": "invalid submission"})
		return
	}

	payload.Note = strings.TrimSpace(payload.Note)
	if payload.Note == "" || !validTaskText(payload.Note, 4000, 16000) {
		postJSON(w, 400, map[string]string{"error": "describe your delivery in 1 to 4000 characters"})
		return
	}

	c.workspaceMutation(w, r, func(q *db.Queries, post db.Post, userID int64) (any, error) {
		if post.AcceptedBy.Int64 != userID {
			postJSON(w, 403, map[string]string{"error": "only the accepted worker can submit work"})
			return nil, nil
		}

		escrow, err := q.GetPostEscrow(r.Context(), post.ID)
		if errors.Is(err, pgx.ErrNoRows) ||
			(err == nil && (escrow.State != "confirmed" || post.Status != db.PostStatusInProgress)) {
			postJSON(
				w,
				409,
				map[string]string{"error": "wait for confirmed escrow funding before submitting work"},
			)

			return nil, nil
		}

		if err != nil {
			return nil, err
		}

		previous, err := q.GetWorkspace(r.Context(), post.ID)
		if err != nil {
			return nil, err
		}

		if payload.Version != nil && *payload.Version != previous.SubmissionVersion {
			return nil, &workspaceFailure{409, "delivery changed; refresh before submitting"}
		}

		saved, err := q.GetPostSettlement(r.Context(), post.ID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}

		if err == nil {
			saved, err = c.syncSettlement(r.Context(), q, post, escrow, saved, false)
			if err != nil {
				return nil, err
			}

			if saved.State != "failed" && saved.State != "expired" {
				return nil, &workspaceFailure{409, "settlement is active; wait for confirmation or expiry"}
			}
		}

		files, err := q.ListWorkspaceFiles(r.Context(), post.ID)
		if err != nil {
			return nil, err
		}

		if payload.Inputs != nil {
			names := post.InputFiles
			if len(names) == 0 {
				seen := map[string]bool{}
				for _, file := range files {
					if file.UploadedBy == post.UserID &&
						(file.Purpose == "input" || file.Purpose == "shared") &&
						!seen[file.Name] {
						names = append(names, file.Name)
						seen[file.Name] = true
					}
				}

				sort.Strings(names)
			}

			if len(payload.Inputs) != len(names) {
				return nil, &workspaceFailure{409, "task inputs changed; refresh before submitting"}
			}

			for i, name := range names {
				latest := ""

				for _, file := range files {
					if file.UploadedBy == post.UserID && file.Name == name &&
						(file.Purpose == "input" || file.Purpose == "shared") {
						latest = file.ID.String()
					}
				}

				if latest == "" || latest != payload.Inputs[i] {
					return nil, &workspaceFailure{409, "task inputs changed; refresh before submitting"}
				}
			}
		}

		selected := map[string]bool{}

		if len(payload.Files) > 20 {
			return nil, &workspaceFailure{400, "submit at most 20 files"}
		}

		for _, id := range payload.Files {
			if selected[id] {
				return nil, &workspaceFailure{400, "duplicate delivery file"}
			}

			selected[id] = true
		}

		latest := map[string]db.ListWorkspaceFilesRow{}

		for _, file := range files {
			if file.UploadedBy != userID || (file.Purpose != "output" && file.Purpose != "shared") ||
				(previous.SubmittedAt.Valid && !file.CreatedAt.Time.After(previous.SubmittedAt.Time)) {
				continue
			}

			if len(payload.Files) > 0 && !selected[file.ID.String()] {
				continue
			}

			latest[file.Name] = file
		}

		delivery := []pgtype.UUID{}

		for _, name := range post.ExpectedOutputs {
			file, ok := latest[name]
			if !ok {
				return nil, &workspaceFailure{409, "share every expected output before submitting: " + name}
			}

			delivery = append(delivery, file.ID)
		}

		if len(payload.Files) > 0 {
			delivery = []pgtype.UUID{}

			for _, id := range payload.Files {
				found := false

				for _, file := range latest {
					if file.ID.String() == id {
						delivery = append(delivery, file.ID)
						found = true

						break
					}
				}

				if !found {
					return nil, &workspaceFailure{
						400,
						"delivery file is not a current output from this worker",
					}
				}
			}
		}

		workspace, err := q.SubmitWorkspace(
			r.Context(),
			db.SubmitWorkspaceParams{PostID: post.ID, Submission: payload.Note, DeliveryFiles: delivery},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			postJSON(w, 409, map[string]string{"error": "work has already been submitted"})
			return nil, nil
		}

		if err != nil {
			return nil, err
		}

		id, err := appendEvent(
			r,
			q,
			post.ID,
			userID,
			"work.submitted",
			map[string]any{
				"note":               payload.Note,
				"submitted_at":       workspace.SubmittedAt,
				"submission_version": workspace.SubmissionVersion,
				"delivery_files":     workspace.DeliveryFiles,
			},
		)

		return map[string]any{"event_id": id}, err
	})
}

func safeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))

	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}

		return r
	}, name))
	if name == "" || name == "." || name == ".." || len(name) > 255 {
		return ""
	}

	return name
}

func (c *Controller) UploadWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	post, err := c.queries.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, c.queries, post, err, userID) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxWorkspaceFile+(64<<10))

	reader, err := r.MultipartReader()
	if err != nil {
		postJSON(w, 400, map[string]string{"error": "use multipart file upload"})
		return
	}

	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || safeFilename(part.FileName()) == "" {
		postJSON(w, 400, map[string]string{"error": "a named file is required"})
		return
	}

	name := safeFilename(part.FileName())

	content, err := io.ReadAll(io.LimitReader(part, maxWorkspaceFile+1))
	if err != nil || len(content) > maxWorkspaceFile {
		postJSON(w, 413, map[string]string{"error": "files must be at most 10 MiB"})
		return
	}

	if len(content) == 0 {
		postJSON(w, 400, map[string]string{"error": "empty files cannot be shared"})
		return
	}

	if _, err := reader.NextPart(); !errors.Is(err, io.EOF) {
		postJSON(w, 400, map[string]string{"error": "upload one file at a time"})
		return
	}

	c.workspaceMutation(w, r, func(q *db.Queries, post db.Post, userID int64) (any, error) {
		file, err := saveWorkspaceFile(r.Context(), q, post, userID, name, "shared", content)
		if err != nil {
			return nil, err
		}

		workspace, err := q.GetWorkspace(r.Context(), post.ID)

		return map[string]any{"file_id": file.ID, "event_id": workspace.LastEventID}, err
	})
}

func saveWorkspaceFile(
	ctx context.Context,
	q *db.Queries,
	post db.Post,
	userID int64,
	name, purpose string,
	content []byte,
) (db.WorkspaceFile, error) {
	var empty db.WorkspaceFile
	if !validFileName(name) || len(content) == 0 || len(content) > maxWorkspaceFile {
		return empty, &workspaceFailure{400, "invalid filename or file size"}
	}

	if post.Status == db.PostStatusCompleted || post.Status == db.PostStatusCancelled {
		return empty, &workspaceFailure{409, "this workspace is closed"}
	}

	switch purpose {
	case "input":
		if userID != post.UserID {
			return empty, &workspaceFailure{403, "only the poster can supply task inputs"}
		}
	case "output":
		if !post.AcceptedBy.Valid || userID != post.AcceptedBy.Int64 {
			return empty, &workspaceFailure{403, "only the worker can share task outputs"}
		}

		escrow, err := q.GetPostEscrow(ctx, post.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, &workspaceFailure{409, "wait for confirmed funding before sharing outputs"}
		}

		if err != nil {
			return empty, err
		}

		if escrow.State != "confirmed" || post.Status != db.PostStatusInProgress {
			return empty, &workspaceFailure{409, "wait for confirmed funding before sharing outputs"}
		}
	case "shared":
	default:
		return empty, &workspaceFailure{400, "choose input, output, or shared"}
	}

	size, err := q.WorkspaceFileBytes(ctx, post.ID)
	if err != nil {
		return empty, err
	}

	files, err := q.ListWorkspaceFiles(ctx, post.ID)
	if err != nil {
		return empty, err
	}

	if size+int64(len(content)) > 100<<20 || len(files) >= 100 {
		return empty, &workspaceFailure{409, "workspace limit reached (100 MiB or 100 files)"}
	}

	sum := sha256.Sum256(content)
	id := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	hash := hex.EncodeToString(sum[:])

	err = q.SaveWorkspaceFile(
		ctx,
		db.SaveWorkspaceFileParams{
			ID:         id,
			PostID:     post.ID,
			UploadedBy: userID,
			Name:       name,
			Size:       int64(len(content)),
			Sha256:     hash,
			Content:    content,
			Purpose:    purpose,
		},
	)
	if err != nil {
		return empty, err
	}

	payload, err := json.Marshal(
		map[string]any{"id": id, "name": name, "size": len(content), "sha256": hash, "purpose": purpose},
	)
	if err != nil {
		return empty, err
	}

	_, err = q.AppendWorkspaceEvent(
		ctx,
		db.AppendWorkspaceEventParams{
			PostID:  post.ID,
			ActorID: pgtype.Int8{Int64: userID, Valid: true},
			Kind:    "file.shared",
			Data:    payload,
		},
	)

	return db.WorkspaceFile{
		ID:         id,
		PostID:     post.ID,
		UploadedBy: userID,
		Name:       name,
		Size:       int64(len(content)),
		Sha256:     hash,
		Purpose:    purpose,
		CreatedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}, err
}

func (c *Controller) DownloadWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	id, ok := workspacePostID(w, r)
	if !ok {
		return
	}

	post, err := c.queries.GetPost(r.Context(), id)
	if !workspaceAccess(w, r, c.queries, post, err, userID) {
		return
	}

	fileID, err := uuid.Parse(r.PathValue("file"))
	if err != nil {
		postJSON(w, 400, map[string]string{"error": "invalid file ID"})
		return
	}

	file, err := c.queries.GetWorkspaceFile(
		r.Context(),
		db.GetWorkspaceFileParams{ID: pgtype.UUID{Bytes: fileID, Valid: true}, PostID: id},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "file not found"})
		return
	}

	if err != nil {
		workspaceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().
		Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(file.Size, 10))
	w.Write(file.Content)
}
