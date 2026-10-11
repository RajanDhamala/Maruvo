package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

type HarnessConnection struct {
	APIURL     string   `json:"api_url"`
	Account    string   `json:"account"`
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
	Directory  string   `json:"directory"`
	Prompt     bool     `json:"prompt"`
}

func RemotePath(profile, name string) (string, error) {
	if name != "harness.json" && name != "inbox.json" {
		return "", errors.New("invalid remote state file")
	}

	dir, err := profileDirectory(profile)

	return filepath.Join(dir, name), err
}

func SaveRemoteState(profile, name string, value any) error {
	path, err := RemotePath(profile, name)
	if err != nil {
		return err
	}

	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("remote state must be a regular private file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return writeJSON(path, value)
}

func LoadRemoteState(profile, name string, value any) error {
	path, err := RemotePath(profile, name)
	if err != nil {
		return err
	}

	info, err := os.Lstat(path)
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 {
		return errors.New("remote state must be a private regular file up to 64 KiB")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, value)
}

func LockRemoteInbox(profile string) (func(), error) {
	path, err := RemotePath(profile, "inbox.json")
	if err != nil {
		return nil, err
	}

	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}

	fd, err := unix.Open(path+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}

	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		return nil, errors.New("an inbox listener is already running for this profile")
	}

	return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }, nil
}
