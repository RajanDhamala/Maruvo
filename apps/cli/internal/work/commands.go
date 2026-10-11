package work

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/agent"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

var Actions = []string{"create", "invite", "join", "remove", "plan", "assign", "claim", "renew", "block", "fail", "submit", "accept", "retry", "cancel", "message", "upload", "import", "grant", "revoke", "control", "integrate", "delegate", "collect", "deliver"}

func loadToken(client *api.Client, profile string) (string, error) {
	token, set, err := auth.LoadAgentSession(client.URL())
	if err != nil {
		return "", err
	}
	if !set {
		token, err = auth.LoadSession(client.URL(), profile)
	}
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", &api.Error{StatusCode: 401, Message: "sign in using agent login or supply MARUVO_AGENT_TOKEN_FILE"}
	}
	return token, nil
}
func Run(ctx context.Context, client *api.Client, profile string, args []string, out, log io.Writer) error {
	if len(args) == 0 {
		return agent.InvalidArgument("work commands: tools, list, show, assignment, handoff, events, download, run, " + strings.Join(Actions, ", "))
	}
	action := args[0]
	if action == "tools" {
		if len(args) != 1 {
			return agent.InvalidArgument("work tools takes no arguments")
		}
		return json.NewEncoder(out).Encode(Catalog())
	}
	flags := flag.NewFlagSet("work "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	project := flags.String("project", "", "private project UUID")
	file := flags.String("file", "", "request JSON, or binary content for upload")
	operation := flags.String("operation-id", "", "stable unique ID for retrying this mutation")
	assignment := flags.String("assignment", "", "assignment ID")
	user := flags.Int64("user", 0, "Maruvo account ID for invite, assign, or remove")
	attempt := flags.String("attempt", "", "execution attempt ID")
	id := flags.String("id", "", "artifact or contribution ID")
	grantID := flags.String("grant-id", "", "project credential grant ID to revoke")
	destination := flags.String("to", "", "new download or credential path")
	text := flags.String("text", "", "message, delivery note, or blocker reason")
	cursor := flags.Int64("cursor", 0, "durable event cursor")
	wait := flags.Int("wait", 0, "bounded event wait, 0 through 5 seconds")
	executable := flags.String("exec", "", "operator-approved harness executable")
	directory := flags.String("dir", "", "isolated attempt parent directory")
	timeout := flags.Duration("timeout", 30*time.Minute, "execution timeout")
	var argv stringFlags
	flags.Var(&argv, "arg", "repeatable executable argument")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(log)
			flags.PrintDefaults()
			return nil
		}
		return agent.InvalidArgument(err.Error())
	}
	if flags.NArg() != 0 {
		return agent.InvalidArgument("unexpected positional arguments")
	}
	known := action == "list" || action == "show" || action == "assignment" || action == "handoff" || action == "events" || action == "download" || action == "run"
	for _, name := range Actions {
		known = known || action == name
	}
	if !known {
		return agent.InvalidArgument("unknown work command: " + action)
	}
	if action != "list" && action != "create" && *project == "" {
		return agent.InvalidArgument("--project is required")
	}
	if (action == "assignment" || action == "handoff") && *assignment == "" {
		return agent.InvalidArgument("--assignment is required")
	}
	userSet := false
	flags.Visit(func(f *flag.Flag) { userSet = userSet || f.Name == "user" })
	if userSet && (*user <= 0 || (action != "invite" && action != "assign" && action != "remove")) {
		return agent.InvalidArgument("--user must be a positive account ID for invite, assign, or remove")
	}
	token, err := loadToken(client, profile)
	if err != nil {
		return err
	}
	encode := json.NewEncoder(out).Encode
	switch action {
	case "list":
		p, err := client.WorkProjects(ctx, token)
		if err != nil {
			return err
		}
		return encode(map[string]any{"projects": p})
	case "show":
		p, err := client.WorkProject(ctx, token, *project)
		if err != nil {
			return err
		}
		return encode(map[string]any{"project": p})
	case "assignment", "handoff":
		p, err := client.WorkProject(ctx, token, *project)
		if err != nil {
			return err
		}
		details, err := AssignmentContext(p, *assignment)
		if err != nil {
			return err
		}
		if action == "handoff" {
			return encode(map[string]any{"project_id": p.ID, "assignment_id": *assignment, "assignee": details.Assignment.Assignee, "prompt": HandoffPrompt(client.URL(), p.ID, details.Assignment)})
		}
		return encode(details)
	case "events":
		if *cursor < 0 || *wait < 0 || *wait > 5 {
			return agent.InvalidArgument("cursor must be nonnegative; --wait is 0 through 5 seconds")
		}
		batch, err := client.WorkEvents(ctx, token, *project, *cursor, *wait)
		if err != nil {
			return err
		}
		return encode(batch)
	case "download":
		if *id == "" || *destination == "" {
			return agent.InvalidArgument("--id and --to are required")
		}
		p, err := client.WorkProject(ctx, token, *project)
		if err != nil {
			return err
		}
		var expected *api.WorkArtifact
		for i := range p.Artifacts {
			if p.Artifacts[i].ID == *id {
				expected = &p.Artifacts[i]
			}
		}
		if expected == nil {
			return &api.Error{StatusCode: 403, Message: "artifact was not shared with this assignment"}
		}
		content, err := client.WorkDownload(ctx, token, *project, *id)
		if err != nil {
			return err
		}
		if err = verifyArtifact(*expected, content); err != nil {
			return err
		}
		if err = writeNew(*destination, content); err != nil {
			return err
		}
		return encode(map[string]any{"artifact": expected, "path": *destination})
	case "run":
		if *operation == "" || *executable == "" || *directory == "" || *assignment == "" || *timeout <= 0 {
			return agent.InvalidArgument("run requires --operation-id, --assignment, --exec, --dir, and positive --timeout")
		}
		return runHarness(ctx, client, token, *project, *assignment, *operation, *executable, argv, *directory, *timeout, out, log)
	}
	request := map[string]any{}
	if *file != "" && action != "upload" {
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		decoder := json.NewDecoder(io.LimitReader(f, 15<<20))
		if err = decoder.Decode(&request); err != nil {
			return agent.InvalidArgument("invalid request JSON: " + err.Error())
		}
		if request == nil {
			return agent.InvalidArgument("request JSON must be an object")
		}
		if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return agent.InvalidArgument("provide one JSON object")
		}
	}
	request["action"] = action
	if *operation != "" {
		request["operation_id"] = *operation
	}
	if request["operation_id"] == nil || request["operation_id"] == "" {
		return agent.InvalidArgument("--operation-id or operation_id in the request is required; reuse it only for the identical mutation")
	}
	if *assignment != "" {
		request["assignment_id"] = *assignment
	}
	if userSet {
		request["user_id"] = *user
	}
	if *attempt != "" {
		request["attempt_id"] = *attempt
	}
	if *id != "" {
		request["contribution_id"] = *id
	}
	if *grantID != "" {
		request["grant_id"] = *grantID
	}
	if *text != "" {
		if action == "block" || action == "fail" {
			request["reason"] = *text
		} else {
			request["note"] = *text
		}
	}
	if action == "upload" {
		if *file == "" {
			return agent.InvalidArgument("--file is required for upload")
		}
		f, err := os.Open(*file)
		if err != nil {
			return err
		}
		content, err := io.ReadAll(io.LimitReader(f, (10<<20)+1))
		_ = f.Close()
		if err != nil {
			return err
		}
		if len(content) == 0 || len(content) > 10<<20 {
			return agent.InvalidArgument("artifact must be 1 byte through 10 MiB")
		}
		request["artifact_name"] = filepath.Base(*file)
		request["content"] = content
	}
	if action == "grant" {
		if *destination == "" {
			return agent.InvalidArgument("grant requires --to for a new private credential file")
		}
		credential, err := os.OpenFile(*destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		defer credential.Close()
		saved := false
		defer func() {
			if !saved {
				_ = os.Remove(*destination)
			}
		}()
		receipt, err := client.WorkOperation(ctx, token, *project, request)
		if err != nil {
			return err
		}
		if receipt.Token == "" {
			return fmt.Errorf("API did not issue a credential")
		}
		data, _ := json.Marshal(map[string]any{"api_url": client.URL(), "token": receipt.Token, "grant": receipt.Result})
		if _, err = credential.Write(data); err == nil {
			err = credential.Close()
		}
		if err != nil {
			var grant struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(receipt.Result, &grant)
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, revokeErr := client.WorkOperation(cleanup, token, *project, map[string]any{"action": "revoke", "grant_id": grant.ID, "operation_id": newOperation()})
			if revokeErr != nil {
				return fmt.Errorf("credential save failed; revoke grant %s: %w", grant.ID, err)
			}
			return err
		}
		saved = true
		receipt.Token = ""
		return encode(map[string]any{"receipt": receipt, "credential_path": *destination})
	}
	receipt, err := client.WorkOperation(ctx, token, *project, request)
	if err != nil {
		return err
	}
	return encode(receipt)
}

type stringFlags []string

func (s *stringFlags) String() string     { return strings.Join(*s, " ") }
func (s *stringFlags) Set(v string) error { *s = append(*s, v); return nil }
func verifyArtifact(art api.WorkArtifact, data []byte) error {
	sum := sha256.Sum256(data)
	if int64(len(data)) != art.Size || hex.EncodeToString(sum[:]) != art.SHA256 {
		return fmt.Errorf("artifact integrity check failed: %s", art.ID)
	}
	return nil
}
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
