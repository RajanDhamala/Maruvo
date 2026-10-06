package wallet

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"testing"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func testWallet(seed byte) *Wallet {
	return &Wallet{key: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, 32))}
}

func settlementFixture(t *testing.T, action, reviewerRole string) (*Wallet, api.Post, api.SettlementPlan) {
	t.Helper()

	poster, worker, reviewer := testWallet(1), testWallet(2), testWallet(3)
	if reviewerRole == "poster" {
		reviewer = poster
	} else if reviewerRole == "worker" {
		reviewer = worker
	}

	escrowKey := testWallet(4).key[32:]
	escrowAddress := Base58(escrowKey)

	program, err := decodeTestKey(ProgramID)
	if err != nil {
		t.Fatal(err)
	}

	keys := [][]byte{reviewer.key[32:], escrowKey}
	for _, key := range [][]byte{poster.key[32:], worker.key[32:]} {
		exists := false

		for _, saved := range keys {
			if bytes.Equal(key, saved) {
				exists = true
			}
		}

		if !exists {
			keys = append(keys, key)
		}
	}

	keys = append(keys, program)
	index := func(address string) byte {
		for i, key := range keys {
			if Base58(key) == address {
				return byte(i)
			}
		}

		t.Fatal("missing key")

		return 0
	}

	message := []byte{1, 0, 1, byte(len(keys))}
	for _, key := range keys {
		message = append(message, key...)
	}

	message = append(message, bytes.Repeat([]byte{5}, 32)...)
	message = append(
		message,
		1,
		index(ProgramID),
		4,
		0,
		1,
		index(poster.Address()),
		index(worker.Address()),
		8,
	)
	discriminator := sha256.Sum256([]byte("global:" + action))
	message = append(message, discriminator[:8]...)
	raw := append([]byte{1}, make([]byte, 64)...)
	raw = append(raw, message...)
	workerID := int64(2)
	post := api.Post{
		ID:           11,
		AcceptedBy:   &workerID,
		PosterWallet: poster.Address(),
		WorkerWallet: worker.Address(),
		CostLamports: 1000000,
	}
	plan := api.SettlementPlan{
		Escrow: api.Escrow{
			State:     "confirmed",
			Address:   escrowAddress,
			ProgramID: ProgramID,
			Reviewer:  reviewer.Address(),
			Network:   "localnet",
		},
		Settlement: api.Settlement{
			State:             "prepared",
			Action:            action,
			SubmissionVersion: 1,
			Transaction:       base64.StdEncoding.EncodeToString(raw),
		},
	}
	t.Setenv("SOLANA_REVIEWER", reviewer.Address())
	t.Setenv("SOLANA_RPC_URL", "http://127.0.0.1:8899")
	t.Setenv("SOLANA_PROGRAM_ID", ProgramID)

	return reviewer, post, plan
}

func TestSignSettlement(t *testing.T) {
	for _, action := range []string{"release", "refund"} {
		for _, role := range []string{"independent", "poster", "worker"} {
			t.Run(action+"/"+role, func(t *testing.T) {
				reviewer, post, plan := settlementFixture(t, action, role)

				signed, err := reviewer.SignSettlement(post, plan)
				if err != nil {
					t.Fatal(err)
				}

				raw, err := base64.StdEncoding.DecodeString(signed)
				if err != nil {
					t.Fatal(err)
				}

				expected, _ := base64.StdEncoding.DecodeString(plan.Settlement.Transaction)
				if !bytes.Equal(raw[65:], expected[65:]) ||
					!ed25519.Verify(ed25519.PublicKey(reviewer.key[32:]), raw[65:], raw[1:65]) {
					t.Fatal("signature or message changed")
				}
			})
		}
	}
}

func TestSignSettlementRejectsChangedAuthorityOrPayment(t *testing.T) {
	cases := []struct {
		name   string
		change func(*api.Post, *api.SettlementPlan)
	}{
		{
			"wrong program",
			func(_ *api.Post, p *api.SettlementPlan) { p.Escrow.ProgramID = testWallet(6).Address() },
		},
		{"mainnet", func(_ *api.Post, p *api.SettlementPlan) { p.Escrow.Network = "mainnet" }},
		{"unfunded", func(_ *api.Post, p *api.SettlementPlan) { p.Escrow.State = "pending" }},
		{"already pending", func(_ *api.Post, p *api.SettlementPlan) { p.Settlement.State = "pending" }},
		{
			"changed recipient",
			func(post *api.Post, _ *api.SettlementPlan) { post.WorkerWallet = testWallet(6).Address() },
		},
		{
			"changed escrow",
			func(_ *api.Post, p *api.SettlementPlan) { p.Escrow.Address = testWallet(6).Address() },
		},
		{
			"changed reviewer",
			func(_ *api.Post, p *api.SettlementPlan) { p.Escrow.Reviewer = testWallet(6).Address() },
		},
		{"changed action", func(_ *api.Post, p *api.SettlementPlan) { p.Settlement.Action = "refund" }},
		{"extra instruction", func(_ *api.Post, p *api.SettlementPlan) {
			raw, _ := base64.StdEncoding.DecodeString(p.Settlement.Transaction)
			raw = append(raw, 0)
			p.Settlement.Transaction = base64.StdEncoding.EncodeToString(raw)
		}},
		{"modified instruction", func(_ *api.Post, p *api.SettlementPlan) {
			raw, _ := base64.StdEncoding.DecodeString(p.Settlement.Transaction)
			raw[len(raw)-1] ^= 1
			p.Settlement.Transaction = base64.StdEncoding.EncodeToString(raw)
		}},
		{"preexisting signature", func(_ *api.Post, p *api.SettlementPlan) {
			raw, _ := base64.StdEncoding.DecodeString(p.Settlement.Transaction)
			raw[1] = 1
			p.Settlement.Transaction = base64.StdEncoding.EncodeToString(raw)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reviewer, post, plan := settlementFixture(t, "release", "independent")
			tc.change(&post, &plan)

			if _, err := reviewer.SignSettlement(post, plan); err == nil {
				t.Fatal("unsafe settlement was signed")
			}
		})
	}

	reviewer, post, plan := settlementFixture(t, "release", "independent")
	if _, err := testWallet(2).SignSettlement(post, plan); err == nil {
		t.Fatal("worker wallet signed reviewer decision")
	}

	t.Setenv("SOLANA_REVIEWER", testWallet(6).Address())

	if _, err := reviewer.SignSettlement(post, plan); err == nil {
		t.Fatal("local reviewer configuration mismatch accepted")
	}
}

func decodeTestKey(address string) ([]byte, error) {
	number := new(big.Int)

	for _, c := range address {
		digit := strings.IndexRune(alphabet, c)
		if digit < 0 {
			return nil, errors.New("invalid address")
		}

		number.Mul(number, big.NewInt(58)).Add(number, big.NewInt(int64(digit)))
	}

	data := append(make([]byte, len(address)-len(strings.TrimLeft(address, "1"))), number.Bytes()...)
	if len(data) != 32 {
		return nil, errors.New("invalid key")
	}

	return data, nil
}
