package controller

import (
	"context"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"
)

func TestReadinessLeaseOwnershipAndRevocation(t *testing.T) {
	url := os.Getenv("PRESENCE_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set PRESENCE_TEST_REDIS_URL for Redis readiness checks")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	post := time.Now().UnixNano()
	keys := []string{readyKey(post, 1), readyKey(post, 3)}
	defer client.Del(context.Background(), keys...)
	defer client.Del(context.Background(), keys[0]+":cancelled:session-a", keys[0]+":cancelled:session-b")
	run := func(state, nonce string) []string {
		t.Helper()
		v, e := readinessScript.Run(ctx, client, keys, state, nonce).StringSlice()
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	if v := run("ready", "session-a"); v[0] != "session-a" || v[1] != "" {
		t.Fatal(v)
	}
	if ttl := client.TTL(ctx, keys[0]).Val(); ttl <= 0 || ttl > 30*time.Second {
		t.Fatal("readiness must expire", ttl)
	}
	if _, err := readinessScript.Run(ctx, client, keys, "ready", "session-b").Result(); err == nil {
		t.Fatal("another session stole readiness")
	}
	if v := run("stop", "session-b"); v[0] != "session-a" {
		t.Fatal("stale stop revoked current consent")
	}
	if err := client.Set(ctx, keys[1], "peer", 30*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := readinessScript.Run(ctx, client, keys, "ready", "session-a", "expired-peer").Result(); err == nil {
		t.Fatal("expired invitation accepted")
	}
	if _, err := readinessScript.Run(ctx, client, keys, "ready", "session-a", "peer").Result(); err != nil {
		t.Fatal("current invitation rejected", err)
	}
	if v := run("read", ""); v[1] != "peer" {
		t.Fatal(v)
	}
	if v := run("stop", "session-a"); v[0] != "" || v[1] != "peer" {
		t.Fatal("stop affected peer", v)
	}
	if _, err := readinessScript.Run(ctx, client, keys, "ready", "session-a").Result(); err == nil {
		t.Fatal("late ready request revived cancelled invitation")
	}
}
