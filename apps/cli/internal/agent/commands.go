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
		return InvalidArgument(
			"agent commands: tools, login, link-github, identity, control, grant, grants, revoke, connect, connection, bridge, inbox, listen, activity, offer, offers, serve, feed, tasks, create, task, accept, cancel, reopen, chat, message, files, send-file, upload, download, submit, request-changes, events, history, wait, run",
		)
	}

	action := args[0]
	switch action {
	case "tools",
		"login",
		"link-github",
		"identity",
		"control",
		"grant",
		"grants",
		"revoke",
		"offer", "offers", "serve", "connect", "connection", "bridge", "inbox", "listen", "activity",
		"feed",
		"tasks",
		"create",
		"task",
		"accept",
		"cancel",
		"reopen",
		"chat",
		"message",
		"files",
		"send-file",
		"upload",
		"download",
		"submit",
		"request-changes",
		"events",
		"history",
		"wait",
		"run":
	default:
		return InvalidArgument("unknown agent command: " + action)
	}

	flags := flag.NewFlagSet("agent "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	postID := flags.Int64("post", 0, "task ID")
	level := flags.String("level", "easy", "feed difficulty")
	provider := flags.String("provider", "github", "login provider: github or google")
	brief := flags.String("brief", "", "task JSON file")
	endTime := flags.String("end-time", "", "future RFC3339 acceptance cutoff for reopening a task")
	deliverBy := flags.String("deliver-by", "", "replacement RFC3339 delivery deadline when reopening")
	file := flags.String("file", "", "local file to upload")
	purpose := flags.String("purpose", "shared", "input, output, or shared")
	fileID := flags.String("id", "", "shared file ID")
	grantID := flags.String("grant-id", "", "agent grant ID to revoke")
	agentName := flags.String("name", "", "name identifying this harness")
	controlMode := flags.String("mode", "", "task automation: manual or agent; omit to inspect")
	activityState := flags.String("state", "", "reported remote activity state")
	runID := flags.String("run-id", os.Getenv("MARUVO_RUN_ID"), "current runner lease ID")
	lifetime := flags.Duration("expires-in", time.Hour, "agent credential lifetime (1 minute to 7 days)")

	var permissions arguments
	flags.Var(
		&permissions,
		"permission",
		"agent permission: read, message, upload, submit, request-changes (repeatable)",
	)
	destination := flags.String("to", "", "new download path")
	text := flags.String("text", "", "message or delivery note")
	messageID := flags.String("message-id", "", "message ID to reuse when retrying the same chat message")
	version := flags.Int64(
		"version",
		-1,
		"current workspace submission version; required for explicit delivery and revision requests",
	)
	after := flags.Int64("after", 0, "last received event ID")
	cursor := flags.String("cursor", "", "last received Redis stream ID")
	before := flags.Int64("before", 0, "exclusive archived event ID for history; 0 starts at the newest page")
	pageLimit := flags.Int("limit", 50, "history page size (1-100)")

	var eventKinds arguments
	flags.Var(&eventKinds, "event", "event kind to wait for (repeatable); defaults to any workspace event")
	directory := flags.String("dir", "./maruvo-work", "local task working directory")
	executable := flags.String("exec", "", "agent harness executable; receives task JSON on stdin")
	prompt := flags.Bool(
		"prompt",
		false,
		"translate task JSON into a prompt for a harness that reads prompts on stdin",
	)
	maxJobs := flags.Int("max-jobs", 1, "maximum jobs for a serving session (1-20)")

	defaultTimeout := 30 * time.Minute
	if action == "wait" {
		defaultTimeout = 30 * time.Second
	}

	timeout := flags.Duration(
		"timeout",
		defaultTimeout,
		"runner or wait timeout; wait accepts up to 5 minutes",
	)
	once := flags.Bool("once", false, "exit after submitting one delivery")

	var commandArgs arguments
	flags.Var(&commandArgs, "arg", "harness argument (repeatable)")

	var deliveryFiles, inputFiles arguments
	flags.Var(&deliveryFiles, "file-id", "uploaded delivery file ID (repeatable)")
	flags.Var(
		&inputFiles,
		"input-id",
		"input file ID used for this delivery, in declared filename order (repeatable)",
	)

	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(log)
			flags.PrintDefaults()

			return nil
		}

		return InvalidArgument(err.Error())
	}

	if flags.NArg() != 0 {
		return InvalidArgument("unexpected positional arguments; use --arg for harness arguments")
	}

	if *version < -1 || ((*version < 0) && (len(deliveryFiles) > 0 || len(inputFiles) > 0)) {
		return InvalidArgument("provide a nonnegative --version with delivery file or input IDs")
	}

	if action == "request-changes" && (*version < 1 || strings.TrimSpace(*text) == "") {
		return InvalidArgument(
			"provide --version for the submitted delivery and --text explaining the requested changes",
		)
	}

	if (action == "submit" || action == "request-changes") && *postID <= 0 {
		return InvalidArgument("provide a positive --post task ID")
	}

	encode := json.NewEncoder(out).Encode

	if action == "control" && (*postID <= 0 ||
		(*controlMode != "" && *controlMode != "manual" && *controlMode != "agent")) {
		return InvalidArgument("provide a positive --post and optional --mode manual or agent")
	}

	if action == "history" && (*postID <= 0 || *before < 0 || *pageLimit < 1 || *pageLimit > 100) {
		return InvalidArgument("provide --post, nonnegative --before, and --limit between 1 and 100")
	}

	if action == "wait" && (*postID <= 0 || *after < 0 || *timeout <= 0 || *timeout > 5*time.Minute) {
		return InvalidArgument(
			"provide --post, nonnegative --after, and --timeout greater than zero and at most 5m",
		)
	}

	if action == "grant" && (*postID <= 0 || strings.TrimSpace(*agentName) == "" || *destination == "" ||
		*lifetime < time.Minute || *lifetime > 7*24*time.Hour) {
		return InvalidArgument(
			"provide --post, --name, --to for a new credential file, and --expires-in between 1m and 168h",
		)
	}

	if action == "revoke" && *grantID == "" {
		return InvalidArgument("provide --grant-id")
	}

	if action == "tools" {
		return encode(toolList())
	}

	if action == "bridge" {
		if *executable == "" {
			return InvalidArgument("provide --exec for the prompt harness")
		}

		if err := bridgeHarness(ctx, *executable, commandArgs, os.Stdin, log); err != nil {
			return err
		}

		return encode(map[string]string{"status": "handled"})
	}

	if action == "login" {
		token, err := auth.LoginWithProvider(ctx, client, *provider)
		if err != nil {
			return err
		}

		if err = auth.SaveSession(client.URL(), token, profile); err != nil {
			return err
		}

		return encode(map[string]string{"status": "signed_in", "profile": profile})
	}

	token, agentSession, err := auth.LoadAgentSession(client.URL())
	if err != nil {
		return err
	}

	if !agentSession {
		token, err = auth.LoadSession(client.URL(), profile)
		if err != nil {
			return err
		}
	}

	if token == "" {
		return &commandError{
			Code:    "UNAUTHENTICATED",
			Message: "sign in first with agent login or the terminal UI using the same profile",
		}
	}

	if action == "wait" {
		result, err := waitForTask(ctx, client, token, *postID, *after, *cursor, eventKinds, *timeout)
		if err != nil {
			return err
		}

		return encode(result)
	}

	user, err := client.Me(ctx, token)
	if err != nil {
		return err
	}

	if agentSession && (action == "offer" || action == "offers" || action == "serve" || action == "connect" ||
		action == "connection" || action == "inbox" || action == "listen" || action == "control") {
		return &commandError{
			Code:    "FORBIDDEN",
			Message: "seller discovery and serving require the owner's login",
		}
	}

	if action == "link-github" {
		linked, err := auth.LinkGitHub(ctx, client, token)
		if err != nil {
			return err
		}

		if err = auth.SaveSession(client.URL(), linked, profile); err != nil {
			return err
		}

		return encode(map[string]string{"status": "github_connected", "profile": profile})
	}

	switch action {
	case "control":
		var control api.AgentControl
		if *controlMode == "" {
			control, err = client.AgentControl(ctx, token, *postID)
		} else {
			control, err = client.SetAgentControl(ctx, token, *postID, *controlMode)
		}

		if err != nil {
			return err
		}

		return encode(control)
	case "connect":
		if *executable == "" {
			return InvalidArgument(
				"provide --exec with your harness; --file optionally publishes seller terms",
			)
		}

		return connectHarness(
			ctx,
			client,
			token,
			profile,
			user.ID,
			*file,
			*directory,
			*executable,
			commandArgs,
			*prompt,
			out,
		)
	case "connection":
		connection, err := loadConnection(client.URL(), profile, user.ID)
		if err != nil {
			return err
		}

		return encode(map[string]any{"executable": connection.Executable, "directory": connection.Directory,
			"argument_count": len(connection.Arguments), "status": "configured"})
	case "inbox":
		posts, err := client.RemoteInbox(ctx, token)
		if err != nil {
			return err
		}

		return encode(posts)
	case "listen", "serve":
		if *executable == "" {
			connection, err := loadConnection(client.URL(), profile, user.ID)
			if err != nil {
				return err
			}

			*executable, commandArgs, *prompt = connection.Executable, connection.Arguments, connection.Prompt
			explicitDir := false

			flags.Visit(func(f *flag.Flag) {
				if f.Name == "dir" {
					explicitDir = true
				}
			})

			if !explicitDir {
				*directory = connection.Directory
			}
		}

		if *prompt {
			*executable, commandArgs, err = promptBridge(*executable, commandArgs)
			if err != nil {
				return err
			}
		}

		if *timeout <= 0 {
			return InvalidArgument("provide a positive --timeout")
		}

		sessionCtx, stop := context.WithTimeout(ctx, *timeout)
		defer stop()

		if action == "listen" {
			return listenInbox(
				sessionCtx,
				client,
				token,
				profile,
				user.ID,
				*directory,
				*executable,
				commandArgs,
				*once,
				out,
				log,
			)
		}

		return serveAgent(
			sessionCtx,
			client,
			token,
			profile,
			user,
			*directory,
			*executable,
			commandArgs,
			*maxJobs,
			*once,
			out,
			log,
		)
	case "offer":
		var offer api.AgentOffer
		if *file == "" {
			offer, err = client.OwnAgentOffer(ctx, token)
		} else {
			terms, loadErr := loadOffer(*file)
			if loadErr != nil {
				return loadErr
			}

			offer, err = client.SaveAgentOffer(ctx, token, terms)
		}

		if err != nil {
			return err
		}

		return encode(offer)
	case "offers":
		offers, err := client.AgentOffers(ctx, token)
		if err != nil {
			return err
		}

		return encode(offers)
	case "history":
		page, err := client.History(ctx, token, *postID, *before, *pageLimit)
		if err != nil {
			return err
		}

		return encode(page)
	case "identity":
		return encode(user)
	case "grant":
		return grantAccess(ctx, client, token, *postID, *agentName,
			append([]string{"read"}, permissions...), *lifetime, *destination, out)
	case "grants":
		if *postID <= 0 {
			return InvalidArgument("provide a positive --post task ID")
		}

		grants, err := client.AgentGrants(ctx, token, *postID)
		if err != nil {
			return err
		}

		return encode(grants)
	case "revoke":
		grant, err := client.RevokeAgentGrant(ctx, token, *grantID)
		if err != nil {
			return err
		}

		return encode(grant)
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
			return InvalidArgument("provide --brief with a task JSON file")
		}

		input, err := os.Open(*brief)
		if err != nil {
			return err
		}
		defer input.Close()

		var payload api.CreatePostPayload

		decoder := json.NewDecoder(io.LimitReader(input, 64<<10))
		decoder.DisallowUnknownFields()

		if err = decoder.Decode(&payload); err != nil {
			return InvalidArgument("invalid task JSON: " + err.Error())
		}

		post, err := client.CreatePost(ctx, token, payload)
		if err != nil {
			return err
		}

		return encode(post)
	}

	if *postID <= 0 {
		return InvalidArgument("provide a positive --post task ID")
	}

	switch action {
	case "activity":
		if len(*runID) < 32 {
			return InvalidArgument("activity requires the current --run-id or MARUVO_RUN_ID")
		}

		if err := client.ReportActivity(ctx, token, *postID, *runID, *activityState, *text); err != nil {
			return err
		}

		return encode(map[string]string{"status": "reported"})
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
	case "cancel", "reopen":
		var deadline time.Time
		if *endTime != "" {
			deadline, err = time.Parse(time.RFC3339, *endTime)
			if err != nil {
				return InvalidArgument("provide --end-time in RFC3339 format")
			}
		}

		var delivery time.Time
		if *deliverBy != "" {
			delivery, err = time.Parse(time.RFC3339, *deliverBy)
			if err != nil {
				return InvalidArgument("provide --deliver-by in RFC3339 format")
			}
		}

		post, err := client.RecoverPost(ctx, token, *postID, action, deadline, delivery)
		if err != nil {
			return err
		}

		return encode(post)
	case "message":
		receipt, err := client.SendMessageReceipt(ctx, token, *postID, *text, *messageID)
		if err != nil {
			return err
		}

		return encode(receipt)
	case "chat":
		if *after < 0 {
			return InvalidArgument("event cursor must be nonnegative")
		}

		if *text != "" {
			receipt, err := client.SendMessageReceipt(ctx, token, *postID, *text, *messageID)
			if err != nil {
				return err
			}

			return encode(receipt)
		}

		workspace, err := client.Workspace(ctx, token, *postID)
		if err != nil {
			return err
		}

		messages := []api.WorkspaceEvent{}

		for _, event := range workspace.Events {
			if event.Kind == "message" &&
				(event.ID == 0 || event.ID > *after) &&
				(*cursor == "" || api.StreamCursorAfter(event.StreamID, *cursor)) {
				messages = append(messages, event)
			}
		}

		return encode(
			map[string]any{
				"post_id":       *postID,
				"last_event_id": workspace.State.LastEventID,
				"cursor":        workspace.Cursor,
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
			return InvalidArgument("provide --file")
		}

		shared, err := client.UploadTaskFile(ctx, token, *postID, *file, *purpose)
		if err != nil {
			return err
		}

		return encode(shared)
	case "download":
		if *fileID == "" || *destination == "" {
			return InvalidArgument("provide --id and --to")
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
			return &commandError{Code: "NOT_FOUND", Message: "file is not in this task workspace"}
		}

		if err != nil {
			return err
		}
	case "submit":
		var receipt api.WorkspaceReceipt
		if *version >= 0 {
			receipt, err = client.SubmitDeliveryReceipt(
				ctx,
				token,
				*postID,
				*version,
				*text,
				deliveryFiles,
				inputFiles,
			)
		} else {
			receipt, err = client.SubmitWorkReceipt(ctx, token, *postID, *text)
		}

		if err != nil {
			return err
		}

		return encode(receipt)
	case "request-changes":
		receipt, err := client.RequestChangesReceipt(ctx, token, *postID, *version, *text)
		if err != nil {
			return err
		}

		return encode(receipt)
	case "events":
		if *after < 0 {
			return InvalidArgument("event cursor must be nonnegative")
		}

		return watch(
			ctx,
			client,
			token,
			*postID,
			*after,
			*cursor,
			func(frame api.StreamFrame) error { return encode(frame) },
		)
	case "run":
		if *executable == "" || *timeout <= 0 {
			return InvalidArgument("provide --exec and a positive --timeout")
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
	cursor string,
	consume func(api.StreamFrame) error,
) error {
	for ctx.Err() == nil {
		stream, err := client.ConnectWorkspace(ctx, token, postID, after, cursor)
		if err == nil {
			for {
				frame, readErr := stream.Read()
				if readErr != nil {
					err = readErr
					break
				}

				if err = consume(frame); err != nil {
					stream.Close()
					return err
				}

				if frame.Event == "workspace.event" {
					var event api.WorkspaceEvent
					if err = json.Unmarshal(frame.Data, &event); err != nil {
						break
					}

					after = max(after, event.ID)
					cursor = event.StreamID
				}
			}

			stream.Close()
		}

		var failure *api.Error
		if errors.As(err, &failure) && failure.StatusCode == 409 {
			workspace, reloadErr := client.Workspace(ctx, token, postID)
			if reloadErr != nil {
				return reloadErr
			}

			after, cursor = workspace.State.LastEventID, workspace.Cursor

			continue
		}

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
