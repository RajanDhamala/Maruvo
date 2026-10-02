package utils

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

type cliLogin struct {
	token     string
	challenge string
	expiresAt time.Time
}

type CLILogins struct {
	mu    sync.Mutex
	codes map[string]cliLogin
}

func NewCLILogins() *CLILogins {
	return &CLILogins{codes: make(map[string]cliLogin)}
}

func (s *CLILogins) Issue(token, challenge string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	for code, login := range s.codes {
		if !time.Now().Before(login.expiresAt) {
			delete(s.codes, code)
		}
	}

	code := rand.Text()
	s.codes[code] = cliLogin{token: token, challenge: challenge, expiresAt: time.Now().Add(time.Minute)}

	return code
}

func (s *CLILogins) Redeem(code, verifier string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	login, ok := s.codes[code]
	if !ok || !time.Now().Before(login.expiresAt) {
		delete(s.codes, code)
		return "", errors.New("invalid or expired CLI login code")
	}

	hash := sha256.Sum256([]byte(verifier))

	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	if len(verifier) < 43 || len(verifier) > 128 ||
		subtle.ConstantTimeCompare([]byte(challenge), []byte(login.challenge)) != 1 {
		return "", errors.New("invalid CLI login verifier")
	}

	delete(s.codes, code)

	return login.token, nil
}
