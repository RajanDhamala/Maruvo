package providers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestEncryptedCredentialsAuthenticateAndBindIdentity(t *testing.T) {
	keyring.MockInit()

	first, err := encryptCredential("worker", "deepseek", "private-api-key")
	if err != nil {
		t.Fatal(err)
	}

	second, err := encryptCredential("worker", "deepseek", "private-api-key")
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("each encryption must use a fresh nonce")
	}

	got, err := decryptCredential("worker", "deepseek", first)
	if err != nil || got != "private-api-key" {
		t.Fatal("encrypted credential failed to round trip")
	}

	master, err := keyring.Get(encryptionService, credentialAccount("worker", "deepseek"))
	if err != nil || bytes.Contains(first, []byte(master)) {
		t.Fatal("encryption key must stay separate from the ciphertext")
	}

	for _, identity := range [][2]string{{"requester", "deepseek"}, {"worker", "openrouter"}} {
		if err := keyring.Set(
			encryptionService,
			credentialAccount(identity[0], identity[1]),
			master,
		); err != nil {
			t.Fatal(err)
		}

		if _, err := decryptCredential(identity[0], identity[1], first); err == nil {
			t.Fatal("ciphertext must be bound to its profile and provider, even with the same encryption key")
		}
	}

	var credential encryptedCredential
	if err := json.Unmarshal(first, &credential); err != nil {
		t.Fatal(err)
	}

	credential.Ciphertext[0] ^= 1

	tampered, _ := json.Marshal(credential)
	if _, err := decryptCredential("worker", "deepseek", tampered); err == nil {
		t.Fatal("modified ciphertext must fail authentication")
	}

	credential.Nonce = nil

	tampered, _ = json.Marshal(credential)
	if _, err := decryptCredential("worker", "deepseek", tampered); err == nil {
		t.Fatal("invalid nonce must fail without panicking")
	}

	credential.Version = 2

	tampered, _ = json.Marshal(credential)
	if _, err := decryptCredential("worker", "deepseek", tampered); err == nil {
		t.Fatal("unsupported credential version must be rejected")
	}

	if err := keyring.Set(encryptionService, credentialAccount("worker", "deepseek"),
		base64.StdEncoding.EncodeToString(make([]byte, 32))); err != nil {
		t.Fatal(err)
	}

	if _, err := decryptCredential("worker", "deepseek", first); err == nil {
		t.Fatal("wrong encryption key must fail authentication")
	}
}

func TestLegacyCredentialsRequireEncryptionAndMigrateOnReconnect(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyring.MockInit()

	for _, storage := range []string{"file", "keyring"} {
		profile, secret := "legacy-"+storage, "legacy-"+storage+"-api-key"
		directory, _ := configDirectory(profile)

		config := Config{Active: "deepseek", Connections: map[string]Connection{
			"deepseek": {Model: "test-model", Storage: storage},
		}}
		if err := saveConfig(profile, config); err != nil {
			t.Fatal(err)
		}

		if storage == "file" {
			if err := writePrivate(filepath.Join(directory, "deepseek.key"), []byte(secret)); err != nil {
				t.Fatal(err)
			}
		} else if err := keyring.Set("maruvo.providers", credentialAccount(profile, "deepseek"), secret); err != nil {
			t.Fatal(err)
		}

		if _, _, err := Credential(profile, "deepseek"); err == nil {
			t.Fatal("legacy credentials must not load through the agent")
		}

		if err := SaveConnection(profile, "deepseek", "test-model", secret); err != nil {
			t.Fatal(err)
		}

		if _, err := os.Stat(filepath.Join(directory, "deepseek.key")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("reconnecting must remove the old plaintext file")
		}

		if _, err := keyring.Get(
			"maruvo.providers",
			credentialAccount(profile, "deepseek"),
		); !errors.Is(
			err,
			keyring.ErrNotFound,
		) {
			t.Fatal("reconnecting must remove the old direct provider keyring entry")
		}

		got, _, err := Credential(profile, "deepseek")
		if err != nil || got != secret {
			t.Fatal("migrated encrypted credential failed to load")
		}
	}
}

func TestMissingEncryptionKeyAndLeftoverPlaintextBlockLoading(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	keyring.MockInit()

	if err := SaveConnection("worker", "deepseek", "test-model", "private-api-key"); err != nil {
		t.Fatal(err)
	}

	directory, _ := configDirectory("worker")
	if err := writePrivate(filepath.Join(directory, "deepseek.key"), []byte("old-plaintext")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Credential("worker", "deepseek"); err == nil {
		t.Fatal("leftover plaintext credential must block agent loading")
	}

	if err := SaveConnection("worker", "deepseek", "test-model", "private-api-key"); err != nil {
		t.Fatal(err)
	}

	if err := keyring.Delete(encryptionService, credentialAccount("worker", "deepseek")); err != nil {
		t.Fatal(err)
	}

	if _, _, err := Credential("worker", "deepseek"); err == nil {
		t.Fatal("missing encryption key must not fall back to plaintext or generate a replacement")
	}

	if _, err := keyring.Get(
		encryptionService,
		credentialAccount("worker", "deepseek"),
	); !errors.Is(
		err,
		keyring.ErrNotFound,
	) {
		t.Fatal("loading must not replace a missing encryption key")
	}
}
