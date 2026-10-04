package controller

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
)

type CreatePostPayload struct {
	Title                string        `json:"title"`
	CostLamports         int64         `json:"cost_lamports"`
	EndTime              time.Time     `json:"end_time"`
	Status               db.PostStatus `json:"status"`
	Level                db.PostLevel  `json:"level"`
	Description          string        `json:"description"`
	AcceptanceCriteria   string        `json:"acceptance_criteria"`
	InputFiles           []string      `json:"input_files"`
	ExpectedOutputs      []string      `json:"expected_outputs"`
	FundingWindowSeconds int64         `json:"funding_window_seconds"`
	DeliverBy            time.Time     `json:"deliver_by"`
	ReviewWindowSeconds  int64         `json:"review_window_seconds"`
}

type DeletePostPayload struct {
	ID int64 `json:"id"`
}

type UpdatePostStatusPayload struct {
	ID     int64         `json:"id"`
	Status db.PostStatus `json:"status"`
}

type PostFeedPayload struct {
	Level db.PostLevel `json:"level"`
}

const maxDescriptionBytes = 32 << 10
const maxDescriptionCharacters = 12000

func validTaskText(text string, characters, bytes int) bool {
	return utf8.ValidString(text) && utf8.RuneCountInString(text) <= characters && len(text) <= bytes &&
		strings.IndexFunc(text, func(r rune) bool {
			return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
		}) < 0
}

func (c *Controller) CreatePost(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload CreatePostPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&payload); err != nil {
		postJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid post payload"})
		return
	}

	payload.Title = strings.TrimSpace(payload.Title)
	payload.EndTime = payload.EndTime.UTC().Truncate(time.Microsecond)
	payload.DeliverBy = payload.DeliverBy.UTC().Truncate(time.Microsecond)

	payload.AcceptanceCriteria = strings.TrimSpace(payload.AcceptanceCriteria)
	if strings.TrimSpace(payload.Description) == "" ||
		!validTaskText(payload.Description, maxDescriptionCharacters, maxDescriptionBytes) {
		postJSON(w, 400, map[string]string{
			"error": "provide a text description of at most 32 KiB and 12,000 characters",
		})

		return
	}

	if payload.InputFiles == nil {
		payload.InputFiles = []string{}
	}

	if payload.ExpectedOutputs == nil {
		payload.ExpectedOutputs = []string{}
	}

	if !validTaskText(payload.AcceptanceCriteria, 4000, 16000) ||
		!validTaskFiles(payload.InputFiles) ||
		!validTaskFiles(payload.ExpectedOutputs) {
		postJSON(
			w,
			400,
			map[string]string{
				"error": "provide a valid task brief and up to 20 unique filenames per input/output list",
			},
		)

		return
	}

	if payload.Status == "" {
		payload.Status = db.PostStatusOpen
	}

	if payload.Title == "" || len([]rune(payload.Title)) > 500 || payload.CostLamports < 0 ||
		!payload.EndTime.After(time.Now()) ||
		!validPostLevel(payload.Level) ||
		payload.Status != db.PostStatusOpen {
		postJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid post fields"})
		return
	}

	if err := validateTaskTiming(payload.EndTime, payload.DeliverBy,
		payload.FundingWindowSeconds, payload.ReviewWindowSeconds); err != nil {
		postJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}

	post, err := c.queries.CreatePost(r.Context(), db.CreatePostParams{
		UserID:               userID,
		Title:                payload.Title,
		CostLamports:         payload.CostLamports,
		EndTime:              pgtype.Timestamptz{Time: payload.EndTime, Valid: true},
		Status:               payload.Status,
		Level:                payload.Level,
		Description:          payload.Description,
		AcceptanceCriteria:   payload.AcceptanceCriteria,
		InputFiles:           payload.InputFiles,
		ExpectedOutputs:      payload.ExpectedOutputs,
		FundingWindowSeconds: payload.FundingWindowSeconds,
		DeliverBy: pgtype.Timestamptz{
			Time:  payload.DeliverBy.UTC().Truncate(time.Microsecond),
			Valid: !payload.DeliverBy.IsZero(),
		},
		ReviewWindowSeconds: payload.ReviewWindowSeconds,
	})
	if err != nil {
		log.Printf("create post: %v", err)
		postJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create post"})

		return
	}

	c.writePost(w, r, http.StatusCreated, post, nil)
}

func validTaskFiles(names []string) bool {
	if len(names) > 20 {
		return false
	}

	seen := map[string]bool{}
	for _, name := range names {
		if !validFileName(name) || seen[name] {
			return false
		}

		seen[name] = true
	}

	return true
}

func validFileName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 180 && utf8.ValidString(name) &&
		strings.TrimSpace(name) == name &&
		!strings.ContainsAny(name, "/\\") &&
		strings.IndexFunc(name, unicode.IsControl) < 0
}

func (c *Controller) GetUrPosts(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	posts, err := c.queries.GetUrPosts(r.Context(), userID)
	if err != nil {
		postJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch your posts"})
		return
	}

	if posts == nil {
		posts = []db.Post{}
	}

	c.writePosts(w, r, posts)
}

func (c *Controller) DeletePost(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload DeletePostPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.ID <= 0 {
		postJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid post ID"})
		return
	}

	deleted, err := c.queries.DeleteYourPost(r.Context(), db.DeleteYourPostParams{
		UserID: userID,
		ID:     payload.ID,
	})
	if err != nil {
		postJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete post"})
		return
	}

	if deleted == 0 {
		postJSON(w, http.StatusNotFound, map[string]string{"error": "post not found"})
		return
	}

	postJSON(w, http.StatusOK, map[string]string{"message": "post deleted"})
}

func (c *Controller) UpdatePostStatus(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload UpdatePostStatusPayload
	if err := json.NewDecoder(r.Body).
		Decode(&payload); err != nil || payload.ID <= 0 ||
		!validPostStatus(payload.Status) {
		postJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid post ID or status"})
		return
	}

	post, err := c.queries.UpdatePostStatus(r.Context(), db.UpdatePostStatusParams{
		Status: payload.Status,
		ID:     payload.ID,
		UserID: userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, http.StatusNotFound, map[string]string{"error": "post not found"})
		return
	}

	if err != nil {
		postJSON(
			w,
			http.StatusInternalServerError,
			map[string]string{"error": "failed to update post status"},
		)

		return
	}

	c.writePost(w, r, http.StatusOK, post, nil)
}

func (c *Controller) DescLevelPost(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload PostFeedPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || !validPostLevel(payload.Level) {
		postJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid post level"})
		return
	}

	posts, err := c.queries.FetchPostByLevel(r.Context(), db.FetchPostByLevelParams{
		Level:  payload.Level,
		UserID: userID,
	})
	if err != nil {
		postJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to fetch post feed"})
		return
	}

	if posts == nil {
		posts = []db.Post{}
	}

	c.writePosts(w, r, posts)
}

func postUserID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	user, ok := r.Context().Value(utils.UserKey).(*utils.UserJWT)
	if !ok || user == nil {
		postJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return 0, false
	}

	userID, err := strconv.ParseInt(user.ID, 10, 64)
	if err != nil || userID <= 0 {
		postJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid user ID"})
		return 0, false
	}

	return userID, true
}

func postJSON(w http.ResponseWriter, status int, response any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(response)
}

func validPostLevel(level db.PostLevel) bool {
	return level == db.PostLevelEasy || level == db.PostLevelMedium || level == db.PostLevelComplex
}

func validPostStatus(status db.PostStatus) bool {
	switch status {
	case db.PostStatusOpen,
		db.PostStatusNegotiating,
		db.PostStatusInProgress,
		db.PostStatusCompleted,
		db.PostStatusCancelled:
		return true
	default:
		return false
	}
}
