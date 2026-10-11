package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
	"sync"
	"testing"
)

func TestFullAccessRequiresLocalConfirmationAndMatchesScope(t *testing.T) {
	m := model{user: api.User{ID: "1"}, profile: "worker", directory: "/tmp/project", permissions: localPermissions{open: true}}
	if m.hasFullAccess() {
		t.Fatal("default granted full access")
	}
	next, _ := m.updatePermissions(tea.KeyPressMsg{Code: 'y'})
	m = next.(model)
	if m.hasFullAccess() {
		t.Fatal("granted without confirmation screen")
	}
	next, _ = m.updatePermissions(tea.KeyPressMsg{Code: 'f'})
	m = next.(model)
	if !m.permissions.confirm || m.hasFullAccess() {
		t.Fatal("full mode skipped confirmation")
	}
	next, _ = m.updatePermissions(tea.KeyPressMsg{Code: 'y'})
	m = next.(model)
	if !m.hasFullAccess() {
		t.Fatal("confirmed grant missing")
	}
	for _, changed := range []model{{user: api.User{ID: "2"}, profile: m.profile, directory: m.directory, permissions: m.permissions}, {user: m.user, profile: "poster", directory: m.directory, permissions: m.permissions}, {user: m.user, profile: m.profile, directory: "/tmp/other", permissions: m.permissions}} {
		if changed.hasFullAccess() {
			t.Fatal("permission leaked across local scope")
		}
	}
	next, _ = m.updatePermissions(tea.KeyPressMsg{Code: 'a'})
	m = next.(model)
	if m.hasFullAccess() {
		t.Fatal("ask mode did not revoke full access")
	}
}

func TestTestingDefaultAllowsActionsAndExplicitModesStillApply(t *testing.T) {
	m := model{user: api.User{ID: "1"}, profile: "worker", directory: t.TempDir(),
		permissions: localPermissions{defaultMode: 2, remembered: &sync.Map{}}}
	captured := m
	if !m.hasFullAccess() || m.permissions.open || m.permissions.confirm {
		t.Fatal("testing default requested confirmation instead of allowing actions")
	}
	for _, action := range []string{"", "Run command", "Share with remote task", "Download from remote task"} {
		if !m.permitsAction(action) {
			t.Fatalf("testing default requested approval for %q", action)
		}
	}
	next, _ := m.openPermissions()
	m = next.(model)
	if m.permissions.selection != 2 {
		t.Fatal("permission menu did not reflect default Full Access")
	}
	for _, mode := range []int{0, 1, 3} {
		m.setPermissionMode(mode)
		if m.hasFullAccess() || m.permitsAction("Run command") || captured.permitsAction("Run command") {
			t.Fatalf("explicit mode %d failed to override testing default", mode)
		}
		if m.permitsAction("") != (mode == 1) {
			t.Fatalf("explicit mode %d failed to control file edits", mode)
		}
	}
	m.directory = t.TempDir()
	if !m.hasFullAccess() || !m.permitsAction("Run command") {
		t.Fatal("new local scope lost the testing default")
	}
}

func TestPermissionMenuSelectionAndAutomaticActionBoundaries(t *testing.T) {
	m := model{user: api.User{ID: "1"}, profile: "worker", directory: "/tmp/project", permissions: localPermissions{open: true}}
	next, _ := m.updatePermissions(tea.KeyPressMsg{Code: tea.KeyDown})
	m = next.(model)
	next, _ = m.updatePermissions(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.permissionMode() != 1 || !m.permitsAction("") || m.permitsAction("Run command") || m.permitsAction("Share with remote task") || m.permitsAction("Download from remote task") {
		t.Fatal("automatic mode bypassed an unapproved action")
	}
	m.permissions.open = true
	next, _ = m.updatePermissions(tea.KeyPressMsg{Code: '4'})
	m = next.(model)
	next, _ = m.updatePermissions(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.permissionMode() != 3 || m.permitsAction("") || m.permitsAction("Run command") {
		t.Fatal("read-only mode auto-approved mutation")
	}
	m.permissions.open = true
	next, _ = m.updatePermissions(tea.KeyPressMsg{Code: '3'})
	m = next.(model)
	next, _ = m.updatePermissions(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if !m.permissions.confirm || m.permissionMode() != 3 {
		t.Fatal("full access skipped confirmation")
	}
}
