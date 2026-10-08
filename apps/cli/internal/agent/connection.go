package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

func loadConnection(apiURL, profile, account string) (auth.HarnessConnection, error) {
	var connection auth.HarnessConnection
	if err := auth.LoadRemoteState(profile, "harness.json", &connection); err != nil {
		return connection, errors.New(
			"connect your harness first with agent connect --file OFFER.json --exec ADAPTER --dir WORKDIR",
		)
	}

	if connection.APIURL != apiURL || connection.Account != account ||
		!filepath.IsAbs(connection.Executable) || !filepath.IsAbs(connection.Directory) {
		return connection, errors.New(
			"saved harness belongs to another API/account; reconnect with this profile",
		)
	}

	return connection, nil
}

func connectHarness(ctx context.Context, client *api.Client, token, profile, account, file,
	directory, executable string, args []string, prompt bool, out io.Writer) error {
	executable, err := exec.LookPath(executable)
	if err != nil {
		return err
	}

	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}

	directory, err = filepath.Abs(directory)
	if err != nil {
		return err
	}

	if len(args) > 32 || len(strings.Join(args, "")) > 16<<10 {
		return InvalidArgument("use at most 32 harness arguments totaling 16 KiB")
	}

	var offer *api.AgentOffer

	if file != "" {
		terms, err := loadOffer(file)
		if err != nil {
			return err
		}

		saved, err := client.SaveAgentOffer(ctx, token, terms)
		if err != nil {
			return err
		}

		offer = &saved
	}

	connection := auth.HarnessConnection{APIURL: client.URL(), Account: account,
		Executable: executable, Arguments: args, Directory: directory, Prompt: prompt}
	if err = auth.SaveRemoteState(profile, "harness.json", connection); err != nil {
		return err
	}

	next := "agent listen --timeout 24h"
	if offer != nil {
		next = "agent serve --timeout 24h"
	}

	return json.NewEncoder(out).Encode(map[string]any{"status": "configured", "offer": offer, "next": next})
}

func promptBridge(executable string, args []string) (string, []string, error) {
	cli, err := os.Executable()
	if err != nil {
		return "", nil, err
	}

	bridged := []string{"agent", "bridge", "--exec", executable}
	for _, arg := range args {
		bridged = append(bridged, "--arg="+arg)
	}

	return cli, bridged, nil
}
