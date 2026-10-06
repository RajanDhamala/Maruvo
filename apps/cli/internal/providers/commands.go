package providers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
)

func Commands(ctx context.Context, profile string, args []string, in io.Reader, out, log io.Writer) error {
	if len(args) == 0 {
		return errors.New("provider commands: connect, status, models, use, remove")
	}

	flags := flag.NewFlagSet("provider "+args[0], flag.ContinueOnError)
	flags.SetOutput(log)
	provider := flags.String("provider", "", "deepseek or openrouter")
	model := flags.String("model", "", "model ID from provider models")

	storage := flags.String("storage", "encrypted", "encrypted storage only; requires the OS keyring")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	if flags.NArg() != 0 {
		return errors.New("unexpected provider arguments")
	}

	encode := json.NewEncoder(out).Encode

	switch args[0] {
	case "status":
		config, err := LoadConfig(profile)
		if err != nil {
			return err
		}

		return encode(config)
	case "connect":
		if *provider != "deepseek" && *provider != "openrouter" {
			return errors.New("choose --provider deepseek or openrouter")
		}

		if *storage != "encrypted" {
			return errors.New("provider keys require encrypted storage; plaintext storage is disabled")
		}

		var (
			secret []byte
			err    error
		)

		if file, ok := in.(*os.File); ok && term.IsTerminal(file.Fd()) {
			fmt.Fprint(log, "API key (hidden): ")

			secret, err = term.ReadPassword(file.Fd())

			fmt.Fprintln(log)
		} else {
			secret, err = io.ReadAll(io.LimitReader(in, 4097))
			if len(secret) > 4096 {
				return errors.New("API key is too long")
			}
		}

		if err != nil {
			return errors.New("could not read the API key")
		}

		key := strings.TrimSpace(string(secret))
		clear(secret)

		client, err := NewClient(*provider, "", key)
		if err != nil {
			return err
		}

		checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		models, err := client.Models(checkCtx)
		if err != nil {
			return err
		}

		if *model == "" {
			return errors.New("choose --model from provider models; use /model for the interactive picker")
		}

		if !containsModel(models, *model) {
			return errors.New("model is not available from this provider")
		}

		if err := SaveConnection(profile, *provider, *model, key); err != nil {
			return err
		}

		return encode(
			map[string]string{
				"status":   "connected",
				"provider": *provider,
				"model":    *model,
				"storage":  *storage,
			},
		)
	case "models":
		client, err := ConnectedClient(profile, *provider)
		if err != nil {
			return err
		}

		models, err := client.Models(ctx)
		if err != nil {
			return err
		}

		return encode(models)
	case "use":
		client, err := ConnectedClient(profile, *provider)
		if err != nil {
			return err
		}

		models, err := client.Models(ctx)
		if err != nil {
			return err
		}

		if !containsModel(models, *model) {
			return errors.New("model is not available from this provider")
		}

		if err := Select(profile, client.provider, *model); err != nil {
			return err
		}

		return encode(map[string]string{"provider": client.provider, "model": *model})
	case "remove":
		if err := Disconnect(profile, *provider); err != nil {
			return err
		}

		return encode(map[string]string{"status": "disconnected", "provider": *provider})
	default:
		return errors.New("unknown provider command")
	}
}

func containsModel(models []Model, id string) bool {
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}

	return false
}

func ChatCommand(ctx context.Context, profile string, args []string, in io.Reader, out, log io.Writer) error {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	flags.SetOutput(log)
	provider := flags.String("provider", "", "connected provider; defaults to the active one")
	prompt := flags.String("prompt", "", "one agent prompt; omit for interactive chat")

	directory := flags.String("dir", ".", "project directory for local file tools")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return err
	}

	if flags.NArg() != 0 {
		return errors.New("unexpected chat arguments")
	}

	client, err := ConnectedClient(profile, *provider)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(in)
	approve := func(ctx context.Context, change Approval) (bool, error) {
		file, ok := in.(*os.File)
		if !ok || !term.IsTerminal(file.Fd()) {
			return false, nil
		}

		fmt.Fprintf(log, "\nProposed file: %s\n%s\nApply this edit? [y/N] ",
			ansi.Strip(change.Path), ansi.Strip(change.Content))

		answer, err := reader.ReadString('\n')
		if err != nil {
			return false, err
		}

		return strings.EqualFold(strings.TrimSpace(answer), "y"), ctx.Err()
	}
	emit := func(event Event) {
		if event.Type == "assistant" {
			fmt.Fprintln(out, ansi.Strip(event.Text))
		} else {
			fmt.Fprintln(log, "Agent:", ansi.Strip(event.Text))
		}
	}

	var history []Message

	if *prompt != "" {
		_, err := client.RunAgent(ctx, *directory, history, *prompt, approve, emit)
		return err
	}

	fmt.Fprintf(
		log,
		"Maruvo agent · %s / %s\nType /exit to leave. File edits ask for approval.\n",
		client.provider,
		client.model,
	)

	for {
		fmt.Fprint(log, "\nYou: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return err
		}

		line = strings.TrimSpace(line)
		if line == "/exit" {
			return nil
		}

		if line == "" {
			continue
		}

		if line == "/new" {
			history = nil
			continue
		}

		updated, err := client.RunAgent(ctx, *directory, history, line, approve, emit)
		if err != nil {
			return err
		}

		history = updated
	}
}
