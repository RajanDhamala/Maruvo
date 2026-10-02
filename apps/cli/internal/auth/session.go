package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type session struct {
	APIURL string `json:"api_url"`
	Token  string `json:"token"`
}

func ValidateProfile(profile string) error {
	if len(profile) > 32 {
		return errors.New("profile name must be at most 32 characters")
	}

	for _, c := range profile {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' ||
			c == '_' {
			continue
		}

		return errors.New("profile name can contain only letters, numbers, hyphens, and underscores")
	}

	return nil
}

func profileDirectory(profile string) (string, error) {
	if err := ValidateProfile(profile); err != nil {
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

	return directory, nil
}

func sessionPath(profile string) (string, error) {
	directory, err := profileDirectory(profile)
	return filepath.Join(directory, "session.json"), err
}

func LoadSession(apiURL, profile string) (string, error) {
	path, err := sessionPath(profile)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	var saved session
	if err := json.Unmarshal(data, &saved); err != nil {
		return "", err
	}

	if saved.APIURL != apiURL {
		return "", nil
	}

	return saved.Token, nil
}

func SaveSession(apiURL, token, profile string) error {
	path, err := sessionPath(profile)
	if err != nil {
		return err
	}

	return writeJSON(path, session{APIURL: apiURL, Token: token})
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".session-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())

	err = json.NewEncoder(file).Encode(value)
	closeErr := file.Close()

	if err != nil {
		return err
	}

	if closeErr != nil {
		return closeErr
	}

	return os.Rename(file.Name(), path)
}

func ClearSession(apiURL, profile string) error {
	token, err := LoadSession(apiURL, profile)
	if err != nil || token == "" {
		return err
	}

	path, err := sessionPath(profile)
	if err != nil {
		return err
	}

	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	return err
}

func LoadWallet(profile string) (string, error) {
	directory, err := profileDirectory(profile)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(filepath.Join(directory, "wallet.json"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", err
	}

	var saved struct {
		Path string `json:"path"`
	}
	if err = json.Unmarshal(data, &saved); err != nil {
		return "", fmt.Errorf("read profile wallet: %w", err)
	}

	return saved.Path, nil
}

func SaveWallet(profile, path string) error {
	directory, err := profileDirectory(profile)
	if err != nil {
		return err
	}

	return writeJSON(filepath.Join(directory, "wallet.json"), struct {
		Path string `json:"path"`
	}{Path: path})
}
