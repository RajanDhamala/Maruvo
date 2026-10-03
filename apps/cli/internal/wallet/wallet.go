package wallet

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

const ProgramID = "FEV9eR2WC2pPo6RDWEx9iSwvBHfokVpBED246fdUg2hd"
const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

type Wallet struct{ key ed25519.PrivateKey }

func Load() (*Wallet, error) {
	path := os.Getenv("MARUVO_WALLET")
	if path == "" {
		return nil, errors.New(
			"Set MARUVO_WALLET to your test wallet keypair file, then connect your wallet.",
		)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read wallet: %w", err)
	}

	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("wallet file permissions must be 0600")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var key []byte
	if json.Unmarshal(data, &key) != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Solana keypair file")
	}

	derived := ed25519.NewKeyFromSeed(key[:32])
	if !bytes.Equal(derived, key) {
		return nil, errors.New("wallet public key does not match its private key")
	}

	return &Wallet{key: ed25519.PrivateKey(key)}, nil
}

func Base58(data []byte) string {
	number, radix, remainder := new(big.Int).SetBytes(data), big.NewInt(58), new(big.Int)
	result := ""

	for number.Sign() > 0 {
		number.QuoRem(number, radix, remainder)
		result = string(alphabet[remainder.Int64()]) + result
	}

	for _, b := range data {
		if b != 0 {
			break
		}

		result = "1" + result
	}

	return result
}

func (w *Wallet) Address() string { return Base58(w.key[32:]) }
func (w *Wallet) SignMessage(message string) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(w.key, []byte(message)))
}

func (w *Wallet) SignFunding(post api.Post, plan api.Escrow) (string, error) {
	if post.AcceptedBy == nil || post.CostLamports < 0 || plan.ProgramID != ProgramID ||
		(plan.Network != "devnet" && plan.Network != "localnet") ||
		plan.State != "prepared" ||
		(plan.AgreementVersion != 1 && plan.AgreementVersion != 2) ||
		w.Address() != post.PosterWallet {
		return "", errors.New("wallet, network, or escrow program does not match this funding request")
	}

	data, err := base64.StdEncoding.DecodeString(plan.Transaction)
	if err != nil || len(data) > 1232 || len(data) < 65+4+128+32+1+1+1+3+1+120 || data[0] != 1 ||
		!bytes.Equal(data[1:65], make([]byte, 64)) {
		return "", errors.New("invalid unsigned funding transaction")
	}

	message := data[65:]
	if !bytes.Equal(message[:4], []byte{1, 0, 2, 4}) {
		return "", errors.New("unexpected funding signers or accounts")
	}

	keys := []string{}
	for i := 0; i < 4; i++ {
		keys = append(keys, Base58(message[4+i*32:4+(i+1)*32]))
	}

	if keys[0] != w.Address() || keys[1] != plan.Address {
		return "", errors.New("funding wallet or escrow address changed")
	}

	rest := message[4+128+32:]
	if len(rest) != 127 || rest[0] != 1 || int(rest[1]) >= len(keys) || keys[rest[1]] != ProgramID ||
		!bytes.Equal(rest[2:5], []byte{3, 0, 1}) ||
		int(rest[5]) >= len(keys) ||
		keys[rest[5]] != "11111111111111111111111111111111" ||
		rest[6] != 120 {
		return "", errors.New("unexpected funding instruction")
	}

	instruction := rest[7:]
	discriminator := sha256.Sum256([]byte("global:fund"))

	hash := agreementHash(post, plan.AgreementVersion)
	if !bytes.Equal(instruction[:8], discriminator[:8]) ||
		binary.LittleEndian.Uint64(instruction[8:16]) != uint64(post.ID) ||
		binary.LittleEndian.Uint64(instruction[16:24]) != uint64(post.CostLamports) ||
		Base58(instruction[24:56]) != post.WorkerWallet ||
		Base58(instruction[56:88]) != plan.Reviewer ||
		!bytes.Equal(instruction[88:120], hash[:]) {
		return "", errors.New("funding transaction does not match the accepted post")
	}

	reviewer := os.Getenv("SOLANA_REVIEWER")
	if reviewer == "" || reviewer != plan.Reviewer {
		return "", errors.New("reviewer does not match your local Solana configuration")
	}

	copy(data[1:65], ed25519.Sign(w.key, message))

	return base64.StdEncoding.EncodeToString(data), nil
}

func agreementHash(post api.Post, version int32) [32]byte {
	terms := fmt.Sprintf(
		"maruvo-escrow-v1\n%d\n%d\n%d\n%s\n%s\n%d\n%s\n%s\n%s",
		post.ID,
		post.UserID,
		*post.AcceptedBy,
		post.PosterWallet,
		post.WorkerWallet,
		post.CostLamports,
		post.EndTime.UTC().Format(time.RFC3339Nano),
		post.Level,
		post.Title,
	)
	if version == 2 {
		data, _ := json.Marshal([]any{
			"maruvo-escrow-v2", post.ID, post.UserID, *post.AcceptedBy,
			post.PosterWallet, post.WorkerWallet, post.CostLamports,
			post.EndTime.UTC().Format(time.RFC3339Nano), post.Level, post.Title,
			post.Description, post.AcceptanceCriteria,
			append([]string{}, post.InputFiles...), append([]string{}, post.ExpectedOutputs...),
		})
		terms = string(data)
	}

	return sha256.Sum256([]byte(terms))
}
