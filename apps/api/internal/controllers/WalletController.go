package controller

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
	"github.com/rajandhamala/Maruvo/internal/utils"
)

func (c *Controller) WalletChallenge(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		Address string `json:"address"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&payload) != nil {
		postJSON(w, 400, map[string]string{"error": "invalid wallet payload"})
		return
	}

	if _, err := utils.WalletKey(payload.Address); err != nil {
		postJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}

	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		postJSON(w, 500, map[string]string{"error": "failed to create wallet challenge"})
		return
	}

	expires := time.Now().Add(5 * time.Minute)
	message := fmt.Sprintf(
		"Maruvo wallet link\nUser: %d\nWallet: %s\nNonce: %s\nExpires: %s",
		userID,
		payload.Address,
		base64.RawURLEncoding.EncodeToString(nonce),
		expires.UTC().Format(time.RFC3339),
	)

	err := c.queries.SetWalletChallenge(
		r.Context(),
		db.SetWalletChallengeParams{
			UserID:    userID,
			Address:   payload.Address,
			Message:   message,
			ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
		},
	)
	if err != nil {
		postJSON(w, 500, map[string]string{"error": "failed to save wallet challenge"})
		return
	}

	postJSON(w, 200, map[string]string{"message": message})
}

func (c *Controller) LinkWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	var payload struct {
		Signature string `json:"signature"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&payload) != nil {
		postJSON(w, 400, map[string]string{"error": "invalid wallet signature"})
		return
	}

	challenge, err := c.queries.GetWalletChallenge(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 409, map[string]string{"error": "wallet challenge expired; reconnect your wallet"})
		return
	}

	if err != nil {
		postJSON(w, 500, map[string]string{"error": "failed to look up wallet challenge"})
		return
	}

	key, keyErr := utils.WalletKey(challenge.Address)

	signature, sigErr := base64.StdEncoding.DecodeString(payload.Signature)
	if keyErr != nil || sigErr != nil || !ed25519.Verify(key, []byte(challenge.Message), signature) {
		postJSON(w, 400, map[string]string{"error": "invalid wallet signature"})
		return
	}

	tx, err := c.pool.Begin(r.Context())
	if err != nil {
		postJSON(w, 500, map[string]string{"error": "failed to link wallet"})
		return
	}
	defer tx.Rollback(r.Context())

	q := db.New(tx)

	consumed, err := q.ConsumeWalletChallenge(
		r.Context(),
		db.ConsumeWalletChallengeParams{UserID: userID, Message: challenge.Message},
	)
	if err != nil {
		postJSON(w, 500, map[string]string{"error": "failed to consume wallet challenge"})
		return
	}

	if consumed != 1 {
		postJSON(w, 409, map[string]string{"error": "wallet challenge already used"})
		return
	}

	wallet, err := q.LinkWallet(r.Context(), db.LinkWalletParams{UserID: userID, Address: challenge.Address})
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(
			w,
			409,
			map[string]string{"error": "this account already has a different wallet; use its keypair file"},
		)

		return
	}

	if err != nil {
		var conflict *pgconn.PgError
		if errors.As(err, &conflict) && conflict.Code == "23505" {
			postJSON(
				w,
				409,
				map[string]string{
					"error": "this wallet is already linked to another account; choose a separate wallet with -wallet",
				},
			)

			return
		}

		postJSON(w, 500, map[string]string{"error": "failed to link wallet"})

		return
	}

	if tx.Commit(r.Context()) != nil {
		postJSON(w, 500, map[string]string{"error": "failed to save linked wallet"})
		return
	}

	postJSON(w, 200, wallet)
}

func (c *Controller) GetWallet(w http.ResponseWriter, r *http.Request) {
	userID, ok := postUserID(w, r)
	if !ok {
		return
	}

	wallet, err := c.queries.GetWallet(r.Context(), userID)
	if errors.Is(err, pgx.ErrNoRows) {
		postJSON(w, 200, map[string]string{"address": ""})
		return
	}

	if err != nil {
		postJSON(w, 500, map[string]string{"error": "failed to look up wallet"})
		return
	}

	postJSON(w, 200, wallet)
}
