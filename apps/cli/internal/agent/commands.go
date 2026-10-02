package agent

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

type arguments []string

func (a *arguments) String() string         { return strings.Join(*a, " ") }
func (a *arguments) Set(value string) error { *a = append(*a, value); return nil }

func Run(ctx context.Context, client *api.Client, profile string, args []string, out, log io.Writer) error {
	if len(args) == 0 {
		return errors.New(
			"agent commands: tools, login, feed, tasks, create, task, accept, chat, message, files, send-file, upload, download, submit, events, run",
		)
	}

	action := args[0]
	flags := flag.NewFlagSet("agent "+action, flag.ContinueOnError)
	flags.SetOutput(log)
	postID := flags.Int64("post", 0, "task ID")
	level := flags.String("level", "easy", "feed difficulty")
	brief := flags.String("brief", "", "task JSON file")
	file := flags.String("file", "", "local file to upload")
	purpose := flags.String("purpose", "shared", "input, output, or shared")
	fileID := flags.String("id", "", "shared file ID")
	destination := flags.String("to", "", "new download path")
	text := flags.String("text", "", "message or delivery note")
	after := flags.Int64("after", 0, "last received event ID")
	directory := flags.String("dir", "./maruvo-work", "local task working directory")
	executable := flags.String("exec", "", "agent harness executable; receives task JSON on stdin")
	timeout := flags.Duration(
		"timeout",
		30*time.Minute,
		"runner timeout, including waiting for funding/review",
	)
	once := flags.Bool("once", false, "exit after submitting one delivery")

	var commandArgs arguments
	flags.Var(&commandArgs, "arg", "harness argument (repeatable)")

	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments; use --arg for harness arguments")
	}

	encode := json.NewEncoder(out).Encode
	if action == "tools" {
		return encode(toolList())
	}

	if action == "login" {
		token, err := auth.Login(ctx, client)
		if err != nil {
			return err
		}

		if err = auth.SaveSession(client.URL(), token, profile); err != nil {
			return err
		}

		return encode(map[string]string{"status": "signed_in", "profile": profile})
	}

	token, err := auth.LoadSession(client.URL(), profile)
	if err != nil {
		return err
	}

	if token == "" {
		return errors.New("sign in first with agent login or the terminal UI using the same profile")
	}

	user, err := client.Me(ctx, token)
	if err != nil {
		return err
	}

	switch action {
	case "feed":
		posts, err := client.Feed(ctx, token, *level)
		if err != nil {
			return err
		}

		return encode(posts)
	case "tasks":
		posts, err := client.OwnPosts(ctx, token)
		if err != nil {
			return err
		}

		return encode(posts)
	case "create":
		if *brief == "" {
			return errors.New("provide --brief with a task JSON file")
		}

		input, err := os.Open(*brief)
		if err != nil {
			return err
		}
		defer input.Close()

		var payload api.CreatePostPayload
		if err = json.NewDecoder(io.LimitReader(input, 64<<10)).Decode(&payload); err != nil {
			return err
		}

		post, err := client.CreatePost(ctx, token, payload)
		if err != nil {
			return err
		}

		return encode(post)
	}

	if *postID <= 0 {
		return errors.New("provide a positive --post task ID")
	}

	switch action {
	case "task":
		info, err := client.PostInfo(ctx, token, *postID)
		if err != nil {
			return err
		}

		if info.Post.AcceptedBy == nil {
			return encode(info)
		}

		workspace, err := client.Workspace(ctx, token, *postID)
		if err != nil {
			return err
		}

		return encode(workspace)
	case "accept":
		post, err := client.AcceptPost(ctx, token, *postID)
		if err != nil {
			return err
		}

		return encode(post)
	case "message":
		if err = client.SendMessage(ctx, token, *postID, *text); err != nil {
			return err
		}
	case "chat":
		if *after < 0 {
			return errors.New("event cursor must be nonnegative")
		}

		if *text != "" {
			if err = client.SendMessage(ctx, token, *postID, *text); err != nil {
				return err
			}

			break
		}

		workspace, err := client.Workspace(ctx, token, *postID)
		if err != nil {
			return err
		}

		messages := []api.WorkspaceEvent{}

		for _, event := range workspace.Events {
			if event.Kind == "message" && event.ID > *after {
				messages = append(messages, event)
			}
		}

		return encode(
			map[string]any{
				"post_id":       *postID,
				"last_event_id": workspace.State.LastEventID,
				"messages":      messages,
			},
		)
	case "files":
		workspace, err := client.Workspace(ctx, token, *postID)
		if err != nil {
			return err
		}

		return encode(workspace.Files)
	case "upload", "send-file":
		if *file == "" {
			return errors.New("provide --file")
		}

		shared, err := client.UploadTaskFile(ctx, token, *postID, *file, *purpose)
		if err != nil {
			return err
		}

		return encode(shared)
	case "download":
		if *fileID == "" || *destination == "" {
			return errors.New("provide --id and --to")
		}

		workspace, err := client.Workspace(ctx, token, *postID)
		if err != nil {
			return err
		}

		found := false

		for _, shared := range workspace.Files {
			if shared.ID == *fileID {
				err = client.DownloadFile(ctx, token, *postID, shared, *destination)
				found = true

				break
			}
		}

		if !found {
			return errors.New("file is not in this task workspace")
		}

		if err != nil {
			return err
		}
	case "submit":
		if err = client.SubmitWork(ctx, token, *postID, *text); err != nil {
			return err
		}
	case "events":
		if *after < 0 {
			return errors.New("event cursor must be nonnegative")
		}

		return watch(
			ctx,
			client,
			token,
			*postID,
			*after,
			func(frame api.StreamFrame) error { return encode(frame) },
		)
	case "run":
		if *executable == "" || *timeout <= 0 {
			return errors.New("provide --exec and a positive --timeout")
		}

		runCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()

		return runTask(
			runCtx,
			client,
			token,
			user,
			profile,
			*postID,
			*directory,
			*executable,
			commandArgs,
			*once,
			out,
			log,
		)
	default:
		return fmt.Errorf("unknown agent command: %s", action)
	}

	return encode(map[string]string{"status": "ok"})
}

func watch(
	ctx context.Context,
	client *api.Client,
	token string,
	postID, after int64,
	consume func(api.StreamFrame) error,
) error {
	for ctx.Err() == nil {
		stream, err := client.ConnectWorkspace(ctx, token, postID, after)
		if err == nil {
			for {
				frame, readErr := stream.Read()
				if readErr != nil {
					err = readErr
					break
				}

				if frame.Event == "workspace.event" {
					var event api.WorkspaceEvent
					if err = json.Unmarshal(frame.Data, &event); err != nil {
						break
					}

					after = event.ID
				}

				if err = consume(frame); err != nil {
					stream.Close()
					return err
				}
			}

			stream.Close()
		}

		var failure *api.Error
		if errors.As(err, &failure) &&
			(failure.StatusCode == 400 || failure.StatusCode == 401 || failure.StatusCode == 403 || failure.StatusCode == 404) {
			return err
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	return ctx.Err()
}
