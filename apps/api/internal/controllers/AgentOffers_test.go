package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

type offerFixture struct {
	db.DBTX
	offer   db.AgentOffer
	missing bool
	busy    bool
	price   int64
	target  int64
}

type offerRow struct {
	value any
	err   error
}

func (r offerRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}

	v := reflect.ValueOf(r.value)
	if v.Kind() != reflect.Struct {
		reflect.ValueOf(dest[0]).Elem().Set(v)
		return nil
	}

	for i, field := range dest {
		reflect.ValueOf(field).Elem().Set(v.Field(i))
	}

	return nil
}

func (f offerFixture) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "-- name: LockAgentOffer"):
		if f.missing {
			return offerRow{err: pgx.ErrNoRows}
		}

		return offerRow{value: f.offer}
	case strings.Contains(query, "-- name: WorkerHasActiveTask"):
		return offerRow{value: f.busy}
	case strings.Contains(query, "-- name: GetPost"):
		return offerRow{
			value: db.Post{
				ID:           7,
				CostLamports: f.price,
				TargetWorker: pgtype.Int8{Int64: f.target, Valid: f.target > 0},
			},
		}
	}

	return offerRow{err: errors.New("unexpected query")}
}

func TestOfferClaimEnforcesPriceCapacityAndLease(t *testing.T) {
	lease := strings.Repeat("l", 32)

	valid := db.AgentOffer{UserID: 2, MinLamports: 100, Accepting: true, LeaseHash: tokenHash(lease),
		AvailableUntil: pgtype.Timestamptz{Time: time.Now().Add(time.Minute), Valid: true}}
	for _, test := range []struct {
		name    string
		fixture offerFixture
		lease   string
		allowed bool
	}{
		{"available", offerFixture{offer: valid, price: 100}, lease, true},
		{"below minimum", offerFixture{offer: valid, price: 99}, lease, false},
		{"directed to this seller", offerFixture{offer: valid, price: 100, target: 2}, lease, true},
		{"directed to another seller", offerFixture{offer: valid, price: 100, target: 3}, lease, false},
		{"busy", offerFixture{offer: valid, price: 100, busy: true}, lease, false},
		{"wrong lease", offerFixture{offer: valid, price: 100}, strings.Repeat("x", 32), false},
		{"missing seller", offerFixture{missing: true}, lease, false},
		{"ordinary worker", offerFixture{missing: true}, "", true},
		{"manual claim respects capacity", offerFixture{offer: valid, price: 100, busy: true}, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := checkOfferClaim(t.Context(), db.New(test.fixture), 2, 7, test.lease)
			if (err == nil) != test.allowed {
				t.Fatalf("allowed=%v: %v", test.allowed, err)
			}
		})
	}

	valid.AvailableUntil.Time = time.Now().Add(-time.Second)
	if checkOfferClaim(t.Context(), db.New(offerFixture{offer: valid, price: 100}), 2, 7, lease) == nil {
		t.Fatal("expired availability allowed a claim")
	}
}

func TestOfferValidationAndPublicPrivacy(t *testing.T) {
	terms := offerTerms{"Go worker", "Fix Go bugs", []string{"Go", "tests"}, 100, 60}
	if !validOffer(terms) {
		t.Fatal("valid terms rejected")
	}

	for _, change := range []func(*offerTerms){
		func(t *offerTerms) { t.MinLamports = 0 }, func(t *offerTerms) { t.JobTimeoutSeconds = 59 },
		func(t *offerTerms) { t.Capabilities = []string{"go", " GO "} },
		func(t *offerTerms) { t.Description = "\x1b[2J" }, func(t *offerTerms) { t.Capabilities = nil },
	} {
		bad := terms
		change(&bad)

		if validOffer(bad) {
			t.Fatal("invalid seller terms accepted")
		}
	}

	view := viewOffer(db.AgentOffer{Name: "worker", LeaseHash: "private-lease-hash"})

	data, _ := json.Marshal(view)
	if strings.Contains(string(data), "lease") {
		t.Fatal("private lease appeared in public metadata")
	}

	for _, body := range []string{`{"name":"worker","token":"secret"}`, `{} {}`, strings.Repeat("x", 25<<10)} {
		w := httptest.NewRecorder()

		r := httptest.NewRequest("PUT", "/agent-offers/mine", strings.NewReader(body))
		if decodeOffer(w, r, new(offerTerms)) || w.Code != 400 {
			t.Fatal("unsafe payload accepted")
		}
	}
}
