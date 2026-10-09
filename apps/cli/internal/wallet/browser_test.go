package wallet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestBrowserFundingKeepsValidationAndVerifiesReturnedTransaction(t *testing.T) {
	local, post, plan := fundingFixture(t, 3)
	calls := 0
	remote := &Wallet{address: local.Address(), browserSign: func(data []byte) ([]byte, error) {
		calls++
		copy(data[1:65], ed25519.Sign(local.key, data[65:]))
		return data, nil
	}}
	if _, err := remote.SignFunding(post, plan); err != nil || calls != 1 {
		t.Fatalf("valid browser signing: %v", err)
	}
	changed := post
	changed.CostLamports++
	if _, err := remote.SignFunding(changed, plan); err == nil || calls != 1 {
		t.Fatal("invalid terms reached browser")
	}
	for _, wrongKey := range []bool{false, true} {
		remote.browserSign = func(data []byte) ([]byte, error) {
			key := local.key
			if wrongKey {
				key = testWallet(9).key
			} else {
				data[len(data)-1] ^= 1
			}
			copy(data[1:65], ed25519.Sign(key, data[65:]))
			return data, nil
		}
		if _, err := remote.SignFunding(post, plan); err == nil {
			t.Fatal("changed message or wrong signer accepted")
		}
	}
}

func TestBrowserSettlementKeepsPaymentRules(t *testing.T) {
	for _, action := range []string{"release", "refund"} {
		local, post, plan := settlementFixture(t, action, "reviewer")
		calls := 0
		remote := &Wallet{address: local.Address(), browserSign: func(data []byte) ([]byte, error) {
			calls++
			copy(data[1:65], ed25519.Sign(local.key, data[65:]))
			return data, nil
		}}
		if _, err := remote.SignSettlement(post, plan); err != nil {
			t.Fatal(err)
		}
		plan.Settlement.Action = "steal"
		if _, err := remote.SignSettlement(post, plan); err == nil || calls != 1 {
			t.Fatal("invalid settlement reached browser")
		}
	}
}

func TestBrowserLoopbackRejectsForeignOriginAndCompletesOnce(t *testing.T) {
	calls := 0
	value, err := runBrowser(context.Background(), browserConfig{Operation: "connect"}, nil,
		func(r browserRequest) (string, error) { calls++; return r.Address, nil }, func(origin string) error {
			response, err := http.Get(origin)
			if err != nil {
				return err
			}
			page, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.Header.Get("Cache-Control") != "no-store" || !strings.Contains(response.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Fatal("missing browser isolation headers")
			}
			token := regexp.MustCompile(`csrf="([A-Za-z0-9_-]+)"`).FindSubmatch(page)
			if len(token) != 2 {
				t.Fatalf("missing rendered browser CSRF token: %s", page)
			}
			for _, test := range []struct {
				origin, csrf, host string
				want               int
			}{
				{"https://foreign.example", string(token[1]), "", 403},
				{origin, "wrong", "", 403},
				{origin, string(token[1]), "evil.example", 403},
				{origin, string(token[1]), "", 200},
				{origin, string(token[1]), "", 409},
			} {
				body, _ := json.Marshal(browserRequest{Address: "verified"})
				r, _ := http.NewRequest("POST", origin+"/complete", bytes.NewReader(body))
				r.Header.Set("Origin", test.origin)
				r.Header.Set("X-Maruvo-CSRF", test.csrf)
				if test.host != "" {
					r.Host = test.host
				}
				response, err := http.DefaultClient.Do(r)
				if err != nil {
					return err
				}
				response.Body.Close()
				if response.StatusCode != test.want {
					t.Fatalf("got %d want %d", response.StatusCode, test.want)
				}
			}
			return nil
		})
	if err != nil || value != "verified" || calls != 1 {
		t.Fatalf("loopback result %q %v, calls=%d", value, err, calls)
	}
}

func TestBrowserLoopbackCancellationAndAddressDecoding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, err := runBrowser(ctx, browserConfig{Operation: "sign"}, nil, nil, func(string) error { cancel(); return nil })
	if err == nil {
		t.Fatal("cancelled browser session succeeded")
	}
	for _, seed := range []byte{1, 2, 3} {
		local := testWallet(seed)
		key, err := addressKey(local.Address())
		if err != nil || !bytes.Equal(key, local.key[32:]) {
			t.Fatal("address decoding changed public key")
		}
	}
	if _, err := addressKey("paste-a-wallet-address"); err == nil {
		t.Fatal("invalid address accepted")
	}
	local := testWallet(1)
	raw, _ := base64.StdEncoding.DecodeString("AQ==")
	remote := &Wallet{address: local.Address(), browserSign: func([]byte) ([]byte, error) { return raw, nil }}
	if _, err := remote.signTransaction(make([]byte, 66)); err == nil {
		t.Fatal("truncated signed transaction accepted")
	}
}
