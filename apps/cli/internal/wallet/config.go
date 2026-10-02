package wallet

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func LoadConfig() error {
	directory, err := os.Getwd()
	if err != nil {
		return err
	}

	for {
		file, err := os.Open(filepath.Join(directory, ".solana", "env"))
		if err == nil {
			defer file.Close()

			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				name, value, ok := strings.Cut(scanner.Text(), "=")
				name, value = strings.TrimSpace(name), strings.TrimSpace(value)

				if !ok {
					continue
				}

				switch name {
				case "SOLANA_RPC_URL", "SOLANA_PROGRAM_ID", "SOLANA_REVIEWER", "MARUVO_WALLET":
					if _, exists := os.LookupEnv(name); exists {
						continue
					}

					if name == "MARUVO_WALLET" && value != "" && !filepath.IsAbs(value) {
						value = filepath.Join(directory, value)
					}

					if err := os.Setenv(name, value); err != nil {
						return err
					}
				}
			}

			return scanner.Err()
		}

		if !errors.Is(err, os.ErrNotExist) {
			return err
		}

		parent := filepath.Dir(directory)
		if parent == directory {
			return nil
		}

		directory = parent
	}
}
