package utils

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCLILoginsRedis(t *testing.T) {
	url := os.Getenv("REDIS_TEST_URL")
	if url == "" {
		t.Skip("set REDIS_TEST_URL to run Redis login integration tests")
	}

	t.Setenv("REDIS_URL", url)

	issuerClient, err := ConnectRedis()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { issuerClient.Close() })

	redeemerClient, err := ConnectRedis()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { redeemerClient.Close() })

	issuer := NewCLILogins(issuerClient)
	redeemer := NewCLILogins(redeemerClient)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	issue := func(t *testing.T) string {
		t.Helper()

		code, err := issuer.Issue(ctx, "test-token", challenge)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { issuerClient.Del(context.Background(), cliLoginKey(code)) })

		return code
	}

	t.Run("TTL and cross-client redemption", func(t *testing.T) {
		code := issue(t)

		ttl, err := issuerClient.PTTL(ctx, cliLoginKey(code)).Result()
		if err != nil || ttl <= 55*time.Second || ttl > time.Minute {
			t.Fatalf("expected one-minute TTL, got %s: %v", ttl, err)
		}

		for _, invalid := range []string{"short", strings.Repeat("b", 43)} {
			if _, err := redeemer.Redeem(ctx, code, invalid); !errors.Is(err, ErrInvalidCLILogin) {
				t.Fatalf("wrong verifier accepted: %v", err)
			}
		}

		token, err := redeemer.Redeem(ctx, code, verifier)
		if err != nil || token != "test-token" {
			t.Fatalf("cross-client redemption failed: %v", err)
		}

		if _, err := issuer.Redeem(ctx, code, verifier); !errors.Is(err, ErrInvalidCLILogin) {
			t.Fatalf("redeemed code was reusable: %v", err)
		}
	})

	t.Run("expired code", func(t *testing.T) {
		code := issue(t)
		if err := issuerClient.PExpireAt(ctx, cliLoginKey(code), time.Now().Add(-time.Second)).
			Err(); err != nil {
			t.Fatal(err)
		}

		if _, err := redeemer.Redeem(ctx, code, verifier); !errors.Is(err, ErrInvalidCLILogin) {
			t.Fatalf("expired code accepted: %v", err)
		}
	})

	t.Run("concurrent redemption", func(t *testing.T) {
		code := issue(t)
		results := make(chan error, 20)

		var workers sync.WaitGroup

		for range cap(results) {
			workers.Add(1)
			go func() {
				defer workers.Done()

				token, err := redeemer.Redeem(ctx, code, verifier)
				if err == nil && token != "test-token" {
					err = errors.New("wrong token returned")
				}

				results <- err
			}()
		}

		workers.Wait()
		close(results)

		successes := 0

		for err := range results {
			if err == nil {
				successes++
			} else if !errors.Is(err, ErrInvalidCLILogin) {
				t.Errorf("unexpected Redis failure: %v", err)
			}
		}

		if successes != 1 {
			t.Fatalf("expected exactly one redemption, got %d", successes)
		}
	})
}
