package controller

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

func (c *Controller) RunEscrowMonitor(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	cursor := int64(0)
	for {
		ids, err := c.queries.TrackedEscrows(ctx, cursor)
		if err != nil && ctx.Err() == nil {
			log.Printf("escrow monitor: %v", err)
		}

		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}

			if err := c.refreshPostEscrow(ctx, id); err != nil && ctx.Err() == nil {
				log.Printf("escrow monitor post %d: %v", id, err)
			}

			cursor = id
		}

		if len(ids) < 20 {
			cursor = 0
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Controller) refreshPostEscrow(parent context.Context, id int64) error {
	if c.rpc == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	post, err := q.LockPost(ctx, id)
	if err != nil {
		return err
	}

	escrow, err := q.GetPostEscrow(ctx, id)
	if err != nil {
		return err
	}

	post, escrow, err = c.syncEscrow(ctx, q, post, escrow, true)
	if err != nil {
		return err
	}

	saved, err := q.GetPostSettlement(ctx, id)
	if err == nil {
		_, err = c.syncSettlement(ctx, q, post, escrow, saved, true)
	}

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	return tx.Commit(ctx)
}
