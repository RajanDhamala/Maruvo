package wallet

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func (w *Wallet) SignSettlement(post api.Post, plan api.SettlementPlan) (string, error) {
	escrow, settlement := plan.Escrow, plan.Settlement
	if post.AcceptedBy == nil || escrow.ProgramID != ProgramID || escrow.State != "confirmed" ||
		(escrow.Network != "devnet" && escrow.Network != "localnet") || settlement.State != "prepared" ||
		(settlement.Action != "release" && settlement.Action != "refund") || w.Address() != escrow.Reviewer || os.Getenv("SOLANA_REVIEWER") != escrow.Reviewer {
		return "", errors.New("reviewer wallet, network, or program does not match this settlement")
	}

	data, err := base64.StdEncoding.DecodeString(settlement.Transaction)
	if err != nil || len(data) > 1232 || len(data) < 65+4+128+32+16 || data[0] != 1 ||
		!bytes.Equal(data[1:65], make([]byte, 64)) {
		return "", errors.New("invalid unsigned settlement transaction")
	}

	message := data[65:]

	count := int(message[3])
	if !bytes.Equal(message[:3], []byte{1, 0, 1}) || count < 4 || count > 5 ||
		len(message) != 4+count*32+32+16 {
		return "", errors.New("unexpected settlement signers or accounts")
	}

	keys := make([]string, count)
	for i := range keys {
		keys[i] = Base58(message[4+i*32 : 4+(i+1)*32])
	}

	if keys[0] != w.Address() {
		return "", errors.New("settlement fee payer changed")
	}

	rest := message[4+count*32+32:]
	if rest[0] != 1 || int(rest[1]) >= count || keys[rest[1]] != ProgramID || rest[2] != 4 || rest[7] != 8 {
		return "", errors.New("unexpected settlement instruction")
	}

	accounts := []string{escrow.Reviewer, escrow.Address, post.PosterWallet, post.WorkerWallet}
	for i, expected := range accounts {
		if int(rest[3+i]) >= count || keys[rest[3+i]] != expected {
			return "", errors.New("settlement participants or escrow changed")
		}
	}

	for _, key := range keys {
		if key != ProgramID && key != escrow.Reviewer && key != escrow.Address && key != post.PosterWallet &&
			key != post.WorkerWallet {
			return "", errors.New("unexpected settlement account")
		}
	}

	discriminator := sha256.Sum256([]byte("global:" + settlement.Action))
	if !bytes.Equal(rest[8:], discriminator[:8]) {
		return "", errors.New("settlement action changed")
	}

	copy(data[1:65], ed25519.Sign(w.key, message))

	return base64.StdEncoding.EncodeToString(data), nil
}
