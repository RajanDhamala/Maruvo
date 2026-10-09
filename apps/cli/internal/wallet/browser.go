package wallet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

//go:embed browser.html
var browserPage string

type browserRequest struct {
	Address     string `json:"address"`
	Signature   string `json:"signature"`
	Transaction string `json:"transaction"`
}

type browserConfig struct{ Operation, Address, Transaction, CSRF, Nonce string }

func Browser(ctx context.Context, address string) (*Wallet, error) {
	if _, err := addressKey(address); err != nil {
		return nil, err
	}
	return &Wallet{address: address, browserSign: func(data []byte) ([]byte, error) {
		if err := validateSigningConfig("devnet", ProgramID); err != nil {
			return nil, err
		}
		encoded, err := runBrowser(ctx, browserConfig{Operation: "sign", Address: address, Transaction: base64.StdEncoding.EncodeToString(data)}, nil,
			func(r browserRequest) (string, error) {
				if r.Address != address {
					return "", errors.New("select the wallet linked to this account")
				}
				return r.Transaction, nil
			}, auth.OpenBrowser)
		if err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(encoded)
	}}, nil
}

func addressKey(address string) (ed25519.PublicKey, error) {
	if len(address) < 32 || len(address) > 44 {
		return nil, errors.New("invalid wallet address")
	}
	n := new(big.Int)
	for _, r := range address {
		i := strings.IndexRune(alphabet, r)
		if i < 0 {
			return nil, errors.New("invalid wallet address")
		}
		n.Mul(n, big.NewInt(58)).Add(n, big.NewInt(int64(i)))
	}
	key := append(make([]byte, len(address)-len(strings.TrimLeft(address, "1"))), n.Bytes()...)
	if len(key) != ed25519.PublicKeySize {
		return nil, errors.New("invalid wallet address")
	}
	return ed25519.PublicKey(key), nil
}

func (w *Wallet) signTransaction(data []byte) (string, error) {
	if w.browserSign == nil {
		copy(data[1:65], ed25519.Sign(w.key, data[65:]))
		return base64.StdEncoding.EncodeToString(data), nil
	}
	signed, err := w.browserSign(append([]byte(nil), data...))
	if err != nil {
		return "", err
	}
	key, err := addressKey(w.Address())
	if err != nil || len(signed) != len(data) || len(signed) < 65 || signed[0] != 1 ||
		!bytes.Equal(signed[65:], data[65:]) || !ed25519.Verify(key, data[65:], signed[1:65]) {
		return "", errors.New("browser wallet returned a changed transaction or invalid signature")
	}
	return base64.StdEncoding.EncodeToString(signed), nil
}

func ConnectBrowser(ctx context.Context, client *api.Client, token string) (string, error) {
	if err := validateSigningConfig("devnet", ProgramID); err != nil {
		return "", err
	}
	var issued string
	return runBrowser(ctx, browserConfig{Operation: "connect"}, func(r browserRequest) (string, error) {
		linked, err := client.Wallet(ctx, token)
		if err != nil {
			return "", err
		}
		if linked != "" && linked != r.Address {
			return "", errors.New("this account already has a different wallet; select its wallet or use a separate account")
		}
		message, err := client.WalletChallenge(ctx, token, r.Address)
		if err == nil {
			issued = r.Address
		}
		return message, err
	}, func(r browserRequest) (string, error) {
		if issued == "" || issued != r.Address {
			return "", errors.New("request a challenge for this wallet first")
		}
		if err := client.LinkWallet(ctx, token, r.Signature); err != nil {
			return "", err
		}
		return issued, nil
	}, auth.OpenBrowser)
}

func runBrowser(parent context.Context, config browserConfig, challenge, complete func(browserRequest) (string, error), open func(string) error) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	config.CSRF = base64.RawURLEncoding.EncodeToString(secret)
	config.Nonce = config.CSRF
	origin := "http://" + listener.Addr().String()
	page, err := template.New("wallet").Parse(browserPage)
	if err != nil {
		return "", err
	}
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	var lock sync.Mutex
	finished := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+config.Nonce+"'; style-src 'nonce-"+config.Nonce+"'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		if r.Host != listener.Addr().String() {
			http.Error(w, "invalid host", 403)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = page.Execute(w, config)
			return
		}
		if r.Method != "POST" || r.Header.Get("Origin") != origin || r.Header.Get("X-Maruvo-CSRF") != config.CSRF {
			http.Error(w, "invalid browser request", 403)
			return
		}
		lock.Lock()
		defer lock.Unlock()
		if finished {
			http.Error(w, "request already completed", 409)
			return
		}
		if r.URL.Path == "/cancel" {
			finished = true
			w.WriteHeader(204)
			done <- result{err: errors.New("wallet connection or signing cancelled")}
			return
		}
		var payload browserRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&payload) != nil {
			http.Error(w, "invalid payload", 400)
			return
		}
		var value string
		var err error
		switch r.URL.Path {
		case "/challenge":
			if challenge == nil {
				http.Error(w, "not available", 404)
				return
			}
			value, err = challenge(payload)
		case "/complete":
			value, err = complete(payload)
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"value": value})
		if r.URL.Path == "/complete" {
			finished = true
			done <- result{value: value}
		}
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case done <- result{err: err}:
			default:
			}
		}
	}()
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()
	if err := open(origin); err != nil {
		return "", fmt.Errorf("open wallet browser: %w", err)
	}
	select {
	case result := <-done:
		return result.value, result.err
	case <-ctx.Done():
		return "", fmt.Errorf("wallet browser closed or timed out: %w", ctx.Err())
	}
}
