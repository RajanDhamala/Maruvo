package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zalando/go-keyring"
)

func mockClient(t *testing.T, provider string, handler http.HandlerFunc) *Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	client, err := NewClient(provider, "test-model", "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	client.baseURL = server.URL

	return client
}

func TestProviderAuthenticationAndCompletion(t *testing.T) {
	for _, provider := range Names {
		t.Run(provider, func(t *testing.T) {
			var paths []string

			client := mockClient(t, provider, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer test-secret" {
					t.Error("missing provider authentication")
				}

				paths = append(paths, r.URL.Path)
				switch r.URL.Path {
				case "/key":
					io.WriteString(w, `{"data":{"label":"test"}}`)
				case "/models":
					io.WriteString(
						w,
						`{"data":[{"id":"z-model"},{"id":"test-model"},{"id":"bad\u001bmodel"}]}`,
					)
				case "/chat/completions":
					var payload map[string]json.RawMessage
					if json.NewDecoder(r.Body).Decode(&payload) != nil ||
						string(payload["model"]) != `"test-model"` {
						t.Error("invalid completion payload")
					}

					if provider == "deepseek" && string(payload["thinking"]) != `{"type":"disabled"}` {
						t.Error("DeepSeek tool requests must disable thinking")
					}

					io.WriteString(
						w,
						`{"choices":[{"message":{"role":"assistant","content":"Hello test-secret"},"finish_reason":"stop"}]}`,
					)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			})

			models, err := client.Models(t.Context())
			if err != nil || len(models) != 2 || models[0].ID != "test-model" {
				t.Fatalf("model discovery: %v %v", models, err)
			}

			if provider == "openrouter" && paths[0] != "/key" {
				t.Fatal("OpenRouter must authenticate before listing public models")
			}

			message, err := client.Complete(t.Context(), []Message{{Role: "user", Content: "Hello"}}, nil)
			if err != nil || message.Content != "Hello [API key redacted]" {
				t.Fatalf("completion: %v %v", message, err)
			}
		})
	}
}

func TestProviderErrorsDoNotExposeCredentials(t *testing.T) {
	for _, status := range []int{401, 402, 429, 500} {
		client := mockClient(t, "openrouter", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			io.WriteString(w, `{"error":"test-secret"}`)
		})

		_, err := client.Models(t.Context())
		if err == nil || strings.Contains(err.Error(), "test-secret") {
			t.Fatalf("HTTP %d: unsafe error %v", status, err)
		}
	}

	client := mockClient(t, "deepseek", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"error":{"message":"test-secret"}}`)
	})
	if _, err := client.Complete(
		t.Context(),
		nil,
		nil,
	); err == nil ||
		strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("unsafe completion error: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := client.Models(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestProviderRedirectDoesNotForwardKey(t *testing.T) {
	reached := false

	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	defer target.Close()

	client := mockClient(t, "deepseek", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	if _, err := client.Models(t.Context()); err == nil || reached {
		t.Fatal("redirect must be rejected before forwarding credentials")
	}
}

func TestCredentialStorageAndProfileIsolation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyring.MockInit()

	for _, profile := range []string{"requester", "worker"} {
		for _, provider := range Names {
			secret := profile + "-" + provider + "-secret"
			if err := SaveConnection(profile, provider, "test-model", secret); err != nil {
				t.Fatal(err)
			}

			got, connection, err := Credential(profile, provider)
			if err != nil || got != secret || connection.Storage != "encrypted" {
				t.Fatalf("profile %s credential failed: %v", profile, err)
			}

			directory, _ := configDirectory(profile)

			info, _ := os.Stat(directory)
			if info.Mode().Perm() != 0700 {
				t.Fatal("provider directory must be private")
			}

			for _, filename := range []string{"config.json", provider + ".enc"} {
				path := filepath.Join(directory, filename)

				data, err := os.ReadFile(path)
				if err != nil || bytes.Contains(data, []byte(secret)) {
					t.Fatal("credential must not appear in metadata or ciphertext")
				}

				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatal("provider files must be private")
				}
			}

			if _, err := os.Stat(filepath.Join(directory, provider+".key")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("plaintext credential file must not exist")
			}
		}
	}

	var out bytes.Buffer
	if err := Commands(
		t.Context(),
		"requester",
		[]string{"status"},
		strings.NewReader(""),
		&out,
		&out,
	); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out.String(), "secret") {
		t.Fatal("provider status exposed a credential")
	}

	if err := Disconnect("worker", "deepseek"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Credential("worker", "deepseek"); err == nil {
		t.Fatal("disconnected credential remains accessible")
	}

	if _, err := keyring.Get(
		encryptionService,
		credentialAccount("worker", "deepseek"),
	); !errors.Is(
		err,
		keyring.ErrNotFound,
	) {
		t.Fatal("disconnect must delete the encryption key")
	}

	if _, _, err := Credential("requester", "deepseek"); err != nil {
		t.Fatal("disconnect must not affect other profiles")
	}
}

func TestUnsafeCredentialFilesAreRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyring.MockInit()

	if err := SaveConnection("", "deepseek", "test-model", "test-secret"); err != nil {
		t.Fatal(err)
	}

	directory, _ := configDirectory("")

	keyPath := filepath.Join(directory, "deepseek.enc")
	if err := os.Chmod(keyPath, 0644); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Credential("", "deepseek"); err == nil {
		t.Fatal("world-readable credential must be rejected")
	}

	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(directory, "config.json"), keyPath); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Credential("", "deepseek"); err == nil {
		t.Fatal("symlink credential must be rejected")
	}

	if err := os.Chmod(directory, 0755); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("nonprivate provider directory must be rejected")
	}

	if err := SaveConnection("../escape", "deepseek", "test-model", "test-secret"); err == nil {
		t.Fatal("profile traversal must be rejected")
	}
}

func TestUnavailableKeyringDoesNotFallBackToFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyring.MockInitWithError(errors.New("locked keyring"))
	t.Cleanup(keyring.MockInit)

	if err := SaveConnection("", "deepseek", "test-model", "test-secret"); err == nil {
		t.Fatal("unavailable keyring must produce an explicit error")
	}

	directory, _ := configDirectory("")
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("keyring failure must not write any credential files")
	}

	var out bytes.Buffer

	err := Commands(t.Context(), "", []string{"connect", "--provider", "deepseek", "--storage", "file"},
		strings.NewReader("test-secret"), &out, &out)
	if err == nil || !strings.Contains(err.Error(), "plaintext storage is disabled") {
		t.Fatal("plaintext CLI storage must be rejected before reading a key or contacting the provider")
	}
}
