package controller

import (
	"context"
	"github.com/redis/go-redis/v9"
	"os"
	"testing"
	"time"
)

func TestSharedWorkStatusIsBoundToCurrentSession(t *testing.T) {
	url := os.Getenv("PRESENCE_TEST_REDIS_URL")
	if url == "" {
		t.Skip("requires test Redis")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	cache := redis.NewClient(options)
	defer cache.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &Controller{redis: cache}
	post := time.Now().UnixNano()
	key := readyKey(post, 1)
	defer cache.Del(context.Background(), key, key+":status")
	if err := cache.Set(ctx, key, "current-session", 30*time.Second).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.reportWorkStatus(ctx, post, 1, 3, "old-session", "working", "old state"); err != nil {
		t.Fatal(err)
	}
	if status, _ := c.workStatus(ctx, post, 1, "current-session"); status != "" {
		t.Fatal("old session published status")
	}
	for _, state := range []string{"working", "waiting_for_approval", "paused", "listening"} {
		if err := c.reportWorkStatus(ctx, post, 1, 3, "current-session", state, "fixture"); err != nil {
			t.Fatal(err)
		}
		if status, detail := c.workStatus(ctx, post, 1, "current-session"); status != state || detail != "fixture" {
			t.Fatal("status snapshot did not reflect current state", status, detail)
		}
	}
	if status, _ := c.workStatus(ctx, post, 1, "another-session"); status != "" {
		t.Fatal("status leaked across sessions")
	}
	if err := cache.Del(ctx, key).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.reportWorkStatus(ctx, post, 1, 3, "current-session", "working", "revived"); err != nil {
		t.Fatal(err)
	}
	if _, detail := c.workStatus(ctx, post, 1, "current-session"); detail == "revived" {
		t.Fatal("revoked session revived status")
	}
}

func TestWorkStatusValidation(t *testing.T) {
	for _, status := range []string{"", "ready", "working", "waiting_for_approval", "paused", "listening"} {
		if !validWorkStatus(status) {
			t.Fatal(status)
		}
	}
	if validWorkStatus("completed") || validWorkStatus("arbitrary remote command") {
		t.Fatal("accepted invalid state")
	}
}
