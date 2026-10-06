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

func validateSigningConfig(network, programID string) error {
	var configuredNetwork string

	switch strings.TrimRight(os.Getenv("SOLANA_RPC_URL"), "/") {
	case "https://api.devnet.solana.com":
		configuredNetwork = "devnet"
	case "http://127.0.0.1:8899", "http://localhost:8899":
		configuredNetwork = "localnet"
	default:
		return errors.New("SOLANA_RPC_URL must be the public Devnet endpoint or localhost:8899")
	}

	if network != configuredNetwork {
		return errors.New("transaction network does not match your local Solana configuration")
	}

	if programID != ProgramID || os.Getenv("SOLANA_PROGRAM_ID") != ProgramID {
		return errors.New("escrow program does not match your local Solana configuration")
	}

	return nil
}
