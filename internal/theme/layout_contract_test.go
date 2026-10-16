package theme

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// Lip Gloss v2's Width is inclusive of the border: Width(n) produces lines of
// exactly n cells, with n-2 cells of content. The interface depends on this, so
// it is pinned here rather than rediscovered by a wrapped pane.
func TestBoxWidthIsInclusive(t *testing.T) {
	const width = 40
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(width)

	rendered := style.Render(strings.Repeat("x", width-2))
	for i, line := range strings.Split(rendered, "\n") {
		if got := lipgloss.Width(line); got != width {
			t.Errorf("line %d is %d cells wide, want %d", i, got, width)
		}
	}

	// Content longer than the interior wraps, which is exactly why every row the
	// interface builds must be truncated to the interior width first.
	overflowing := style.Render(strings.Repeat("x", width*2))
	if lines := strings.Split(overflowing, "\n"); len(lines) <= 3 {
		t.Errorf("expected content wider than the box to wrap, got %d lines", len(lines))
	}
}

// Height behaves the same way, which is what keeps the two panes level.
func TestBoxHeightIsInclusive(t *testing.T) {
	const height = 6
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(20).Height(height)
	lines := strings.Split(style.Render("x"), "\n")
	if len(lines) != height {
		t.Errorf("Height(%d) produced %d lines, want %d", height, len(lines), height)
	}
}
