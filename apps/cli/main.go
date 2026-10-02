package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rajandhamala/Maruvo/cli/internal/agent"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
	"github.com/rajandhamala/Maruvo/cli/internal/tui"
	walletconfig "github.com/rajandhamala/Maruvo/cli/internal/wallet"
)

func main() {
	explicitWallet := os.Getenv("MARUVO_WALLET")

	if err := walletconfig.LoadConfig(); err != nil {
		fmt.Fprintln(os.Stderr, "load local Solana configuration:", err)
		os.Exit(1)
	}

	defaultURL := os.Getenv("API_URL")
	if defaultURL == "" {
		defaultURL = "http://127.0.0.1:3000"
	}

	apiURL := flag.String("api", defaultURL, "Go API base URL (or set API_URL)")
	message := flag.String("message", "Hello from Bubble Tea", "demo message to send to Rust")
	check := flag.Bool("check", false, "send once without a terminal UI; exit nonzero on failure")
	demo := flag.Bool("demo", false, "show the existing Go/Rust connection demo")
	profile := flag.String("profile", "default", "saved login profile (e.g. poster or worker)")
	wallet := flag.String("wallet", "", "wallet keypair file (saved separately for each profile)")

	flag.Parse()

	args := flag.Args()
	listTools := len(args) > 1 && args[0] == "agent" && args[1] == "tools"
	*apiURL = strings.TrimRight(*apiURL, "/")

	parsed, err := url.Parse(*apiURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		fmt.Fprintln(os.Stderr, "API URL must be an http(s) base URL without a query or fragment")
		os.Exit(1)
	}

	if err := auth.ValidateProfile(*profile); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if !*check && !*demo && !listTools {
		chosen := *wallet
		if chosen == "" {
			chosen = explicitWallet
		}

		if chosen == "" {
			var err error

			chosen, err = auth.LoadWallet(*profile)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}

		if chosen == "" {
			chosen = os.Getenv("MARUVO_WALLET")
		}

		if chosen != "" {
			path, err := filepath.Abs(chosen)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}

			if err = auth.SaveWallet(*profile, path); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}

			os.Setenv("MARUVO_WALLET", path)
		}
	}

	var runErr error

	if len(args) > 0 {
		if args[0] != "agent" {
			fmt.Fprintln(os.Stderr, "unknown command; use agent or run without a command for the terminal UI")
			os.Exit(1)
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		runErr = agent.Run(ctx, api.NewClient(*apiURL), *profile, args[1:], os.Stdout, os.Stderr)
	} else {
		runErr = run(*apiURL, *message, *check, *demo, *profile)
	}

	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}

func run(apiURL, message string, check, demo bool, profile string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := api.NewClient(apiURL)
	if check {
		response, err := client.Demo(ctx, message)
		if err != nil {
			return err
		}

		return json.NewEncoder(os.Stdout).Encode(response)
	}

	return tui.Run(ctx, client, message, demo, profile)
}
