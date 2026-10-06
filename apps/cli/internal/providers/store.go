package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rajandhamala/Maruvo/cli/internal/auth"
	"github.com/zalando/go-keyring"
)

type Connection struct {
	Model   string `json:"model"`
	Storage string `json:"storage"`
}

type Config struct {
	Active      string                `json:"active,omitempty"`
	Connections map[string]Connection `json:"connections"`
}

func configDirectory(profile string) (string, error) {
	if err := auth.ValidateProfile(profile); err != nil {
		return "", err
	}

	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	directory = filepath.Join(directory, "maruvo")
	if profile != "" && profile != "default" {
		directory = filepath.Join(directory, "profiles", profile)
	}

	return filepath.Join(directory, "providers"), nil
}

func privateDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}

	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("provider directory must be a real private directory")
	}

	return os.Chmod(directory, 0700)
}

func readPrivate(path string) ([]byte, error) {
	directory, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return nil, err
	}

	if !directory.IsDir() || directory.Mode().Perm()&0077 != 0 || directory.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("provider directory must have permissions 0700 and cannot be a symlink")
	}

	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 {
		return nil, errors.New("provider files must be regular private files with permissions 0600")
	}

	return os.ReadFile(path)
}

func writePrivate(path string, data []byte) error {
	if err := privateDirectory(filepath.Dir(path)); err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".provider-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}

	closeErr := file.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	return os.Rename(file.Name(), path)
}

func LoadConfig(profile string) (Config, error) {
	config := Config{Connections: map[string]Connection{}}

	directory, err := configDirectory(profile)
	if err != nil {
		return config, err
	}

	data, err := readPrivate(filepath.Join(directory, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return config, nil
	}

	if err != nil {
		return config, err
	}

	if json.Unmarshal(data, &config) != nil {
		return config, errors.New("invalid local provider configuration")
	}

	if config.Connections == nil {
		config.Connections = map[string]Connection{}
	}

	for name, connection := range config.Connections {
		if !slices.Contains(Names, name) || !validModel(connection.Model) ||
			(connection.Storage != "encrypted" && connection.Storage != "keyring" && connection.Storage != "file") {
			return config, errors.New("invalid local provider connection")
		}
	}

	if config.Active != "" {
		if _, exists := config.Connections[config.Active]; !exists {
			return config, errors.New("active provider is not connected")
		}
	}

	return config, nil
}

func saveConfig(profile string, config Config) error {
	directory, err := configDirectory(profile)
	if err != nil {
		return err
	}

	data, err := json.Marshal(config)
	if err != nil {
		return err
	}

	return writePrivate(filepath.Join(directory, "config.json"), data)
}

func credentialAccount(profile, provider string) string {
	if profile == "" {
		profile = "default"
	}

	return profile + ":" + provider
}

func SaveConnection(profile, provider, model, secret string) error {
	if _, err := NewClient(provider, model, secret); err != nil {
		return err
	}

	if !validModel(model) || strings.Contains(model, strings.TrimSpace(secret)) {
		return errors.New("choose a model")
	}

	config, err := LoadConfig(profile)
	if err != nil {
		return err
	}

	directory, err := configDirectory(profile)
	if err != nil {
		return err
	}

	data, err := encryptCredential(profile, provider, strings.TrimSpace(secret))
	if err != nil {
		return err
	}

	if err := writePrivate(filepath.Join(directory, provider+".enc"), data); err != nil {
		return err
	}

	previous := config.Connections[provider]
	config.Connections[provider] = Connection{Model: model, Storage: "encrypted"}

	config.Active = provider
	if err := saveConfig(profile, config); err != nil {
		return err
	}

	if err := os.Remove(
		filepath.Join(directory, provider+".key"),
	); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return errors.New(
			"encrypted credential saved; reconnect after removing the old plaintext credential file",
		)
	}

	if previous.Storage == "keyring" {
		if err := keyring.Delete("maruvo.providers", credentialAccount(profile, provider)); err != nil &&
			!errors.Is(err, keyring.ErrNotFound) {
			return errors.New(
				"encrypted credential saved; remove the old provider entry from your system keyring",
			)
		}
	}

	return nil
}

func Credential(profile, provider string) (string, Connection, error) {
	config, err := LoadConfig(profile)
	if err != nil {
		return "", Connection{}, err
	}

	connection, ok := config.Connections[provider]
	if !ok {
		return "", Connection{}, errors.New("connect the provider using /model or provider connect")
	}

	if connection.Storage != "encrypted" {
		return "", connection, errors.New(
			"reconnect this provider with its API key to enable encrypted storage",
		)
	}

	directory, err := configDirectory(profile)
	if err != nil {
		return "", connection, err
	}

	if _, err := os.Lstat(filepath.Join(directory, provider+".key")); !errors.Is(err, os.ErrNotExist) {
		return "", connection, errors.New(
			"reconnect the provider to remove its leftover plaintext credential file",
		)
	}

	data, err := readPrivate(filepath.Join(directory, provider+".enc"))
	if err != nil {
		return "", connection, errors.New("encrypted credential unavailable; reconnect the provider")
	}

	secret, err := decryptCredential(profile, provider, data)

	return secret, connection, err
}

func ConnectedClient(profile, provider string) (*Client, error) {
	if provider == "" {
		config, err := LoadConfig(profile)
		if err != nil {
			return nil, err
		}

		provider = config.Active
	}

	secret, connection, err := Credential(profile, provider)
	if err != nil {
		return nil, err
	}

	return NewClient(provider, connection.Model, secret)
}

func Select(profile, provider, model string) error {
	if !validModel(model) {
		return errors.New("choose a model")
	}

	config, err := LoadConfig(profile)
	if err != nil {
		return err
	}

	connection, ok := config.Connections[provider]
	if !ok {
		return errors.New("connect this provider first")
	}

	connection.Model = model
	config.Connections[provider], config.Active = connection, provider

	return saveConfig(profile, config)
}

func Disconnect(profile, provider string) error {
	config, err := LoadConfig(profile)
	if err != nil {
		return err
	}

	connection, ok := config.Connections[provider]
	if !ok {
		return fmt.Errorf("%s is not connected", provider)
	}

	directory, err := configDirectory(profile)
	if err != nil {
		return err
	}

	if connection.Storage == "encrypted" {
		err = os.Remove(filepath.Join(directory, provider+".enc"))
		if err == nil || errors.Is(err, os.ErrNotExist) {
			err = keyring.Delete(encryptionService, credentialAccount(profile, provider))
		}
	} else if connection.Storage == "keyring" {
		err = keyring.Delete("maruvo.providers", credentialAccount(profile, provider))
	} else if connection.Storage == "file" {
		err = os.Remove(filepath.Join(directory, provider+".key"))
	}

	if errors.Is(err, keyring.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		err = nil
	}

	if err != nil {
		return errors.New("could not remove the provider credential")
	}

	delete(config.Connections, provider)

	if config.Active == provider {
		config.Active = ""
	}

	return saveConfig(profile, config)
}
