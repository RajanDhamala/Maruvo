package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type taskFlow struct {
	stage, next   string
	label, action string
}

func (m model) taskFlow(post api.Post, escrow api.Escrow, review string, settlement api.Settlement, canReview bool) taskFlow {
	switch {
	case escrow.State == "refunded" || review == "refunded":
		return taskFlow{stage: "Closed · payment refunded", next: "The payment was returned to the requester."}
	case escrow.State == "released":
		return taskFlow{stage: "Complete · worker paid", next: "View the delivery and payment details in the workspace."}
	case review == "approved" && settlement.State != "pending" && settlement.State != "prepared":
		return taskFlow{stage: "Delivery approved · payment unconfirmed", next: "Verify escrow release before treating the worker as paid.", label: "View review", action: "v"}
	case post.Status == "completed":
		return taskFlow{stage: "Closed · task completed", next: "This task is closed to new workers."}
	case post.Status == "cancelled":
		return taskFlow{stage: "Closed · task cancelled", next: "This assignment is closed. Its workspace keeps the history."}
	case post.AcceptedBy == nil:
		if !post.EndTime.After(time.Now()) {
			return taskFlow{stage: "Closed to new workers", next: "The acceptance deadline has passed."}
		}
		if m.isPoster(post) {
			return taskFlow{stage: "Step 1/4 · Find a worker", next: "Wait for a worker to accept. You fund escrow after acceptance."}
		}
		if post.TargetWorker != nil && strconv.FormatInt(*post.TargetWorker, 10) != m.user.ID {
			return taskFlow{stage: "Step 1/4 · Waiting for selected worker", next: "Only the selected worker can accept this task."}
		}
		return taskFlow{stage: "Step 1/4 · Accept the task", next: "Read the brief and deadlines, then accept. Wait for funding before starting."}
	case settlement.State == "prepared":
		return taskFlow{stage: "Step 4/4 · Decision prepared", next: "The reviewer must sign the prepared decision before payment is submitted.", label: "View review", action: "v"}
	case settlement.State == "pending":
		return taskFlow{stage: "Step 4/4 · Payment pending", next: "A payment decision is in progress. Wait for confirmation before submitting again.", label: "View review", action: "v"}
	case escrow.State == "pending":
		return taskFlow{stage: "Step 2/4 · Funding pending", next: "Funding was submitted. Wait for confirmation before starting work.", label: "View funding", action: "b"}
	case escrow.State != "confirmed" && (escrow.State != "" || post.Status != "in_progress"):
		if m.isPoster(post) {
			return taskFlow{stage: "Step 2/4 · Fund escrow", next: "Review the payment and fees, then sign funding to let the worker start.", label: "View funding", action: "b"}
		}
		return taskFlow{stage: "Step 2/4 · Waiting for funding", next: "The requester must fund escrow. You can clarify the task and share inputs now.", label: "View funding", action: "b"}
	case review == "submitted" || post.Deadline.Stage == "review" || post.Remote.Status == "waiting_for_review":
		if canReview {
			return taskFlow{stage: "Step 4/4 · Review delivery", next: "Check the result, then request changes, pay the worker, or refund the requester.", label: "Review delivery", action: "v"}
		}
		return taskFlow{stage: "Step 4/4 · Awaiting review", next: "Delivery is submitted. The authorized reviewer decides on changes or payment.", label: "View delivery", action: "v"}
	case review == "changes_requested":
		if m.isWorker(post) {
			return taskFlow{stage: "Step 3/4 · Revise delivery", next: "Read the requested changes, update your work, then submit it again.", label: "Submit revision", action: "s"}
		}
		return taskFlow{stage: "Step 3/4 · Waiting for revision", next: "The worker is revising the delivery. Review the new version when submitted.", label: "View changes", action: "v"}
	default:
		if m.isWorker(post) {
			return taskFlow{stage: "Step 3/4 · Deliver work", next: "Complete the brief, share the result, then submit it for review.", label: "Submit delivery", action: "s"}
		}
		return taskFlow{stage: "Step 3/4 · Work in progress", next: "Share any needed inputs. The worker will submit the result for review.", label: "View delivery", action: "v"}
	}
}

func (m model) workspaceFlow() taskFlow {
	w := m.workspace
	flow := m.taskFlow(w.Post, w.Escrow, w.State.ReviewState, w.Settlement, w.CanReview)
	// Delivery buttons follow the same funding/review gates as the submit command.
	if flow.action == "s" && (w.Escrow.State != "confirmed" ||
		(w.State.ReviewState != "working" && w.State.ReviewState != "changes_requested")) {
		flow.label, flow.action = "View delivery", "v"
	}
	return flow
}

func flowRows(flow taskFlow, width int) []string {
	rows := []string{accent(flow.stage)}
	for _, row := range strings.Split(ansi.Wrap("Next: "+flow.next, max(1, width), " "), "\n") {
		rows = append(rows, muted(row))
	}
	return rows
}
