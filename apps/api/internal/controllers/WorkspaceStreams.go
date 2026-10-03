package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/redis/go-redis/v9"
)

const workspaceArchiveStream = "maruvo:workspace:archive"
const workspaceArchiveGroup = "postgres-history"

var errMessageIDConflict = errors.New("message ID already used with different text")
var errWorkspaceClosed = errors.New("this workspace is closed")

// A retry of a database event returns its original stream ID. Chat is queued
// for archival in the same operation that makes it visible to live readers.
var publishWorkspaceEvent = redis.NewScript(`
local existing = redis.call('HGET', KEYS[2], ARGV[1])
if existing then
    if ARGV[4] ~= '' and redis.call('HGET', KEYS[2], ARGV[1] .. ':payload') ~= ARGV[4] then
        return redis.error_reply('MESSAGE_ID_CONFLICT')
    end
    return existing
end
if ARGV[5] == 'closed' then
    return redis.error_reply('WORKSPACE_CLOSED')
end
local id = redis.call('XADD', KEYS[1], '*', 'event', ARGV[2])
if ARGV[3] == 'message' then
    redis.call('XADD', KEYS[3], '*', 'event', ARGV[2], 'stream_id', id)
end
redis.call('HSET', KEYS[2], ARGV[1], id)
if ARGV[4] ~= '' then
    redis.call('HSET', KEYS[2], ARGV[1] .. ':payload', ARGV[4])
end
return id
`)

func workspaceStreamKey(postID int64) string {
	return "maruvo:workspace:" + strconv.FormatInt(postID, 10) + ":events"
}

func validStreamCursor(cursor string) bool {
	first, second, ok := strings.Cut(cursor, "-")
	if !ok || first == "" || second == "" {
		return false
	}

	for _, part := range []string{first, second} {
		if strings.Trim(part, "0123456789") != "" {
			return false
		}

		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}

	return true
}

func (c *Controller) publishEvent(
	ctx context.Context,
	event db.WorkspaceEvent,
	identity string,
	canPublish bool,
) (string, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return "", err
	}

	key := workspaceStreamKey(event.PostID)

	archive := ""
	fingerprint := ""

	if event.ID == 0 && event.Kind == "message" {
		archive = "message"
		fingerprint = fmt.Sprintf("%x", sha256.Sum256(event.Data))
	}

	state := "open"
	if !canPublish {
		state = "closed"
	}

	cursor, err := publishWorkspaceEvent.Run(ctx, c.redis,
		[]string{key, key + ":published", workspaceArchiveStream},
		identity, payload, archive, fingerprint, state).Text()
	if redis.HasErrorPrefix(err, "MESSAGE_ID_CONFLICT") {
		return "", errMessageIDConflict
	}

	if redis.HasErrorPrefix(err, "WORKSPACE_CLOSED") {
		return "", errWorkspaceClosed
	}

	return cursor, err
}

func decodeStreamEvent(message redis.XMessage) (db.WorkspaceEvent, error) {
	var event db.WorkspaceEvent

	payload, ok := message.Values["event"].(string)
	if !ok {
		return event, errors.New("workspace stream event is missing its payload")
	}

	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return event, fmt.Errorf("decode workspace stream event: %w", err)
	}

	event.StreamID = pgtype.Text{String: message.ID, Valid: true}

	return event, nil
}

func (c *Controller) recentStreamEvents(
	ctx context.Context,
	postID int64,
) ([]db.WorkspaceEvent, string, error) {
	messages, err := c.redis.XRevRangeN(ctx, workspaceStreamKey(postID), "+", "-", 100).Result()
	if err != nil {
		return nil, "", err
	}

	cursor := "0-0"
	if len(messages) > 0 {
		cursor = messages[0].ID
	}

	events := make([]db.WorkspaceEvent, 0, len(messages))
	for i := len(messages) - 1; i >= 0; i-- {
		event, err := decodeStreamEvent(messages[i])
		if err != nil {
			return nil, "", err
		}

		events = append(events, event)
	}

	return events, cursor, nil
}
