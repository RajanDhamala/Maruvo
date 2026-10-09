package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/rajandhamala/Maruvo/cli/internal/api"
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
