package controller

import (
	"context"
	"slices"
	"sort"

	db "github.com/rajandhamala/Maruvo/db/sqlc"
)

type workspaceContext struct {
	Role                 string            `json:"role"`
	Terminal             bool              `json:"terminal"`
	SubmissionVersion    int64             `json:"submission_version"`
	AllowedActions       []string          `json:"allowed_actions"`
	BlockedActions       map[string]string `json:"blocked_actions"`
	UploadPurposes       []string          `json:"upload_purposes"`
	WaitingFor           []string          `json:"waiting_for"`
	InputFileIDs         []string          `json:"input_file_ids"`
	MissingInputs        []string          `json:"missing_inputs"`
	MissingOutputs       []string          `json:"missing_outputs"`
	HumanPaymentRequired bool              `json:"human_payment_required"`
}

func workspaceInputs(post db.Post, files []db.ListWorkspaceFilesRow) ([]string, []string) {
	latest := map[string]string{}

	for _, file := range files {
		if file.UploadedBy == post.UserID && (file.Purpose == "input" || file.Purpose == "shared") {
			latest[file.Name] = file.ID.String()
		}
	}

	names := slices.Clone(post.InputFiles)
	if len(names) == 0 {
		for name := range latest {
			names = append(names, name)
		}

		sort.Strings(names)
	}

	ids, missing := []string{}, []string{}

	for _, name := range names {
		if id := latest[name]; id != "" {
			ids = append(ids, id)
		} else {
			missing = append(missing, name)
		}
	}

	return ids, missing
}

func taskWorkspaceContext(
	ctx context.Context, post db.Post, state db.PostWorkspace, files []db.ListWorkspaceFilesRow,
	escrow fundingView, settlement settlementView, userID int64, reviewer bool,
) workspaceContext {
	result := workspaceContext{
		Role: "reviewer", SubmissionVersion: state.SubmissionVersion,
		AllowedActions: []string{"task", "chat", "files", "download", "history", "events", "wait"},
		BlockedActions: map[string]string{}, UploadPurposes: []string{}, WaitingFor: []string{},
		MissingOutputs: []string{},
	}
	result.InputFileIDs, result.MissingInputs = workspaceInputs(post, files)

	worker := post.AcceptedBy.Valid && post.AcceptedBy.Int64 == userID
	if post.UserID == userID {
		result.Role = "requester"
	} else if worker {
		result.Role = "worker"
	}

	result.Terminal = post.Status == db.PostStatusCompleted || post.Status == db.PostStatusCancelled ||
		escrow.State == "released" || escrow.State == "refunded"
	permitted := func(permission string) bool {
		grant := requestAgent(ctx)
		return grant == nil || slices.Contains(grant.Permissions, permission)
	}
	allow := func(action, permission, blocked string) {
		if result.Terminal {
			blocked = "task_closed"
		} else if !permitted(permission) {
			blocked = "permission_required"
		}

		if blocked != "" {
			result.BlockedActions[action] = blocked
		} else {
			result.AllowedActions = append(result.AllowedActions, action)
		}
	}
	allow("message", "message", "")
	allow("upload", "upload", "")

	funded := escrow.State == "confirmed" && post.Status == db.PostStatusInProgress

	if !result.Terminal && permitted("upload") {
		result.UploadPurposes = append(result.UploadPurposes, "shared")
		if post.UserID == userID {
			result.UploadPurposes = append(result.UploadPurposes, "input")
		}

		if worker && funded {
			result.UploadPurposes = append(result.UploadPurposes, "output")
		}
	}

	working := state.ReviewState == "working" || state.ReviewState == "changes_requested"
	if worker && working {
		outputs := map[string]bool{}

		for _, file := range files {
			if file.UploadedBy == userID && (file.Purpose == "output" || file.Purpose == "shared") &&
				(!state.SubmittedAt.Valid || file.CreatedAt.Time.After(state.SubmittedAt.Time)) {
				outputs[file.Name] = true
			}
		}

		for _, name := range post.ExpectedOutputs {
			if !outputs[name] {
				result.MissingOutputs = append(result.MissingOutputs, name)
			}
		}
	}

	activeSettlement := settlement.State != "" && settlement.State != "failed" &&
		settlement.State != "expired"
	submitBlock := ""

	switch {
	case !worker:
		submitBlock = "worker_required"
	case !funded:
		submitBlock = "funding"
	case activeSettlement:
		submitBlock = "settlement"
	case !working:
		submitBlock = "review"
	case len(result.MissingInputs) > 0:
		submitBlock = "inputs"
	case len(result.MissingOutputs) > 0:
		submitBlock = "outputs"
	}

	allow("submit", "submit", submitBlock)

	reviewBlock := ""

	switch {
	case !reviewer:
		reviewBlock = "reviewer_required"
	case state.ReviewState != "submitted" || state.SubmissionVersion < 1:
		reviewBlock = "delivery"
	case !funded:
		reviewBlock = "funding"
	case activeSettlement:
		reviewBlock = "settlement"
	}

	allow("request-changes", "request-changes", reviewBlock)

	if !result.Terminal {
		switch {
		case activeSettlement:
			result.WaitingFor = append(result.WaitingFor, "settlement")
		case !funded:
			result.WaitingFor = append(result.WaitingFor, "funding")
		case worker && state.ReviewState == "submitted":
			result.WaitingFor = append(result.WaitingFor, "review")
		case !worker && state.ReviewState != "submitted":
			result.WaitingFor = append(result.WaitingFor, "worker_delivery")
		case !worker && !reviewer:
			result.WaitingFor = append(result.WaitingFor, "review")
		}

		if worker && working && len(result.MissingInputs) > 0 {
			result.WaitingFor = append(result.WaitingFor, "inputs")
		}
	}

	needsFundingSignature := post.UserID == userID && !funded &&
		(escrow.State == "unfunded" || escrow.State == "" || escrow.State == "prepared" ||
			escrow.State == "failed" || escrow.State == "expired")
	result.HumanPaymentRequired = !result.Terminal && (needsFundingSignature ||
		(reviewer && funded && !activeSettlement && state.ReviewState == "submitted"))

	return result
}
