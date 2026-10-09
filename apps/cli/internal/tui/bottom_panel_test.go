package tui

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func TestDecisionPanelKeepsBackgroundAndStaysAtBottom(t *testing.T) {
	m := model{width: 80, height: 30, permissions: localPermissions{open: true}}
	view := m.bottomPanel([]string{"Update Model Permissions", "› Ask for approval"}, "Enter select · Esc back")
	lines := strings.Split(ansi.Strip(view.Content), "\n")
	if len(lines) != 30 {
		t.Fatalf("panel height %d", len(lines))
	}
	background := m
	background.permissions.open = false
	original := strings.Split(ansi.Strip(background.View().Content), "\n")
	if lines[0] != original[0] {
		t.Fatal("background header lost")
	}
	for i, line := range lines {
		if strings.Contains(line, "Update Model Permissions") && i < 24 {
			t.Fatal("decision panel did not stay at bottom")
		}
	}
	if !strings.Contains(lines[29], "Enter select") {
		t.Fatal("footer not at bottom")
	}
}
