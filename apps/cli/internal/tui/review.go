package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/wallet"
)

type reviewPrepared struct {
	generation uint64
	plan       api.SettlementPlan
	err        error
}

func (m model) reviewIsCurrent() bool {
	return m.workspace.CanReview && m.reviewVersion > 0 &&
		m.reviewVersion == m.workspace.State.SubmissionVersion &&
		m.workspace.State.ReviewState == "submitted" && m.workspace.Escrow.State == "confirmed"
}

func (m model) prepareSettlement() (tea.Model, tea.Cmd) {
	action := "release"
	if m.workspaceAction == "x" {
		action = "refund"
	}

	generation, ctx, postID, version, note := m.workspaceGen, m.workspaceCtx, m.workspace.Post.ID, m.reviewVersion, strings.TrimSpace(
		m.workspaceInput.value,
	)
	m.loading, m.err = true, nil

	return m, func() tea.Msg {
		plan, err := m.client.PrepareSettlement(ctx, m.token, postID, version, action, note)
		return reviewPrepared{generation: generation, plan: plan, err: err}
	}
}

func (m model) reviewPrepared(msg reviewPrepared) (tea.Model, tea.Cmd) {
	if m.screen != workspaceScreen || msg.generation != m.workspaceGen {
		return m, nil
	}

	m.loading = false
	m.setPostError(msg.err)

	if msg.err == nil {
		if !m.reviewIsCurrent() || msg.plan.Settlement.SubmissionVersion != m.reviewVersion {
			m.err = errors.New("Delivery changed. Reopen review before deciding.")
			return m, nil
		}

		m.reviewPlan = msg.plan
		m.workspace.Settlement = msg.plan.Settlement

		m.reviewConfirm = msg.plan.Settlement.State == "prepared"
		if !m.reviewConfirm {
			m.workspaceAction = ""
			m.notice = "Settlement is already " + msg.plan.Settlement.State + ". Waiting for confirmation."
		}
	}

	return m, nil
}

func (m model) signSettlement() (tea.Model, tea.Cmd) {
	if !m.reviewIsCurrent() || m.reviewPlan.Settlement.SubmissionVersion != m.reviewVersion {
		m.reviewConfirm, m.workspaceAction = false, ""
		m.err = errors.New("Delivery changed. Reopen review before deciding.")

		return m, nil
	}

	generation, ctx, post, plan := m.workspaceGen, m.workspaceCtx, m.workspace.Post, m.reviewPlan
	m.loading, m.err = true, nil

	return m, func() tea.Msg {
		key, err := wallet.Load()
		if err != nil {
			return workspaceActionResult{generation: generation, err: err}
		}

		transaction, err := key.SignSettlement(post, plan)
		if err == nil {
			err = m.client.SubmitSettlement(ctx, m.token, post.ID, transaction)
		}

		return workspaceActionResult{
			generation: generation,
			err:        err,
			notice:     "Settlement submitted. Waiting for confirmation.",
		}
	}
}

func (m model) reviewConfirmationLayout() postLayout {
	plan, post := m.reviewPlan, m.workspace.Post

	title, recipient, buttonLabel := "Pay the worker?", post.WorkerWallet, "Sign & pay worker"
	if plan.Settlement.Action == "refund" {
		title, recipient, buttonLabel = "Refund the poster?", post.PosterWallet, "Sign & refund poster"
	}

	l := postLayout{
		rows: []string{
			bold(title),
			"",
			fmt.Sprintf("Escrow amount  %d lamports", post.CostLamports),
			fmt.Sprintf("Network fee    %d lamports", plan.Settlement.FeeLamports),
			"Network        " + plan.Escrow.Network,
			"Recipient      " + ansi.Truncate(recipient, max(1, m.contentWidth()-15), "…"),
			fmt.Sprintf(
				"Delivery       Version %d",
				plan.Settlement.SubmissionVersion,
			),
			"Review note    " + plain(plan.Settlement.Note),
			"",
		},
		footer: "Enter / y sign decision   Esc cancel",
	}

	_, height := m.dimensions()
	if height < 22 {
		l.rows = []string{
			bold(title),
			fmt.Sprintf("Amount %d · Fee %d lamports", post.CostLamports, plan.Settlement.FeeLamports),
			"To " + ansi.Truncate(recipient, max(1, m.contentWidth()-3), "…"),
			fmt.Sprintf(
				"%s · Delivery v%d",
				plan.Escrow.Network,
				plan.Settlement.SubmissionVersion,
			),
			plain(plan.Settlement.Note),
		}
	}

	l.buttons([]string{buttonLabel, "Cancel"}, []string{"workspace-send", "cancel"})

	if m.loading {
		l.footer = "Submitting reviewer-signed settlement..."
	}

	return l
}

func (m model) reviewLayout(l postLayout) postLayout {
	width := m.contentWidth()
	_, height := m.dimensions()

	state := m.workspace.State
	if height < 20 {
		l.rows = []string{bold("Review · " + state.ReviewState)}
	}

	l.rows = append(l.rows, "", bold(fmt.Sprintf("Delivery · version %d", state.SubmissionVersion)))

	body := []string{}
	if state.SubmittedAt == nil {
		body = append(body, muted("The worker has not submitted delivery yet."))
	} else {
		body = append(body, muted(state.SubmittedAt.Local().Format("02 Jan 2006, 15:04")))

		body = append(body, strings.Split(ansi.Wrap(plain(state.Submission), max(1, width), " "), "\n")...)
		for _, id := range state.DeliveryFiles {
			for _, file := range m.workspace.Files {
				if file.ID == id {
					body = append(body, "File: "+plain(file.Name))
					break
				}
			}
		}
	}

	if state.ReviewNote != "" {
		body = append(body, "", bold("Requested changes"))
		body = append(body, strings.Split(ansi.Wrap(plain(state.ReviewNote), max(1, width), " "), "\n")...)
	}

	available := max(1, m.bodyHeight()-len(l.rows)-2)
	start := min(m.activityScroll, max(0, len(body)-available))
	l.rows = append(l.rows, body[start:min(len(body), start+available)]...)

	l.rows = append(l.rows, "")
	if m.workspace.CanReview && state.ReviewState == "submitted" && m.workspace.Escrow.State == "confirmed" {
		l.buttons([]string{"Approve & pay", "Refund", "Request changes"}, []string{"a", "x", "e"})
		l.footer = "a approve & pay · x refund · e request changes · Esc chat"
	} else {
		l.buttons([]string{"Chat", "Files"}, []string{"v", "workspace-tab"})
		l.footer = "Esc chat · Tab files · ↑↓ scroll · b task"
	}

	return l
}
