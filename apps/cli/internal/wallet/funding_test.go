package wallet

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"testing"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func TestAgreementCompatibility(t *testing.T) {
	deadline, err := time.Parse(time.RFC3339Nano, "2026-10-03T04:05:06.123456Z")
	if err != nil {
		t.Fatal(err)
	}

	workerID := int64(2)

	post := api.Post{
		ID: 11, UserID: 1, AcceptedBy: &workerID,
		PosterWallet: "poster", WorkerWallet: "worker", CostLamports: 1000000,
		EndTime: deadline, Level: "easy", Title: `Fix "parser"`,
		Description: "Read\nUTF-8: café <data>", AcceptanceCriteria: "All tests pass",
		InputFiles: []string{"source.txt", "input.csv"}, ExpectedOutputs: []string{"patch.diff", "tests.log"},
	}
	fundBy, deliverBy := deadline.Add(2*time.Hour), deadline.Add(48*time.Hour)

	post.FundingWindowSeconds, post.FundBy, post.DeliverBy, post.ReviewWindowSeconds = 7200, &fundBy, &deliverBy, 86400
	for version, expected := range map[int32]string{
		1: "59d9f2b50b4a026cc306a42a5ca506dd914302208c0e2e0f76578344a91436f5",
		2: "ad76188263bc8723d26988c6e5dac8ff643c1a750850fa1d91d8a93d78b7c56e",
		3: "4e08a522ed4285cbe7dce83d977e3fba3ee3a7a99c012ba88c8d6e0597a3237a",
	} {
		hash := agreementHash(post, version)
		if hex.EncodeToString(hash[:]) != expected {
			t.Fatalf("v%d does not match the API agreement vector", version)
		}
	}

	post.InputFiles, post.ExpectedOutputs = nil, nil
	nilHash := agreementHash(post, 2)

	post.InputFiles, post.ExpectedOutputs = []string{}, []string{}
	if agreementHash(post, 2) != nilHash {
		t.Fatal("empty file declarations must have one encoding")
	}
}

func fundingFixture(t *testing.T, version int32) (*Wallet, api.Post, api.Escrow) {
	t.Helper()

	poster, worker, reviewer, escrow := testWallet(1), testWallet(2), testWallet(3), testWallet(4)
	workerID := int64(2)

	post := api.Post{
		ID: 11, UserID: 1, AcceptedBy: &workerID, CostLamports: 1000000,
		PosterWallet: poster.Address(), WorkerWallet: worker.Address(),
		Title: "Fix parser", Description: "Read the source", AcceptanceCriteria: "Tests pass",
		InputFiles: []string{"source.txt"}, ExpectedOutputs: []string{"patch.diff", "tests.log"},
		Level: "easy", EndTime: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC),
	}
	if version == 3 {
		fundBy, deliverBy := post.EndTime.Add(2*time.Hour), post.EndTime.Add(48*time.Hour)
		post.FundingWindowSeconds, post.FundBy, post.DeliverBy, post.ReviewWindowSeconds = 7200, &fundBy, &deliverBy, 86400
	}

	program, err := decodeTestKey(ProgramID)
	if err != nil {
		t.Fatal(err)
	}

	message := []byte{1, 0, 2, 4}
	for _, key := range [][]byte{poster.key[32:], escrow.key[32:], make([]byte, 32), program} {
		message = append(message, key...)
	}

	message = append(message, bytes.Repeat([]byte{5}, 32)...)
	message = append(message, 1, 3, 3, 0, 1, 2, 120)
	discriminator := sha256.Sum256([]byte("global:fund"))
	message = append(message, discriminator[:8]...)
	message = binary.LittleEndian.AppendUint64(message, uint64(post.ID))
	message = binary.LittleEndian.AppendUint64(message, uint64(post.CostLamports))
	message = append(message, worker.key[32:]...)
	message = append(message, reviewer.key[32:]...)
	hash := agreementHash(post, version)
	message = append(message, hash[:]...)
	raw := append([]byte{1}, make([]byte, 64)...)
	raw = append(raw, message...)
	plan := api.Escrow{
		State: "prepared", Network: "localnet", ProgramID: ProgramID,
		Address: escrow.Address(), Reviewer: reviewer.Address(), AgreementVersion: version,
		Transaction: base64.StdEncoding.EncodeToString(raw),
	}
	t.Setenv("SOLANA_REVIEWER", reviewer.Address())

	return poster, post, plan
}

func TestSignFundingVersions(t *testing.T) {
	for _, version := range []int32{1, 2, 3} {
		key, post, plan := fundingFixture(t, version)

		signed, err := key.SignFunding(post, plan)
		if err != nil {
			t.Fatal(err)
		}

		raw, err := base64.StdEncoding.DecodeString(signed)
		if err != nil || !ed25519.Verify(ed25519.PublicKey(key.key[32:]), raw[65:], raw[1:65]) {
			t.Fatal("funding must contain a valid poster signature")
		}
	}
}

func TestSignFundingBindsFullBrief(t *testing.T) {
	for name, mutate := range map[string]func(*api.Post, *api.Escrow){
		"instructions":      func(p *api.Post, _ *api.Escrow) { p.Description += " changed" },
		"criteria":          func(p *api.Post, _ *api.Escrow) { p.AcceptanceCriteria += " changed" },
		"inputs":            func(p *api.Post, _ *api.Escrow) { p.InputFiles = []string{"other.txt"} },
		"outputs":           func(p *api.Post, _ *api.Escrow) { p.ExpectedOutputs = []string{"tests.log", "patch.diff"} },
		"amount":            func(p *api.Post, _ *api.Escrow) { p.CostLamports++ },
		"deadline":          func(p *api.Post, _ *api.Escrow) { p.EndTime = p.EndTime.Add(time.Second) },
		"version downgrade": func(_ *api.Post, e *api.Escrow) { e.AgreementVersion = 1 },
		"unknown version":   func(_ *api.Post, e *api.Escrow) { e.AgreementVersion = 4 },
		"missing version":   func(_ *api.Post, e *api.Escrow) { e.AgreementVersion = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			key, post, plan := fundingFixture(t, 2)
			mutate(&post, &plan)

			if _, err := key.SignFunding(post, plan); err == nil {
				t.Fatal("changed funding terms must not be signed")
			}
		})
	}
}

func TestSignFundingBindsDeadlineTerms(t *testing.T) {
	for name, mutate := range map[string]func(*api.Post, *api.Escrow){
		"funding window": func(p *api.Post, _ *api.Escrow) { p.FundingWindowSeconds++ },
		"fund by":        func(p *api.Post, _ *api.Escrow) { date := p.FundBy.Add(time.Second); p.FundBy = &date },
		"deliver by":     func(p *api.Post, _ *api.Escrow) { date := p.DeliverBy.Add(time.Second); p.DeliverBy = &date },
		"review window":  func(p *api.Post, _ *api.Escrow) { p.ReviewWindowSeconds++ },
		"missing timing": func(p *api.Post, _ *api.Escrow) { p.DeliverBy = nil },
		"downgrade":      func(_ *api.Post, e *api.Escrow) { e.AgreementVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			key, post, plan := fundingFixture(t, 3)
			mutate(&post, &plan)

			if _, err := key.SignFunding(post, plan); err == nil {
				t.Fatal("changed deadline terms must not be signed")
			}
		})
	}
}
