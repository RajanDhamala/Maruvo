-- name: GetWallet :one
SELECT * FROM wallets WHERE user_id = $1;

-- name: SetWalletChallenge :exec
INSERT INTO wallet_challenges (user_id, address, message, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (user_id) DO UPDATE SET address = EXCLUDED.address,
    message = EXCLUDED.message, expires_at = EXCLUDED.expires_at;

-- name: GetWalletChallenge :one
SELECT * FROM wallet_challenges WHERE user_id = $1 AND expires_at > NOW();

-- name: ConsumeWalletChallenge :execrows
DELETE FROM wallet_challenges WHERE user_id = $1 AND message = $2 AND expires_at > NOW();

-- name: LinkWallet :one
INSERT INTO wallets (user_id, address) VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET address = EXCLUDED.address
WHERE wallets.address = EXCLUDED.address
RETURNING *;

-- name: AcceptPost :one
UPDATE posts SET accepted_by = sqlc.arg(worker_id), accepted_at = NOW(),
    poster_wallet = poster.address, worker_wallet = worker.address,
    status = 'negotiating', updated_at = NOW()
FROM wallets AS poster, wallets AS worker
WHERE posts.id = sqlc.arg(post_id) AND posts.user_id <> sqlc.arg(worker_id)
    AND posts.status = 'open' AND posts.accepted_by IS NULL AND posts.end_time > NOW()
    AND poster.user_id = posts.user_id AND worker.user_id = sqlc.arg(worker_id)
RETURNING posts.*;

-- name: GetPost :one
SELECT * FROM posts WHERE id = $1;

-- name: LockPost :one
SELECT * FROM posts WHERE id = $1 FOR UPDATE;

-- name: GetPostEscrow :one
SELECT * FROM post_escrows WHERE post_id = $1;

-- name: SavePostEscrow :one
INSERT INTO post_escrows (post_id, address, program_id, reviewer, network, transaction, last_valid_block_height, fee_lamports, storage_lamports, agreement_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (post_id) DO UPDATE SET transaction = EXCLUDED.transaction,
    last_valid_block_height = EXCLUDED.last_valid_block_height,
    fee_lamports = EXCLUDED.fee_lamports, storage_lamports = EXCLUDED.storage_lamports,
    agreement_version = EXCLUDED.agreement_version,
    signature = '', signed_transaction = '', state = 'prepared', updated_at = NOW()
WHERE post_escrows.state IN ('failed', 'expired')
    AND post_escrows.address = EXCLUDED.address AND post_escrows.program_id = EXCLUDED.program_id
    AND post_escrows.reviewer = EXCLUDED.reviewer AND post_escrows.network = EXCLUDED.network
RETURNING *;

-- name: MarkEscrowSubmitted :one
UPDATE post_escrows SET signature = $2, signed_transaction = $4, state = 'pending', updated_at = NOW()
WHERE post_id = $1 AND transaction = $3
    AND (state = 'prepared' OR (state = 'pending' AND signature = $2 AND signed_transaction = ''))
RETURNING *;

-- name: UpdateEscrowState :exec
UPDATE post_escrows SET state = $2, updated_at = NOW() WHERE post_id = $1;

-- name: MarkPostFunded :one
UPDATE posts SET status = 'in_progress', updated_at = NOW()
WHERE id = $1 AND accepted_by IS NOT NULL AND status = 'negotiating' RETURNING *;

-- name: MarkPostSettled :one
UPDATE posts SET status = $2, updated_at = NOW() WHERE id = $1 AND accepted_by IS NOT NULL RETURNING *;
