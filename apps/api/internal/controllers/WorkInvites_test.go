package controller

import (
	"context"
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/redis/go-redis/v9"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAccountWebSocketReceivesWorkInvitation(t *testing.T) {
	url := os.Getenv("PRESENCE_TEST_REDIS_URL")
	if url == "" {
		t.Skip("requires test Redis")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	c := &Controller{redis: client}
	user := time.Now().UnixNano()
	key := workInviteKey(user)
	defer client.Del(context.Background(), key)
	t.Setenv("JWT_TOKEN", "isolated-invitation-test")
	identity := &utils.UserJWT{ID: strconv.FormatInt(user, 10)}
	token, _, err := utils.CreateUserToken(identity)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.WorkInvites(w, r.WithContext(context.WithValue(r.Context(), utils.UserKey, identity)))
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), http.Header{"Authorization": []string{"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	payload := `{"post_id":5,"from":1,"nonce":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	if err := client.Publish(context.Background(), workChannel(user), payload).Err(); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var frame struct {
		Event string          `json:"event"`
		Data  json.RawMessage `json:"data"`
	}
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if frame.Event != "work.invite" || string(frame.Data) != payload {
		t.Fatal("invitation was not delivered to account socket", frame)
	}
	if err := client.Set(context.Background(), key, payload, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	c.clearWorkInvite(context.Background(), user, "wrong-session")
	if client.Exists(context.Background(), key).Val() != 1 {
		t.Fatal("stale decision removed current invitation")
	}
	c.clearWorkInvite(context.Background(), user, strings.Repeat("a", 32))
	if client.Exists(context.Background(), key).Val() != 0 {
		t.Fatal("decision did not clear matching invitation")
	}
}
