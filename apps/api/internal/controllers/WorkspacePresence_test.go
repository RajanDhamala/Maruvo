package controller

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestWorkspacePresenceMultipleConnectionsAndExpiry(t *testing.T) {
	url := os.Getenv("PRESENCE_TEST_REDIS_URL")
	if url == "" {
		t.Skip("set PRESENCE_TEST_REDIS_URL to a test Redis service")
	}
	options, err := redis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c := &Controller{redis: client}
	post, user := time.Now().UnixNano(), int64(1)
	key := presenceKey(post, user)
	defer client.Del(context.Background(), key)
	for _, conn := range []string{"tab-a", "tab-b"} {
		if err := c.touchPresence(ctx, post, user, conn); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int64 {
		t.Helper()
		n, err := client.ZCount(ctx, key, "("+strconv.FormatInt(time.Now().UnixMilli(), 10), "+inf").Result()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 2 {
		t.Fatal("two connections must both be live")
	}
	if err := client.ZRem(ctx, key, "tab-a").Err(); err != nil {
		t.Fatal(err)
	}
	if count() != 1 {
		t.Fatal("closing one tab must not make the account offline")
	}
	if err := client.ZAdd(ctx, key, redis.Z{Score: float64(time.Now().Add(-time.Second).UnixMilli()), Member: "tab-b"}).Err(); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("expired connection must not report online")
	}
	if err := c.touchPresence(ctx, post, user, "tab-c"); err != nil {
		t.Fatal(err)
	}
	if n, err := client.ZCard(ctx, key).Result(); err != nil || n != 1 {
		t.Fatalf("expired connections not cleaned: %d %v", n, err)
	}
	if ttl, err := client.TTL(ctx, key).Result(); err != nil || ttl <= 0 || ttl > 2*pongWait {
		t.Fatalf("presence keys must expire: %v %v", ttl, err)
	}
}
