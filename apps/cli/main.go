package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rajandhamala/Maruvo/cli/internal/agent"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
	"github.com/rajandhamala/Maruvo/cli/internal/providers"
	"github.com/rajandhamala/Maruvo/cli/internal/tui"
	walletconfig "github.com/rajandhamala/Maruvo/cli/internal/wallet"
)

func main() {
	explicitWallet := os.Getenv("MARUVO_WALLET")
	agentMode := agentInvocation(os.Args[1:])
	fail := func(err error) {
		if agentMode {
			_ = agent.WriteError(os.Stderr, err)
		} else {
			fmt.Fprintln(os.Stderr, err)
		}

		os.Exit(1)
	}

	command := commandInvocation(os.Args[1:])

	defaultURL := os.Getenv("API_URL")
	if defaultURL == "" {
		defaultURL = "http://127.0.0.1:3000"
	}

	flags := flag.NewFlagSet("maruvo", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	if agentMode {
		flags.SetOutput(io.Discard)
	}

	apiURL := flags.String("api", defaultURL, "Go API base URL (or set API_URL)")
	message := flags.String("message", "Hello from Bubble Tea", "demo message to send to Rust")
	check := flags.Bool("check", false, "send once without a terminal UI; exit nonzero on failure")
	demo := flags.Bool("demo", false, "show the existing Go/Rust connection demo")
	profile := flags.String("profile", "default", "saved login profile (e.g. poster or worker)")

	wallet := flags.String("wallet", "", "wallet keypair file (saved separately for each profile)")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			if agentMode {
				flags.SetOutput(os.Stderr)
				flags.PrintDefaults()
			}

			return
		}

		fail(agent.InvalidArgument(err.Error()))
	}

	args := flags.Args()

	bridgeMode := len(args) > 1 && args[0] == "agent" && args[1] == "bridge"
	if command != "provider" && command != "chat" && !bridgeMode {
		if err := walletconfig.LoadConfig(); err != nil {
			fail(fmt.Errorf("load local Solana configuration: %w", err))
		}
	}

	if len(args) > 0 && (args[0] == "provider" || args[0] == "chat") {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		if err := auth.ValidateProfile(*profile); err != nil {
			fail(err)
		}

		var err error

		if args[0] == "provider" {
			err = providers.Commands(ctx, *profile, args[1:], os.Stdin, os.Stdout, os.Stderr)
		} else {
			*apiURL = strings.TrimRight(*apiURL, "/")

			parsed, parseErr := url.Parse(*apiURL)
			if parseErr != nil || parsed.Host == "" ||
				(parsed.Scheme != "http" && parsed.Scheme != "https") ||
				parsed.RawQuery != "" ||
				parsed.Fragment != "" ||
				parsed.User != nil {
				fail(
					errors.New(
						"API URL must be an http(s) base URL without credentials, a query or fragment",
					),
				)
			}

			err = providers.ChatCommand(
				ctx,
				*profile,
				args[1:],
				os.Stdin,
				os.Stdout,
				os.Stderr,
				api.NewClient(*apiURL),
			)
		}

		if err != nil {
			fail(err)
		}

		return
	}

	listTools := len(args) > 1 && args[0] == "agent" && args[1] == "tools"
	*apiURL = strings.TrimRight(*apiURL, "/")

	parsed, err := url.Parse(*apiURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		fail(agent.InvalidArgument("API URL must be an http(s) base URL without a query or fragment"))
	}

	if err := auth.ValidateProfile(*profile); err != nil {
		fail(agent.InvalidArgument(err.Error()))
	}

	if !*check && !*demo && !listTools && !bridgeMode {
		chosen := *wallet
		if chosen == "" {
			chosen = explicitWallet
		}

		if chosen == "" {
			var err error

			chosen, err = auth.LoadWallet(*profile)
			if err != nil {
				fail(err)
			}
		}

		if chosen == "" {
			chosen = os.Getenv("MARUVO_WALLET")
		}

		if chosen != "" {
			path, err := filepath.Abs(chosen)
			if err != nil {
				fail(err)
			}

			if err = auth.SaveWallet(*profile, path); err != nil {
				fail(err)
			}

			os.Setenv("MARUVO_WALLET", path)
		}
	}

	var runErr error

	if len(args) > 0 {
		if args[0] != "agent" {
			fmt.Fprintln(
				os.Stderr,
				"unknown command; use agent, provider, chat, or run without a command for the terminal UI",
			)
			os.Exit(1)
		}

		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		runErr = agent.Run(ctx, api.NewClient(*apiURL), *profile, args[1:], os.Stdout, os.Stderr)
	} else {
		runErr = run(*apiURL, *message, *check, *demo, *profile)
	}

	if runErr != nil {
		fail(runErr)
	}
}

func agentInvocation(args []string) bool {
	return commandInvocation(args) == "agent"
}

func commandInvocation(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) {
				return args[i+1]
			}

			return ""
		}

		if !strings.HasPrefix(arg, "-") {
			return arg
		}

		name, _, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !assigned && (name == "api" || name == "profile" || name == "wallet" || name == "message") {
			i++
		}
	}

	return ""
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
