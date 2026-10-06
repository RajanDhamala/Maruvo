package providers

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/zalando/go-keyring"
)

const encryptionService = "maruvo.providers.encryption"

type encryptedCredential struct {
	Version    int    `json:"version"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

func credentialCipher(profile, provider string, create bool) (cipher.AEAD, error) {
	account := credentialAccount(profile, provider)

	encoded, err := keyring.Get(encryptionService, account)
	if errors.Is(err, keyring.ErrNotFound) && create {
		key := make([]byte, 32)
		defer clear(key)

		if _, err := rand.Read(key); err != nil {
			return nil, errors.New("could not generate a credential encryption key")
		}

		encoded = base64.StdEncoding.EncodeToString(key)
		err = keyring.Set(encryptionService, account, encoded)
	}

	if err != nil {
		return nil, errors.New(
			"encryption key unavailable; unlock your system keyring or reconnect the provider",
		)
	}

	key, err := base64.StdEncoding.DecodeString(encoded)
	defer clear(key)

	if err != nil || len(key) != 32 {
		return nil, errors.New(
			"invalid credential encryption key; remove its system keyring entry and reconnect",
		)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("could not initialize credential encryption")
	}

	return cipher.NewGCM(block)
}

func encryptCredential(profile, provider, secret string) ([]byte, error) {
	box, err := credentialCipher(profile, provider, true)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, box.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("could not generate a credential encryption nonce")
	}

	plaintext := []byte(secret)
	defer clear(plaintext)

	data := encryptedCredential{
		Version: 1,
		Nonce:   nonce,
		Ciphertext: box.Seal(
			nil,
			nonce,
			plaintext,
			[]byte("maruvo-provider-v1:"+credentialAccount(profile, provider)),
		),
	}

	return json.Marshal(data)
}

func decryptCredential(profile, provider string, data []byte) (string, error) {
	var credential encryptedCredential

	if json.Unmarshal(data, &credential) != nil || credential.Version != 1 {
		return "", errors.New("invalid encrypted credential; reconnect the provider")
	}

	box, err := credentialCipher(profile, provider, false)
	if err != nil {
		return "", err
	}

	if len(credential.Nonce) != box.NonceSize() {
		return "", errors.New("invalid encrypted credential nonce; reconnect the provider")
	}

	plaintext, err := box.Open(nil, credential.Nonce, credential.Ciphertext,
		[]byte("maruvo-provider-v1:"+credentialAccount(profile, provider)))
	defer clear(plaintext)

	if err != nil {
		return "", errors.New("credential authentication failed; reconnect the provider")
	}

	return string(plaintext), nil
}
