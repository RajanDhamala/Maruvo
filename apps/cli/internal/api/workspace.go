package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type WorkspaceEvent struct {
	StreamID  string          `json:"stream_id"`
	PostID    int64           `json:"post_id"`
	ID        int64           `json:"id"`
	ActorID   *int64          `json:"actor_id"`
	Kind      string          `json:"kind"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

type WorkspaceFile struct {
	AgentGrantID *string   `json:"agent_grant_id,omitempty"`
	Purpose      string    `json:"purpose"`
	ID           string    `json:"id"`
	PostID       int64     `json:"post_id"`
	UploadedBy   int64     `json:"uploaded_by"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	SHA256       string    `json:"sha256"`
	CreatedAt    time.Time `json:"created_at"`
}

type WorkspaceState struct {
	DeliveryFiles     []string   `json:"delivery_files"`
	PostID            int64      `json:"post_id"`
	LastEventID       int64      `json:"last_event_id"`
	SubmittedAt       *time.Time `json:"submitted_at"`
	ReviewBy          *time.Time `json:"review_by"`
	Submission        string     `json:"submission"`
	ReviewState       string     `json:"review_state"`
	SubmissionVersion int64      `json:"submission_version"`
	ReviewNote        string     `json:"review_note"`
}

type Workspace struct {
	Context    TaskContext      `json:"context"`
	Cursor     string           `json:"cursor"`
	Post       Post             `json:"post"`
	Escrow     Escrow           `json:"escrow"`
	State      WorkspaceState   `json:"workspace"`
	Files      []WorkspaceFile  `json:"files"`
	Events     []WorkspaceEvent `json:"events"`
	CanReview  bool             `json:"can_review"`
	Settlement Settlement       `json:"settlement"`
}

type MessageReceipt struct {
	Status    string `json:"status"`
	MessageID string `json:"message_id"`
	StreamID  string `json:"stream_id"`
}

type WorkspaceReceipt struct {
	Status            string   `json:"status"`
	EventID           int64    `json:"event_id"`
	PostID            int64    `json:"post_id"`
	SubmissionVersion int64    `json:"submission_version"`
	ReviewState       string   `json:"review_state"`
	DeliveryFiles     []string `json:"delivery_files,omitempty"`
}

func workspacePath(id int64) string { return fmt.Sprintf("/posts/%d", id) }

func (c *Client) Workspace(ctx context.Context, token string, id int64) (Workspace, error) {
	var result Workspace

	err := c.request(ctx, http.MethodGet, workspacePath(id)+"/workspace", token, nil, &result)

	return result, err
}

func (c *Client) SendMessage(
	ctx context.Context,
	token string,
	id int64,
	text string,
	messageID ...string,
) error {
	return c.sendMessage(ctx, token, id, text, nil, messageID...)
}

func (c *Client) SendMessageReceipt(
	ctx context.Context, token string, id int64, text, messageID string,
) (MessageReceipt, error) {
	receipt := MessageReceipt{Status: "ok"}

	err := c.sendMessage(ctx, token, id, text, &receipt, messageID)

	return receipt, err
}

func (c *Client) sendMessage(
	ctx context.Context, token string, id int64, text string, result any, messageID ...string,
) error {
	key := ""
	if len(messageID) > 0 {
		key = messageID[0]
	}

	if key == "" {
		key = rand.Text()
	}

	for attempt := 0; ; attempt++ {
		err := c.requestJSON(ctx, http.MethodPost, workspacePath(id)+"/messages", token,
			map[string]string{"text": text, "message_id": key}, result)

		var failure *Error
		if err == nil || attempt == 1 || ctx.Err() != nil ||
			(errors.As(err, &failure) && failure.StatusCode < 500) {
			return err
		}

		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) SubmitWork(ctx context.Context, token string, id int64, note string) error {
	return c.requestJSON(
		ctx,
		http.MethodPost,
		workspacePath(id)+"/submit",
		token,
		map[string]string{"note": note},
		nil,
	)
}

func (c *Client) SubmitWorkReceipt(
	ctx context.Context,
	token string,
	id int64,
	note string,
) (WorkspaceReceipt, error) {
	receipt := WorkspaceReceipt{Status: "ok"}

	err := c.requestJSON(ctx, http.MethodPost, workspacePath(id)+"/submit", token,
		map[string]string{"note": note}, &receipt)

	return receipt, err
}

func (c *Client) SubmitDeliveryReceipt(
	ctx context.Context, token string, id, version int64, note string, files, inputs []string,
) (WorkspaceReceipt, error) {
	receipt := WorkspaceReceipt{Status: "ok"}

	if inputs == nil {
		inputs = []string{}
	}

	err := c.requestJSON(ctx, http.MethodPost, workspacePath(id)+"/submit", token,
		map[string]any{"note": note, "submission_version": version, "files": files, "input_files": inputs},
		&receipt)

	return receipt, err
}

func (c *Client) SubmitDelivery(
	ctx context.Context,
	token string,
	id, version int64,
	note string,
	files, inputs []string,
) error {
	return c.requestJSON(
		ctx,
		http.MethodPost,
		workspacePath(id)+"/submit",
		token,
		map[string]any{"note": note, "submission_version": version, "files": files, "input_files": inputs},
		nil,
	)
}

func localPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}

	return path
}

type WorkspaceStream struct {
	conn      *websocket.Conn
	stopWatch func() bool
}

type StreamFrame struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func (c *Client) ConnectWorkspace(
	ctx context.Context,
	token string,
	id, after int64,
	cursor ...string,
) (*WorkspaceStream, error) {
	u, err := url.Parse(c.baseURL + "/ws")
	if err != nil {
		return nil, err
	}

	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return nil, errors.New("API URL must use http or https")
	}

	q := u.Query()
	q.Set("post_id", strconv.FormatInt(id, 10))
	q.Set("after", strconv.FormatInt(after, 10))

	if len(cursor) > 0 && cursor[0] != "" {
		q.Set("cursor", cursor[0])
	}

	u.RawQuery = q.Encode()
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, Proxy: http.ProxyFromEnvironment}

	conn, response, err := dialer.DialContext(
		ctx,
		u.String(),
		http.Header{"Authorization": []string{"Bearer " + token}},
	)
	if err != nil {
		if response != nil {
			response.Body.Close()

			return nil, &Error{
				StatusCode: response.StatusCode,
				Message:    "workspace connection failed: " + response.Status,
			}
		}

		return nil, err
	}

	conn.SetReadLimit(64 << 10)
	conn.SetReadDeadline(time.Now().Add(65 * time.Second))
	conn.SetPingHandler(func(data string) error {
		conn.SetReadDeadline(time.Now().Add(65 * time.Second))
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(10*time.Second))
	})

	if ctx.Err() != nil {
		conn.Close()
		return nil, ctx.Err()
	}

	return &WorkspaceStream{conn: conn, stopWatch: context.AfterFunc(ctx, func() { conn.Close() })}, nil
}

func (s *WorkspaceStream) Read() (StreamFrame, error) {
	var frame StreamFrame

	err := s.conn.ReadJSON(&frame)

	return frame, err
}

func (s *WorkspaceStream) Close() {
	if s != nil {
		s.stopWatch()
		s.conn.Close()
	}
}

func StreamCursorAfter(a, b string) bool {
	if a == "" {
		return false
	}

	if b == "" {
		b = "0-0"
	}

	aTime, aSequence, _ := strings.Cut(a, "-")
	bTime, bSequence, _ := strings.Cut(b, "-")
	at, _ := strconv.ParseUint(aTime, 10, 64)
	bt, _ := strconv.ParseUint(bTime, 10, 64)
	as, _ := strconv.ParseUint(aSequence, 10, 64)
	bs, _ := strconv.ParseUint(bSequence, 10, 64)

	return at > bt || (at == bt && as > bs)
}
