package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajandhamala/Maruvo/internal/utils"
	"github.com/redis/go-redis/v9"
)

func TestExchangeCLITokenRedisUnavailable(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	client.Close()
	ctrl := &Controller{cliLogins: utils.NewCLILogins(client)}

	request := httptest.NewRequest(http.MethodPost, "/oauth/cli/token",
		strings.NewReader(`{"code":"test-code","code_verifier":"`+strings.Repeat("a", 43)+`"}`))
	response := httptest.NewRecorder()
	ctrl.ExchangeCLIToken(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for unavailable Redis, got %d", response.Code)
	}
}

func TestExchangeCLITokenInvalidVerifier(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"})
	client.Close()
	ctrl := &Controller{cliLogins: utils.NewCLILogins(client)}

	request := httptest.NewRequest(http.MethodPost, "/oauth/cli/token",
		strings.NewReader(`{"code":"test-code","code_verifier":"short"}`))
	response := httptest.NewRecorder()
	ctrl.ExchangeCLIToken(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid verifier, got %d", response.Code)
	}
}
