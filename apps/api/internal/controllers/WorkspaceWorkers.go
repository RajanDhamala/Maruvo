package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/redis/go-redis/v9"
)

func (c *Controller) RunWorkspaceWorkers(ctx context.Context) {
	var workers sync.WaitGroup
	workers.Go(func() { c.runWorkspacePublisher(ctx) })
	workers.Go(func() { c.runWorkspaceArchive(ctx) })
	workers.Wait()
}

func (c *Controller) runWorkspacePublisher(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for ctx.Err() == nil {
		err := c.listenWorkspaceEvents(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("workspace publisher: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Controller) listenWorkspaceEvents(ctx context.Context) error {
	conn, err := pgx.ConnectConfig(ctx, c.pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "LISTEN workspace_events"); err != nil {
		return err
	}

	for ctx.Err() == nil {
		batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := c.publishDatabaseEvents(batchCtx)

		cancel()

		if err != nil {
			return err
		}

		// Notifications wake one publisher, independent of the socket count.
		// A slower sweep also recovers missed notifications after reconnects.
		waitCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		_, err = conn.WaitForNotification(waitCtx)

		stop()

		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
	}

	return ctx.Err()
}

func (c *Controller) publishDatabaseEvents(ctx context.Context) error {
	for {
		count, err := c.publishDatabaseBatch(ctx)
		if err != nil || count < 100 {
			return err
		}
	}
}

func (c *Controller) publishDatabaseBatch(ctx context.Context) (int, error) {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	locked, err := q.LockWorkspacePublisher(ctx)
	if err != nil || !locked {
		return 0, err
	}

	events, err := q.UnpublishedWorkspaceEvents(ctx)
	if err != nil {
		return 0, err
	}

	for _, event := range events {
		identity := fmt.Sprintf("db:%d", event.ID)

		cursor, err := c.publishEvent(ctx, event, identity, true)
		if err != nil {
			return 0, err
		}

		if err := q.MarkWorkspaceEventPublished(ctx, db.MarkWorkspaceEventPublishedParams{
			PostID: event.PostID, ID: event.ID, StreamID: pgtype.Text{String: cursor, Valid: true},
		}); err != nil {
			return 0, err
		}
	}

	return len(events), tx.Commit(ctx)
}

func (c *Controller) runWorkspaceArchive(ctx context.Context) {
	consumer := uuid.NewString()

	retry := time.NewTicker(time.Second)
	defer retry.Stop()

	for ctx.Err() == nil {
		err := c.archiveWorkspaceEvents(ctx, consumer)
		if err != nil && ctx.Err() == nil {
			log.Printf("workspace archive: %v", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-retry.C:
		}
	}
}

func (c *Controller) archiveWorkspaceEvents(ctx context.Context, consumer string) error {
	err := c.redis.XGroupCreateMkStream(ctx, workspaceArchiveStream, workspaceArchiveGroup, "0").Err()
	if err != nil && !redis.HasErrorPrefix(err, "BUSYGROUP") {
		return err
	}

	claimCursor := "0-0"
	for ctx.Err() == nil {
		messages, next, err := c.redis.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream: workspaceArchiveStream, Group: workspaceArchiveGroup, Consumer: consumer,
			MinIdle: 30 * time.Second, Start: claimCursor, Count: 100,
		}).Result()
		if err != nil {
			return err
		}

		claimCursor = next

		if len(messages) == 0 {
			streams, err := c.redis.XReadGroup(ctx, &redis.XReadGroupArgs{
				Group: workspaceArchiveGroup, Consumer: consumer,
				Streams: []string{workspaceArchiveStream, ">"}, Count: 100, Block: time.Second,
			}).Result()
			if errors.Is(err, redis.Nil) {
				continue
			}

			if err != nil {
				return err
			}

			for _, stream := range streams {
				messages = append(messages, stream.Messages...)
			}
		}

		batchCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = c.archiveMessageBatch(batchCtx, messages)

		cancel()

		if err != nil {
			return err
		}
	}

	return ctx.Err()
}

func (c *Controller) archiveMessageBatch(ctx context.Context, messages []redis.XMessage) error {
	args := make([]db.ArchiveWorkspaceMessagesParams, 0, len(messages))

	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		event, err := decodeStreamEvent(message)
		if err != nil {
			return err
		}

		cursor, ok := message.Values["stream_id"].(string)
		if !ok || !validStreamCursor(cursor) || event.Kind != "message" {
			return errors.New("invalid chat archive entry")
		}

		args = append(args, db.ArchiveWorkspaceMessagesParams{
			PostID: event.PostID, ActorID: event.ActorID, StreamID: cursor,
			Data: event.Data, CreatedAt: event.CreatedAt,
		})
		ids = append(ids, message.ID)
	}

	sort.SliceStable(args, func(i, j int) bool { return args[i].PostID < args[j].PostID })

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	batch := db.New(tx).ArchiveWorkspaceMessages(ctx, args)
	batch.Exec(func(_ int, batchErr error) {
		if err == nil {
			err = batchErr
		}
	})

	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	if err := c.redis.XAck(ctx, workspaceArchiveStream, workspaceArchiveGroup, ids...).Err(); err != nil {
		return err
	}

	return c.redis.XDel(ctx, workspaceArchiveStream, ids...).Err()
}
