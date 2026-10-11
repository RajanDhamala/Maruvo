package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

func checkOfferClaim(ctx context.Context, q *db.Queries, userID, postID int64, lease string) error {
	offer, err := q.LockAgentOffer(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) && lease == "" {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return &workspaceFailure{409, "publish an agent offer first"}
	}

	if err != nil {
		return err
	}

	if lease != "" && (offer.LeaseHash != tokenHash(lease) || !offer.Accepting ||
		!offer.AvailableUntil.Valid || !offer.AvailableUntil.Time.After(time.Now())) {
		return &workspaceFailure{409, "serving lease is inactive; no task was accepted"}
	}

	busy, err := q.WorkerHasActiveTask(ctx, pgtype.Int8{Int64: userID, Valid: true})
	if err != nil {
		return err
	}

	if busy {
		return &workspaceFailure{409, "finish or resume your active task before accepting another"}
	}

	post, err := q.GetPost(ctx, postID)
	if errors.Is(err, pgx.ErrNoRows) {
		return &workspaceFailure{404, "post not found"}
	}

	if err != nil {
		return err
	}

	if post.TargetWorker.Valid && post.TargetWorker.Int64 != userID {
		return &workspaceFailure{403, "this task is reserved for another seller"}
	}

	if post.CostLamports < offer.MinLamports {
		return &workspaceFailure{409, "task payment is below your agent offer minimum"}
	}

	return nil
}

type offerTerms struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	Capabilities      []string `json:"capabilities"`
	MinLamports       int64    `json:"min_lamports"`
	JobTimeoutSeconds int64    `json:"job_timeout_seconds"`
}

type offerView struct {
	offerTerms
	UserID         int64      `json:"user_id"`
	Available      bool       `json:"available"`
	Online         bool       `json:"online"`
	AvailableUntil *time.Time `json:"available_until"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func viewOffer(o db.AgentOffer) offerView {
	v := offerView{offerTerms: offerTerms{o.Name, o.Description, o.Capabilities, o.MinLamports,
		o.JobTimeoutSeconds}, UserID: o.UserID, UpdatedAt: o.UpdatedAt.Time}
	if o.AvailableUntil.Valid {
		v.AvailableUntil = &o.AvailableUntil.Time
		v.Available = o.Accepting && o.AvailableUntil.Time.After(time.Now())
		v.Online = o.AvailableUntil.Time.After(time.Now())
	}

	return v
}

func decodeOffer(w http.ResponseWriter, r *http.Request, payload any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 24<<10))
	d.DisallowUnknownFields()

	if d.Decode(payload) != nil || d.Decode(new(any)) != io.EOF {
		postJSON(w, 400, map[string]string{"error": "invalid JSON payload"})
		return false
	}

	return true
}

func validOffer(t offerTerms) bool {
	validText := func(s string, limit int) bool {
		return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= limit &&
			!strings.ContainsFunc(
				s,
				func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' },
			)
	}
	if !validText(t.Name, 80) || !validText(t.Description, 4000) || t.MinLamports <= 0 ||
		t.JobTimeoutSeconds < 60 || t.JobTimeoutSeconds > 86400 ||
		len(t.Capabilities) == 0 || len(t.Capabilities) > 10 {
		return false
	}

	seen := map[string]bool{}

	for _, capability := range t.Capabilities {
		key := strings.ToLower(strings.TrimSpace(capability))
		if !validText(key, 40) || strings.ContainsAny(key, "\n\t") || seen[key] {
			return false
		}

		seen[key] = true
	}

	return true
}

func (c *Controller) SaveAgentOffer(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload offerTerms
	if !decodeOffer(w, r, &payload) {
		return
	}

	if !validOffer(payload) {
		postJSON(
			w,
			400,
			map[string]string{
				"error": "provide a name, description, 1-10 unique capabilities, positive minimum payment, and job timeout of 60-86400 seconds",
			},
		)

		return
	}

	offer, err := c.queries.SaveAgentOffer(r.Context(), db.SaveAgentOfferParams{
		UserID: userID, Name: payload.Name, Description: payload.Description,
		Capabilities: payload.Capabilities, MinLamports: payload.MinLamports,
		JobTimeoutSeconds: payload.JobTimeoutSeconds,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 409, map[string]string{"error": "stop the serving process before changing its offer"})
		return
	}

	if err != nil {
		workspaceError(w, err)
		return
	}

	postJSON(w, 200, viewOffer(offer))
}

func (c *Controller) OwnAgentOffer(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	offer, err := c.queries.GetAgentOffer(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "publish an agent offer first"})
		return
	}

	if err != nil {
		workspaceError(w, err)
		return
	}

	busy, err := c.queries.WorkerHasActiveTask(r.Context(), pgtype.Int8{Int64: userID, Valid: true})
	if err != nil {
		workspaceError(w, err)
		return
	}

	view := viewOffer(offer)
	view.Available = view.Available && !busy
	postJSON(w, 200, view)
}

func (c *Controller) ListAgentOffers(w http.ResponseWriter, r *http.Request) {
	if _, ok := postUserID(w, r); !ok {
		return
	}

	offers, err := c.queries.ListAgentOffers(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}

	if offers == nil {
		offers = []db.ListAgentOffersRow{}
	}

	postJSON(w, 200, offers)
}

func (c *Controller) LeaseAgentOffer(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		Lease     string `json:"lease"`
		Accepting bool   `json:"accepting"`
	}
	if !decodeOffer(w, r, &payload) {
		return
	}

	if len(payload.Lease) < 32 || len(payload.Lease) > 128 {
		postJSON(w, 400, map[string]string{"error": "use a random serving lease of 32-128 bytes"})
		return
	}

	if r.Method == http.MethodDelete {
		_, err := c.queries.ReleaseAgentOffer(r.Context(), db.ReleaseAgentOfferParams{
			UserID: userID, LeaseHash: tokenHash(payload.Lease),
		})
		if err != nil {
			workspaceError(w, err)
			return
		}

		postJSON(w, 200, map[string]string{"status": "offline"})

		return
	}

	offer, err := c.queries.LeaseAgentOffer(r.Context(), db.LeaseAgentOfferParams{
		UserID: userID, LeaseHash: tokenHash(payload.Lease), Accepting: payload.Accepting,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(
			w,
			409,
			map[string]string{"error": "offer missing or another process is serving this account"},
		)

		return
	}

	if err != nil {
		workspaceError(w, err)
		return
	}

	postJSON(w, 200, viewOffer(offer))
}
