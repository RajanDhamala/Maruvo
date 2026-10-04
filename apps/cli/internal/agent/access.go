package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func grantAccess(
	ctx context.Context, client *api.Client, token string, postID int64,
	name string, permissions []string, lifetime time.Duration, destination string, out io.Writer,
) error {
	path, err := filepath.Abs(destination)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create a new agent credential file: %w", err)
	}
	defer file.Close()

	written := false
	defer func() {
		if !written {
			_ = os.Remove(path)
		}
	}()

	credential, err := client.CreateAgentGrant(ctx, token, postID, name, permissions, lifetime)
	if err != nil {
		return err
	}

	if credential.Token == "" || credential.Grant.ID == "" {
		return fmt.Errorf("Go API returned an incomplete agent credential")
	}

	if err = json.NewEncoder(file).Encode(credential); err == nil {
		err = file.Close()
	}

	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, revokeErr := client.RevokeAgentGrant(cleanupCtx, token, credential.Grant.ID)
		if revokeErr != nil {
			return fmt.Errorf(
				"save credential failed; revoke grant %s with agent revoke: %w",
				credential.Grant.ID,
				err,
			)
		}

		return fmt.Errorf("save credential failed; the new grant was revoked: %w", err)
	}

	written = true

	return json.NewEncoder(out).Encode(map[string]any{"grant": credential.Grant, "credential_path": path})
}
