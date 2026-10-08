package controller

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
)

type chatFixture struct {
	db.DBTX
	owner    int64
	profile  string
	id       string
	snapshot json.RawMessage
	saved    bool
	deleted  bool
}

func (f *chatFixture) QueryRow(_ context.Context, query string, args ...any) pgx.Row {
	if strings.Contains(query, "-- name: GetUser") {
		if f.deleted {
			return offerRow{err: pgx.ErrNoRows}
		}

		return offerRow{value: db.User{ID: args[0].(int64)}}
	}

	if strings.Contains(query, "-- name: GetAgentChat") && args[0] == f.owner && args[1] == f.profile &&
		args[2] == f.id {
		return offerRow{value: f.snapshot}
	}

	return offerRow{err: pgx.ErrNoRows}
}

func (f *chatFixture) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	f.owner, f.profile, f.id = args[0].(int64), args[1].(string), args[2].(string)
	f.snapshot, f.saved = args[10].(json.RawMessage), true

	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func TestAgentChatsRequireOwnerAndValidateSnapshots(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	chat := agentChat{
		ID:        strings.Repeat("a", 32),
		Title:     "Go HTTP server",
		Directory: "/project",
		Provider:  "deepseek",
		Model:     "deepseek-flash",
		CreatedAt: now,
		UpdatedAt: now,
		Lines:     []string{"You: Write a server"},
	}
	data, _ := json.Marshal(chat)
	f := &chatFixture{}
	c := &Controller{queries: db.New(f)}

	request := func(owner, id, profile, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", "/agent/chats/"+id+"?profile="+profile, strings.NewReader(body))
		r.SetPathValue("chat", id)

		if owner != "" {
			r = r.WithContext(context.WithValue(r.Context(), utils.UserKey, &utils.UserJWT{ID: owner}))
		}

		w := httptest.NewRecorder()
		c.SaveAgentChat(w, r)

		return w
	}
	if w := request(
		"7",
		chat.ID,
		"worker",
		string(data),
	); w.Code != 200 || !f.saved || f.owner != 7 ||
		f.profile != "worker" {
		t.Fatalf("owner snapshot was not stored: %d %s", w.Code, w.Body.String())
	}

	for _, test := range []struct {
		owner, id, body string
		status          int
	}{
		{"", chat.ID, string(data), 401},
		{"8", strings.Repeat("b", 32), string(data), 400},
		{"7", chat.ID, strings.TrimSuffix(string(data), "}") + `,"user_id":8}`, 400},
		{"7", chat.ID, string(data) + ` {}`, 400},
		{"7", chat.ID, strings.Replace(string(data), `"messages":null`, `"messages":[{"role":"tool","content":"replay"}]`, 1), 400},
		{"7", chat.ID, `{"id":"` + chat.ID + `","lines":["` + strings.Repeat("x", 2<<20) + `"]}`, 400},
	} {
		f.saved = false
		if w := request(test.owner, test.id, "worker", test.body); w.Code != test.status || f.saved {
			t.Fatalf("invalid write accepted: %d %s", w.Code, w.Body.String())
		}
	}

	f.deleted = true

	if w := request("7", chat.ID, "worker", string(data)); w.Code != 401 {
		t.Fatal("deleted account could save a chat")
	}
}

func TestAgentChatReadCannotCrossOwnerOrProfile(t *testing.T) {
	id := strings.Repeat("a", 32)
	f := &chatFixture{
		owner:    7,
		profile:  "worker",
		id:       id,
		snapshot: json.RawMessage(`{"title":"Private chat"}`),
	}
	c := &Controller{queries: db.New(f)}

	for _, test := range []struct {
		owner, profile string
		status         int
	}{{"7", "worker", 200}, {"8", "worker", 404}, {"7", "requester", 404}} {
		r := httptest.NewRequest("GET", "/agent/chats/"+id+"?profile="+test.profile, nil)
		r.SetPathValue("chat", id)
		r = r.WithContext(context.WithValue(r.Context(), utils.UserKey, &utils.UserJWT{ID: test.owner}))
		w := httptest.NewRecorder()
		c.GetAgentChat(w, r)

		if w.Code != test.status ||
			(test.status != 200 && strings.Contains(w.Body.String(), "Private chat")) {
			t.Fatalf("chat isolation failed: %d %s", w.Code, w.Body.String())
		}
	}
}
