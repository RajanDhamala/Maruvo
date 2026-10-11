package work

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

func newOperation() string {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(id[:])
}
func safeName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name && !strings.ContainsAny(name, "/\\")
}

type harnessInput struct {
	Artifact api.WorkArtifact `json:"artifact"`
	Path     string           `json:"path"`
}
type harnessContext struct {
	Mode            string             `json:"mode"`
	ProjectID       string             `json:"project_id"`
	Assignment      api.WorkAssignment `json:"assignment"`
	Attempt         api.WorkAttempt    `json:"attempt"`
	Inputs          []harnessInput     `json:"inputs"`
	OutputDirectory string             `json:"output_directory"`
	EvidenceFile    string             `json:"evidence_file"`
	CLI             string             `json:"cli"`
	CLIArguments    []string           `json:"cli_arguments"`
}
type harnessReport struct {
	Note     string             `json:"note"`
	Evidence []api.WorkEvidence `json:"evidence"`
	Usage    map[string]int64   `json:"usage,omitempty"`
}

func runHarness(parent context.Context, client *api.Client, token, project, assignment, operation, executable string, argv []string, directory string, timeout time.Duration, out, log io.Writer) error {
	path, err := exec.LookPath(executable)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ownerToken := token
	if !strings.HasPrefix(token, "mru_work_") {
		sum := sha256.Sum256([]byte("runtime:" + operation))
		lifetime := int64(timeout.Seconds()) + 60
		if lifetime < 60 {
			lifetime = 60
		}
		if lifetime > 604800 {
			lifetime = 604800
		}
		grant, grantErr := client.WorkOperation(ctx, token, project, map[string]any{"action": "grant", "operation_id": hex.EncodeToString(sum[:]), "name": "managed-work-runtime", "permissions": []string{"read", "execute", "upload", "message"}, "expires_in_seconds": lifetime})
		if grantErr != nil {
			return grantErr
		}
		if grant.Token == "" {
			return fmt.Errorf("runtime grant returned no token")
		}
		token = grant.Token
		var metadata struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(grant.Result, &metadata)
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, _ = client.WorkOperation(cleanup, ownerToken, project, map[string]any{"action": "revoke", "operation_id": newOperation(), "grant_id": metadata.ID})
		}()
	}
	receipt, err := client.WorkOperation(ctx, token, project, map[string]any{"action": "claim", "operation_id": operation, "assignment_id": assignment})
	if err != nil {
		return err
	}
	var attempt api.WorkAttempt
	if err = json.Unmarshal(receipt.Result, &attempt); err != nil || attempt.ID == "" {
		return fmt.Errorf("claim returned no attempt")
	}
	p, err := client.WorkProject(ctx, token, project)
	if err != nil {
		return err
	}
	var selected api.WorkAssignment
	for _, s := range p.Assignments {
		if s.ID == assignment {
			selected = s
		}
	}
	if len(selected.Attempts) == 0 || selected.State != "running" || selected.Attempts[len(selected.Attempts)-1].ID != attempt.ID || !time.Now().Before(selected.Attempts[len(selected.Attempts)-1].LeaseUntil) {
		return fmt.Errorf("attempt expired or replaced; use a new operation ID to claim again")
	}
	runDir, err := os.MkdirTemp(directory, "attempt-"+attempt.ID+"-")
	if err != nil {
		return err
	}
	mutation := func(action string, fields map[string]any) (api.WorkReceipt, error) {
		fields["action"] = action
		fields["operation_id"] = newOperation()
		fields["assignment_id"] = assignment
		fields["attempt_id"] = attempt.ID
		return client.WorkOperation(ctx, token, project, fields)
	}
	completed := false
	defer func() {
		if !completed {
			cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			_, _ = client.WorkOperation(cleanup, token, project, map[string]any{"action": "fail", "operation_id": newOperation(), "assignment_id": assignment, "attempt_id": attempt.ID, "reason": "managed harness interrupted or failed", "next_action": "inspect the attempt directory and retry"})
		}
	}()
	// The operator explicitly invokes this adapter. Assignments never start remote processes.
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				_, e := mutation("renew", map[string]any{})
				if e != nil {
					cancel()
					heartbeatDone <- e
					return
				}
			}
		}
	}()
	defer func() { stopHeartbeat(); <-heartbeatDone }()
	task := harnessContext{Mode: "work", ProjectID: project, Assignment: selected, Attempt: attempt, Inputs: []harnessInput{}, OutputDirectory: filepath.Join(runDir, "outputs"), EvidenceFile: filepath.Join(runDir, "evidence.json")}
	credentialPath := filepath.Join(runDir, "access.json")
	credential, _ := json.Marshal(map[string]string{"api_url": client.URL(), "token": token})
	if err = writeNew(credentialPath, credential); err != nil {
		return err
	}
	defer os.Remove(credentialPath)
	task.CLI, _ = os.Executable()
	task.CLIArguments = []string{"-api", client.URL(), "work"}
	for _, id := range attempt.Inputs {
		var art api.WorkArtifact
		for _, item := range p.Artifacts {
			if item.ID == id {
				art = item
			}
		}
		if art.ID == "" || !safeName(art.Name) || !safeName(art.ID) {
			return fmt.Errorf("unsafe or missing input artifact %s", id)
		}
		content, e := client.WorkDownload(ctx, token, project, id)
		if e != nil {
			return e
		}
		if e = verifyArtifact(art, content); e != nil {
			return e
		}
		inputDir := filepath.Join(runDir, "inputs", id)
		if e = os.MkdirAll(inputDir, 0700); e != nil {
			return e
		}
		target := filepath.Join(inputDir, art.Name)
		if e = writeNew(target, content); e != nil {
			return e
		}
		task.Inputs = append(task.Inputs, harnessInput{art, target})
	}
	if err = os.Mkdir(task.OutputDirectory, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}
	if err = writeNew(filepath.Join(runDir, "task.json"), data); err != nil {
		return err
	}
	if err = json.NewEncoder(out).Encode(map[string]any{"event": "attempt.started", "attempt_id": attempt.ID, "directory": runDir, "project_id": project}); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, argv...)
	configureHarness(cmd)
	cmd.Dir = runDir
	cmd.Stdin = strings.NewReader(string(data))
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.WaitDelay = 5 * time.Second
	// Work credentials are available for platform tools; payment keys are never passed by this adapter.
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if name != "MARUVO_AGENT_TOKEN_FILE" && name != "MARUVO_WALLET" && name != "MARUVO_TASK_FILE" && name != "MARUVO_WORK_ATTEMPT" {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "MARUVO_AGENT_TOKEN_FILE="+credentialPath, "MARUVO_TASK_FILE="+filepath.Join(runDir, "task.json"), "MARUVO_WORK_ATTEMPT="+attempt.ID)
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("harness failed: %w", err)
	}
	root, err := os.OpenRoot(runDir)
	if err != nil {
		return err
	}
	defer root.Close()
	reportFile, err := root.Open("evidence.json")
	if err != nil {
		return fmt.Errorf("harness must write evidence.json: %w", err)
	}
	var report harnessReport
	decoder := json.NewDecoder(io.LimitReader(reportFile, 256<<10))
	decoder.DisallowUnknownFields()
	err = decoder.Decode(&report)
	if err == nil {
		if endErr := decoder.Decode(new(any)); endErr != io.EOF {
			err = fmt.Errorf("provide one evidence JSON object")
		}
	}
	_ = reportFile.Close()
	if err != nil {
		return fmt.Errorf("invalid evidence.json: %w", err)
	}
	outputIDs := []string{}
	for _, name := range selected.ExpectedOutputs {
		if !safeName(name) {
			return fmt.Errorf("unsafe output filename")
		}
		f, e := root.Open(filepath.Join("outputs", name))
		if e != nil {
			return e
		}
		content, e := io.ReadAll(io.LimitReader(f, (10<<20)+1))
		_ = f.Close()
		if e != nil {
			return e
		}
		if len(content) == 0 || len(content) > 10<<20 {
			return fmt.Errorf("invalid output size: %s", name)
		}
		upload, e := mutation("upload", map[string]any{"artifact_name": name, "content": content})
		if e != nil {
			return e
		}
		var art api.WorkArtifact
		if e = json.Unmarshal(upload.Result, &art); e != nil || art.ID == "" {
			return fmt.Errorf("artifact upload returned no ID")
		}
		outputIDs = append(outputIDs, art.ID)
	}
	submitted, err := mutation("submit", map[string]any{"base_revision": attempt.BaseRevision, "inputs": attempt.Inputs, "outputs": outputIDs, "note": report.Note, "evidence": report.Evidence, "usage": report.Usage})
	if err != nil {
		return err
	}
	completed = true
	return json.NewEncoder(out).Encode(map[string]any{"event": "contribution.submitted", "receipt": submitted, "directory": runDir})
}

// Execute starts only a locally approved harness using the selected operator's session.
func Execute(ctx context.Context, client *api.Client, token, project, assignment, operation, executable string, argv []string, directory string, timeout time.Duration, out, log io.Writer) error {
	return runHarness(ctx, client, token, project, assignment, operation, executable, argv, directory, timeout, out, log)
}
