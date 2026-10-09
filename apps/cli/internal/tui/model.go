package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
)

type screen int

const (
	feedScreen screen = iota
	myPostsScreen
	newPostScreen
	detailScreen
	workspaceScreen
)

var levels = []string{"easy", "medium", "complex"}
var statuses = []string{"open", "negotiating", "in_progress", "completed", "cancelled"}

type model struct {
	shortcutsOpen          bool
	shortcutScroll         int
	ctx                    context.Context
	client                 *api.Client
	profile                string
	message                string
	loading                bool
	response               api.DemoResponse
	err                    error
	demo                   bool
	user                   api.User
	token                  string
	loggingIn              bool
	authProvider           string
	homeFocus              int
	homeInput              textField
	homePath               string
	directory              string
	commands               commandMenu
	providers              providerSettings
	localAgent             localAgentState
	workAgent              workspaceAgent
	workSetup              workSetup
	permissions            localPermissions
	remote                 remoteState
	agentControls          agentControls
	dashboard              dashboardState
	profileOpen            bool
	walletAddress          string
	escrow                 api.Escrow
	fundingConfirm         bool
	width                  int
	height                 int
	screen                 screen
	posts                  []api.Post
	selected               int
	level                  int
	taskFilter             int
	own                    bool
	form                   postForm
	descriptionSearchSeq   uint64
	picker                 deadlinePicker
	notice                 string
	deleting               bool
	recovering             string
	recoveryDeadline       time.Time
	recoveryDelivery       time.Time
	editingStatus          bool
	statusChoice           int
	scroll                 int
	invitations            inviteState
	workspace              api.Workspace
	workspaceGen           uint64
	workspaceCtx           context.Context
	workspaceCancel        context.CancelFunc
	stream                 *api.WorkspaceStream
	live                   string
	presence               *api.WorkspacePresence
	workspaceAction        string
	workspacePendingAction string
	workspaceInput         textField
	composer               chatComposer
	workspaceFiles         bool
	fileSelection          int
	activityScroll         int
	workspaceReview        bool
	reviewPlan             api.SettlementPlan
	reviewConfirm          bool
	reviewVersion          int64
}

func Run(ctx context.Context, client *api.Client, message string, demo bool, profile string) error {
	final, err := tea.NewProgram(model{
		ctx:       ctx,
		client:    client,
		profile:   profile,
		dashboard: dashboardState{focus: dashboardPrompt},
		homeInput: textField{limit: 2000},
		homePath:  currentHomePath(),
		message:   message,
		loading:   true,
		demo:      demo,
		width:     80,
		height:    24,
	}).Run()
	if m, ok := final.(model); ok {
		m.stopWorkspace()
		m.stopInvites()
	}

	return err
}

func (m model) Init() tea.Cmd {
	if m.demo {
		return m.sendDemo()
	}

	return tea.Batch(m.restoreSession(), m.checkStartupProvider(), deadlineTick(), remoteTick())
}
