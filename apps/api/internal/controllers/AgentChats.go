package controller

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

type agentChat struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Directory string    `json:"directory"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Lines    []string `json:"lines"`
	Usage    string   `json:"usage,omitempty"`
	Draft    string   `json:"draft,omitempty"`
	Pending  bool     `json:"pending"`
	Archived bool     `json:"archived,omitempty"`
}

func validChatID(id string) bool {
	_, err := hex.DecodeString(id)
	return len(id) == 32 && err == nil && strings.ToLower(id) == id
}

func validAgentChat(chat agentChat) bool {
	if !validChatID(chat.ID) || strings.TrimSpace(chat.Title) == "" ||
		utf8.RuneCountInString(chat.Title) > 80 ||
		!filepath.IsAbs(chat.Directory) ||
		len(chat.Directory) > 4096 ||
		len(chat.Provider) > 80 ||
		len(chat.Model) > 256 ||
		chat.CreatedAt.IsZero() ||
		chat.UpdatedAt.Before(chat.CreatedAt) ||
		chat.UpdatedAt.After(time.Now().Add(5*time.Minute)) {
		return false
	}

	for _, value := range []string{chat.Title, chat.Directory, chat.Provider, chat.Model} {
		if strings.ContainsFunc(value, unicode.IsControl) {
			return false
		}
	}

	for _, message := range chat.Messages {
		if message.Role != "user" && message.Role != "assistant" {
			return false
		}
	}

	return true
}

func (c *Controller) chatOwner(w http.ResponseWriter, r *http.Request) (int64, string, bool) {
	userID, ok := postUserID(w, r)
	if !ok {
		return 0, "", false
	}

	if requestAgent(r.Context()) != nil {
		postJSON(w, 403, map[string]string{"error": "personal agent chats require an account login"})
		return 0, "", false
	}

	profile := r.URL.Query().Get("profile")
	if len(profile) > 64 || strings.ContainsAny(profile, "/\\") ||
		strings.ContainsFunc(profile, unicode.IsControl) {
		postJSON(w, 400, map[string]string{"error": "invalid chat profile"})
		return 0, "", false
	}

	if _, err := c.queries.GetUser(r.Context(), userID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			postJSON(w, 401, map[string]string{"error": "account no longer exists"})
		} else {
			chatStorageError(w, err)
		}

		return 0, "", false
	}

	return userID, profile, true
}

func chatStorageError(w http.ResponseWriter, err error) {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "42P01" {
		postJSON(w, 503, map[string]string{"error": "chat storage needs migration 00003_agent_chats.sql"})
		return
	}

	workspaceError(w, err)
}

func (c *Controller) SaveAgentChat(w http.ResponseWriter, r *http.Request) {
	userID, profile, ok := c.chatOwner(w, r)
	if !ok {
		return
	}

	var chat agentChat

	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	d.DisallowUnknownFields()

	if d.Decode(&chat) != nil || d.Decode(new(any)) != io.EOF ||
		!validAgentChat(chat) || chat.ID != r.PathValue("chat") {
		postJSON(
			w,
			400,
			map[string]string{
				"error": "invalid chat snapshot; use user/assistant messages and a body of at most 2 MiB",
			},
		)

		return
	}

	snapshot, err := json.Marshal(chat)
	if err == nil {
		err = c.queries.SaveAgentChat(r.Context(), db.SaveAgentChatParams{
			UserID: userID, Profile: profile, ID: chat.ID, Title: chat.Title, Directory: chat.Directory,
			Provider: chat.Provider, Model: chat.Model, Archived: chat.Archived, Snapshot: snapshot,
			CreatedAt: pgtype.Timestamptz{Time: chat.CreatedAt, Valid: true},
			UpdatedAt: pgtype.Timestamptz{Time: chat.UpdatedAt, Valid: true},
			Revision:  chat.UpdatedAt.UnixNano(),
		})
	}

	if err != nil {
		chatStorageError(w, err)
		return
	}

	postJSON(w, 200, map[string]bool{"saved": true})
}

func (c *Controller) ListAgentChats(w http.ResponseWriter, r *http.Request) {
	userID, profile, ok := c.chatOwner(w, r)
	if !ok {
		return
	}

	chats, err := c.queries.ListAgentChats(
		r.Context(),
		db.ListAgentChatsParams{UserID: userID, Profile: profile},
	)
	if err != nil {
		chatStorageError(w, err)
		return
	}

	if chats == nil {
		chats = []db.ListAgentChatsRow{}
	}

	postJSON(w, 200, chats)
}

func (c *Controller) GetAgentChat(w http.ResponseWriter, r *http.Request) {
	userID, profile, ok := c.chatOwner(w, r)
	if !ok {
		return
	}

	id := r.PathValue("chat")
	if !validChatID(id) {
		postJSON(w, 400, map[string]string{"error": "invalid chat ID"})
		return
	}

	snapshot, err := c.queries.GetAgentChat(
		r.Context(),
		db.GetAgentChatParams{UserID: userID, Profile: profile, ID: id},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 404, map[string]string{"error": "chat not found"})
		return
	}

	if err != nil {
		chatStorageError(w, err)
		return
	}

	postJSON(w, 200, snapshot)
}
