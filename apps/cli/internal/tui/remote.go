package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"github.com/rajandhamala/Maruvo/cli/internal/auth"
)

type remoteState struct {
	open, busy     bool
	tab, selection int
	generation     uint64
	offers         []api.AgentOffer
	posts          []api.Post
	err            error
}
type remoteClock time.Time
type remoteLoaded struct {
	generation uint64
	token      string
	offers     []api.AgentOffer
	posts      []api.Post
	err        error
}
type remoteStatusLoaded struct {
	generation uint64
	cursor     string
	post       api.Post
	err        error
}

func remoteTick() tea.Cmd {
	return tea.Tick(15*time.Second, func(t time.Time) tea.Msg { return remoteClock(t) })
}

func (m model) openRemote() (tea.Model, tea.Cmd) {
	if m.token == "" {
		m.commands.err = errors.New("Sign in to discover remote agents.")
		return m, nil
	}

	m.commands.open = false
	m.remote = remoteState{open: true, busy: true, generation: m.remote.generation + 1}

	return m, m.fetchRemote()
}
func (m model) fetchRemote() tea.Cmd {
	return func() tea.Msg {
		offers, offerErr := m.client.AgentOffers(m.ctx, m.token)
		posts, postErr := m.client.RemoteInbox(m.ctx, m.token)

		return remoteLoaded{m.remote.generation, m.token, offers, posts, errors.Join(offerErr, postErr)}
	}
}
func (m model) refreshRemoteStatus() tea.Cmd {
	return func() tea.Msg {
		info, err := m.client.PostInfo(m.workspaceCtx, m.token, m.workspace.Post.ID)
		return remoteStatusLoaded{m.workspaceGen, m.workspace.Cursor, info.Post, err}
	}
}
func (m model) remoteCount() int {
	if m.remote.tab == 0 {
		return len(m.remote.offers)
	}

	if m.remote.tab == 1 {
		return len(m.remote.posts)
	}

	return 0
}
func (m model) updateRemote(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.remote.open = false
	case "tab", "right":
		m.remote.tab = (m.remote.tab + 1) % 3
		m.remote.selection = 0
	case "shift+tab", "left":
		m.remote.tab = (m.remote.tab + 2) % 3
		m.remote.selection = 0
	case "up":
		m.remote.selection = max(0, m.remote.selection-1)
	case "down":
		m.remote.selection = min(max(0, m.remoteCount()-1), m.remote.selection+1)
	case "r":
		if !m.remote.busy {
			m.remote.busy = true
			return m, m.fetchRemote()
		}
	case "enter":
		if m.remoteCount() == 0 {
			return m, nil
		}

		if m.remote.tab == 0 {
			offer := m.remote.offers[m.remote.selection]
			if strconv.FormatInt(offer.UserID, 10) == m.user.ID {
				m.remote.err = errors.New("Choose another user's agent to delegate work.")
				return m, nil
			}

			m.stopWorkspace()
			m.form = newPostForm()
			m.form.targetWorker, m.form.targetName = &offer.UserID, offer.Name
			m.form.fields[1].value = strconv.FormatInt(offer.MinLamports, 10)
			m.form.fields[1].cursor = len(m.form.fields[1].value)
			m.remote.open, m.loading, m.profileOpen = false, false, false
			m.localAgent.open = false
			m.screen, m.err, m.notice = newPostScreen, nil, "Only this seller can accept. Offline work queues until they reconnect."

			return m, nil
		}

		post := m.remote.posts[m.remote.selection]
		m.remote.open = false
		m.localAgent.open = false

		m.posts, m.selected, m.own, m.screen, m.loading = []api.Post{
			post,
		}, 0, post.UserID == parseUser(
			m.user.ID,
		), detailScreen, false
		if post.AcceptedBy != nil {
			return m.openWorkspace("")
		}

		m.loading = true

		return m, m.postInfo()
	}

	return m, nil
}
func parseUser(value string) int64 { id, _ := strconv.ParseInt(value, 10, 64); return id }

func remoteLabel(post api.Post) string {
	label := "Remote: " + strings.ReplaceAll(post.Remote.Status, "_", " ")
	if post.Remote.AgentName != "" {
		label += " · " + plain(post.Remote.AgentName)
	}

	if post.AcceptedBy != nil || post.TargetWorker != nil {
		presence := "offline"
		if post.Remote.WorkerOnline {
			presence = "online"
		}

		label += " · seller " + presence
	}

	return label
}

func (m model) remoteView() tea.View {
	tabs := []string{"Sellers", "My remote tasks", "Connect harness"}
	for i, label := range tabs {
		if i == m.remote.tab {
			tabs[i] = accent(label)
		} else {
			tabs[i] = muted(label)
		}
	}

	rows := []string{strings.Join(tabs, "   "), ""}

	if m.remote.tab == 2 {
		var cfg auth.HarnessConnection
		if auth.LoadRemoteState(m.profile, "harness.json", &cfg) == nil && cfg.APIURL == m.client.URL() &&
			cfg.Account == m.user.ID {
			rows = append(
				rows,
				bold("Harness connected"),
				plain(cfg.Executable),
				muted("Work directory: "+plain(cfg.Directory)),
				"",
			)
		}

		rows = append(
			rows,
			"Your existing harness runs the work.",
			"Connect once from another terminal:",
			accent("maruvo agent connect --exec ADAPTER --file OFFER.json"),
			"Then: agent serve --timeout 24h",
			"Requesters: agent listen --exec ADAPTER --timeout 24h",
			"",
			"Requests, messages and deliveries stay on the server.",
			"Only the executing seller needs to stay online.",
			"Funding and payment require a human wallet signature.",
		)
	} else {
		_, height := m.dimensions()
		count := max(1, height-9)

		start := max(0, m.remote.selection-count+1)
		for i := start; i < min(m.remoteCount(), start+count); i++ {
			label := ""

			if m.remote.tab == 0 {
				offer := m.remote.offers[i]

				state := "offline · requests queue"
				if offer.Online && offer.AvailableUntil != nil && offer.AvailableUntil.After(time.Now()) {
					state = "busy"
					if offer.Available {
						state = "available"
					}
				}

				label = fmt.Sprintf("%s · %s · %d lamports", plain(offer.Name), state, offer.MinLamports)
			} else {
				post := m.remote.posts[i]
				label = fmt.Sprintf("#%d %s · %s", post.ID, plain(post.Title), remoteLabel(post))
			}

			if i == m.remote.selection {
				rows = append(rows, accent("› "+label))
			} else {
				rows = append(rows, "  "+label)
			}
		}

		if m.remoteCount() == 0 && !m.remote.busy {
			rows = append(rows, muted("No remote work yet. Publish an offer or delegate to a seller."))
		}

		if m.remote.tab == 0 && len(m.remote.offers) > 0 {
			offer := m.remote.offers[m.remote.selection]
			rows = append(rows, "", muted(plain(strings.Join(offer.Capabilities, ", "))))
		}
	}

	if m.remote.err != nil {
		rows = append(rows, warning(plain(m.remote.err.Error())))
	} else if m.remote.busy {
		rows = append(rows, muted("Refreshing remote work..."))
	}

	return m.localView(
		"Remote collaboration",
		rows,
		"Tab view · ↑↓ select · Enter delegate / open · r refresh · Esc close",
	)
}
