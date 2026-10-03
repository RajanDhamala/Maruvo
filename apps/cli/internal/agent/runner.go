package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type artifact struct {
	File api.WorkspaceFile `json:"file"`
	Path string            `json:"path"`
}

type taskContext struct {
	CLI                string     `json:"cli"`
	CLIArguments       []string   `json:"cli_arguments"`
	Instructions       string     `json:"instructions"`
	Task               api.Post   `json:"task"`
	SubmissionVersion  int64      `json:"submission_version"`
	ReviewNote         string     `json:"review_note"`
	PreviousSubmission string     `json:"previous_submission"`
	Inputs             []artifact `json:"inputs"`
	PreviousDelivery   []artifact `json:"previous_delivery"`
	OutputDirectory    string     `json:"output_directory"`
}

var workReady = errors.New("task ready")

func safeName(name string) bool {
	return name != "" && name != "." && name != ".." && len(name) <= 180 && utf8.ValidString(name) &&
		strings.TrimSpace(name) == name &&
		!strings.ContainsAny(name, "/\\") &&
		strings.IndexFunc(name, unicode.IsControl) < 0
}

func taskInputs(workspace api.Workspace) ([]api.WorkspaceFile, bool) {
	inputs := []api.WorkspaceFile{}

	names := workspace.Post.InputFiles
	if len(names) == 0 {
		seen := map[string]bool{}
		for _, file := range workspace.Files {
			if file.UploadedBy == workspace.Post.UserID &&
				(file.Purpose == "input" || file.Purpose == "shared") && !seen[file.Name] {
				names = append(names, file.Name)
				seen[file.Name] = true
			}
		}

		sort.Strings(names)
	}

	for _, name := range names {
		var found api.WorkspaceFile
		for _, file := range workspace.Files {
			if file.Name == name && file.UploadedBy == workspace.Post.UserID &&
				(file.Purpose == "input" || file.Purpose == "shared") &&
				(found.ID == "" || !file.CreatedAt.Before(found.CreatedAt)) {
				found = file
			}
		}

		if found.ID == "" {
			return nil, false
		}

		inputs = append(inputs, found)
	}

	return inputs, true
}

func awaitTask(
	ctx context.Context,
	client *api.Client,
	token string,
	userID, postID int64,
) (api.Workspace, error) {
	var ready api.Workspace

	check := func() error {
		workspace, err := client.Workspace(ctx, token, postID)
		if err != nil {
			return err
		}

		if workspace.Post.AcceptedBy == nil || *workspace.Post.AcceptedBy != userID {
			return errors.New("only the accepted worker can run this task")
		}

		if workspace.Post.Status == "completed" || workspace.Post.Status == "cancelled" {
			ready = workspace
			return workReady
		}

		if strings.TrimSpace(workspace.Post.Description) == "" {
			return errors.New(
				"task needs a description before using an agent runner",
			)
		}

		for _, name := range append(append([]string{}, workspace.Post.InputFiles...), workspace.Post.ExpectedOutputs...) {
			if !safeName(name) {
				return errors.New("task contains an unsafe filename")
			}
		}

		_, inputsReady := taskInputs(workspace)
		if workspace.Escrow.State == "confirmed" && workspace.Post.Status == "in_progress" &&
			(workspace.State.ReviewState == "working" || workspace.State.ReviewState == "changes_requested") &&
			inputsReady {
			ready = workspace
			return workReady
		}

		ready = workspace

		return nil
	}
	if err := check(); err != nil {
		if err == workReady {
			return ready, nil
		}

		return ready, err
	}

	err := watch(
		ctx,
		client,
		token,
		postID,
		ready.State.LastEventID,
		ready.Cursor,
		func(frame api.StreamFrame) error {
			if frame.Event == "workspace.event" || frame.Event == "connected" {
				return check()
			}

			return nil
		},
	)
	if err == workReady {
		return ready, nil
	}

	return ready, err
}

func runTask(
	ctx context.Context,
	client *api.Client,
	token string,
	user api.User,
	profile string,
	postID int64,
	directory, executable string,
	args []string,
	once bool,
	out, log io.Writer,
) error {
	userID, err := strconv.ParseInt(user.ID, 10, 64)
	if err != nil {
		return err
	}

	executable, err = exec.LookPath(executable)
	if err != nil {
		return fmt.Errorf("find agent harness: %w", err)
	}

	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}

	directory, err = filepath.Abs(directory)
	if err != nil {
		return err
	}

	encode := json.NewEncoder(out).Encode
	for {
		if err = encode(map[string]any{"event": "runner.waiting", "post_id": postID}); err != nil {
			return err
		}

		workspace, err := awaitTask(ctx, client, token, userID, postID)
		if err != nil {
			return err
		}

		if workspace.Post.Status == "completed" || workspace.Post.Status == "cancelled" {
			return encode(
				map[string]any{
					"event":   "runner.completed",
					"post_id": postID,
					"state":   workspace.State.ReviewState,
				},
			)
		}

		if err = executeTask(
			ctx,
			client,
			token,
			profile,
			workspace,
			directory,
			executable,
			args,
			out,
			log,
		); err != nil {
			return err
		}

		if once {
			return nil
		}
	}
}

func executeTask(
	ctx context.Context,
	client *api.Client,
	token, profile string,
	workspace api.Workspace,
	directory, executable string,
	args []string,
	out, log io.Writer,
) error {
	postID, version := workspace.Post.ID, workspace.State.SubmissionVersion

	parent := filepath.Join(directory, strconv.FormatInt(postID, 10))
	if err := os.MkdirAll(parent, 0700); err != nil {
		return err
	}

	runDir, err := os.MkdirTemp(parent, fmt.Sprintf("delivery-%d-", version+1))
	if err != nil {
		return err
	}

	for _, name := range []string{"input", "output", "previous"} {
		if err = os.Mkdir(filepath.Join(runDir, name), 0700); err != nil {
			return err
		}
	}

	cli, err := os.Executable()
	if err != nil {
		return err
	}

	task := taskContext{
		CLI:                cli,
		CLIArguments:       []string{"-api", client.URL(), "-profile", profile, "agent"},
		Instructions:       "Complete task.description using the provided inputs. Follow task.acceptance_criteria and review_note. Write each task.expected_outputs filename into output_directory. Exit nonzero if the task cannot be completed. Maruvo will upload only the declared outputs and submit them for human review. To communicate or share files, invoke cli with cli_arguments followed by chat, files, send-file, or download; tools lists the available commands.",
		Task:               workspace.Post,
		SubmissionVersion:  version,
		ReviewNote:         workspace.State.ReviewNote,
		PreviousSubmission: workspace.State.Submission,
		Inputs:             []artifact{},
		PreviousDelivery:   []artifact{},
		OutputDirectory:    filepath.Join(runDir, "output"),
	}
	if len(workspace.Post.ExpectedOutputs) == 0 {
		task.Instructions = "Complete task.description using the provided inputs. Follow task.acceptance_criteria (the expected result) and review_note. Write the result and a delivery summary as UTF-8 text in output_directory/result.txt, at most 4,000 characters and 16,000 bytes. Maruvo submits this text for human review. Additional files are shared only through explicit send-file tool calls; other output files and logs are not uploaded automatically. Exit nonzero if the task cannot be completed. Invoke cli with cli_arguments followed by chat, files, send-file, or download; tools lists the commands."
	}

	download := func(file api.WorkspaceFile, folder string) (artifact, error) {
		if !safeName(file.Name) {
			return artifact{}, errors.New("unsafe shared filename")
		}

		path := filepath.Join(runDir, folder, file.Name)
		err := client.DownloadFile(ctx, token, postID, file, path)

		return artifact{File: file, Path: path}, err
	}

	inputs, _ := taskInputs(workspace)
	for _, file := range inputs {
		input, err := download(file, "input")
		if err != nil {
			return err
		}

		task.Inputs = append(task.Inputs, input)
	}

	for _, id := range workspace.State.DeliveryFiles {
		for _, file := range workspace.Files {
			if file.ID == id {
				previous, err := download(file, "previous")
				if err != nil {
					return err
				}

				task.PreviousDelivery = append(task.PreviousDelivery, previous)

				break
			}
		}
	}

	contextJSON, err := json.Marshal(task)
	if err != nil {
		return err
	}

	contextPath := filepath.Join(runDir, "task.json")
	if err = os.WriteFile(contextPath, contextJSON, 0600); err != nil {
		return err
	}

	if err = client.SendMessage(
		ctx,
		token,
		postID,
		fmt.Sprintf("Agent started delivery version %d.", version+1),
	); err != nil {
		return err
	}

	if err = json.NewEncoder(out).
		Encode(map[string]any{"event": "runner.started", "post_id": postID, "version": version + 1, "directory": runDir}); err != nil {
		return err
	}

	command := exec.CommandContext(ctx, executable, args...)
	command.Dir, command.Stdin, command.Stdout, command.Stderr = runDir, strings.NewReader(
		string(contextJSON),
	), log, log
	command.WaitDelay = 5 * time.Second

	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if name != "MARUVO_TOKEN" && name != "MARUVO_WALLET" && name != "JWT_TOKEN" &&
			name != "DATABASE_URL" {
			command.Env = append(command.Env, value)
		}
	}

	command.Env = append(command.Env, "MARUVO_TASK_FILE="+contextPath)
	if err = command.Run(); err != nil {
		return fmt.Errorf("agent harness failed; delivery was not submitted: %w", err)
	}

	current, err := client.Workspace(ctx, token, postID)
	if err != nil {
		return err
	}

	if current.Escrow.State != "confirmed" || current.Post.Status != "in_progress" ||
		current.State.SubmissionVersion != version ||
		current.State.ReviewState != workspace.State.ReviewState {
		return errors.New("task changed while the agent ran; review its current state before retrying")
	}

	currentInputs, ready := taskInputs(current)
	if !ready || len(currentInputs) != len(inputs) {
		return errors.New("task inputs changed while the agent ran")
	}

	for i, file := range currentInputs {
		if file.ID != inputs[i].ID {
			return errors.New("task inputs changed while the agent ran")
		}
	}

	root, err := os.OpenRoot(runDir)
	if err != nil {
		return err
	}
	defer root.Close()

	note := "Agent delivery: " + strings.Join(workspace.Post.ExpectedOutputs, ", ")
	if len(workspace.Post.ExpectedOutputs) == 0 {
		content, err := readOutput(root, "result.txt")
		if err != nil {
			return err
		}

		note = strings.TrimSpace(string(content))
		if note == "" || !utf8.ValidString(note) || utf8.RuneCountInString(note) > 4000 ||
			len(content) > 16000 ||
			strings.IndexFunc(note, func(r rune) bool {
				return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t'
			}) >= 0 {
			return errors.New("result.txt must contain UTF-8 text, at most 4,000 characters and 16,000 bytes")
		}
	}

	outputs := []api.WorkspaceFile{}

	for _, name := range workspace.Post.ExpectedOutputs {
		content, err := readOutput(root, name)
		if err != nil {
			return err
		}

		shared, err := client.UploadBytes(ctx, token, postID, name, "output", content)
		if err != nil {
			return err
		}

		outputs = append(outputs, shared)
	}

	ids := []string{}
	for _, file := range outputs {
		ids = append(ids, file.ID)
	}

	inputIDs := []string{}
	for _, file := range inputs {
		inputIDs = append(inputIDs, file.ID)
	}

	if err = client.SubmitDelivery(ctx, token, postID, version, note, ids, inputIDs); err != nil {
		return err
	}

	return json.NewEncoder(out).
		Encode(map[string]any{"event": "runner.submitted", "post_id": postID, "version": version + 1, "files": outputs})
}

func readOutput(root *os.Root, name string) ([]byte, error) {
	if !safeName(name) {
		return nil, errors.New("unsafe output filename")
	}

	path := "output/" + name

	info, err := root.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("missing expected output %s: %w", name, err)
	}

	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 10<<20 {
		return nil, fmt.Errorf("output %s must be a regular file between 1 byte and 10 MiB", name)
	}

	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return io.ReadAll(io.LimitReader(file, (10<<20)+1))
}
