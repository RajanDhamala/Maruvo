package utils

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const cliLoginTTL = time.Minute

var ErrInvalidCLILogin = errors.New("invalid or expired CLI login code or verifier")

// Lua Script 2 Verify the challenge and consume the code in a single Redis operation.
var redeemCLILogin = redis.NewScript(`
local value = redis.call('GET', KEYS[1])
if not value then
    return false
end
local login = cjson.decode(value)
if login.challenge ~= ARGV[1] then
    return false
end
redis.call('DEL', KEYS[1])
return login.token
`)

type CLILogins struct {
	client *redis.Client
}

func NewCLILogins(client *redis.Client) *CLILogins {
	temp := CLILogins{
		client: client,
	}

	return &temp
}

func (s *CLILogins) Issue(ctx context.Context, token, challenge string) (string, error) {
	login := struct {
		Token     string `json:"token"`
		Challenge string `json:"challenge"`
	}{
		Token:     token,
		Challenge: challenge,
	}

	value, err := json.Marshal(login)
	if err != nil {
		return "", err
	}

	for {
		code := rand.Text()

		stored, err := s.client.SetNX(ctx, cliLoginKey(code), value, cliLoginTTL).Result()
		if err != nil {
			return "", fmt.Errorf("store CLI login: %w", err)
		}

		if stored {
			return code, nil
		}
	}
}

func (s *CLILogins) Redeem(ctx context.Context, code, verifier string) (string, error) {
	if code == "" || len(verifier) < 43 || len(verifier) > 128 {
		return "", ErrInvalidCLILogin
	}

	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	token, err := redeemCLILogin.Run(ctx, s.client, []string{cliLoginKey(code)}, challenge).Text()
	if errors.Is(err, redis.Nil) {
		return "", ErrInvalidCLILogin
	}

	if err != nil {
		return "", fmt.Errorf("redeem CLI login: %w", err)
	}

	return token, nil
}

func cliLoginKey(code string) string {
	return "maruvo:oauth:cli:" + code
}
