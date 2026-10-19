package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// archiveExts are the extensions the interface treats as archives: they are
// listed as such, previewed as their contents, and offered Extract.
var archiveExts = []string{
	".zip",
	".tar", ".tar.gz", ".tgz", ".tar.zst", ".tzst", ".tar.bz2", ".tbz2", ".tar.xz", ".txz",
	".gz", ".zst", ".xz", ".bz2",
	".7z", ".rar",
}

// IsArchive reports whether a name looks like an archive.
func IsArchive(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range archiveExts {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// humanSize renders a byte count the way a person reads it.
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, u := range units {
		value /= unit
		if value < unit {
			if value < 10 {
				return fmt.Sprintf("%.1f %s", value, u)
			}
			return fmt.Sprintf("%.0f %s", value, u)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}

// permString renders the ten-character permission column, marking directories
// and symlinks in the leading position the way ls does.
//
// A server that omits attributes leaves the mode at zero. Ten question marks per
// row is noise; one is an honest "not reported", and the icon still comes from
// the directory flag.
func permString(e Entry) string {
	if e.Mode == 0 {
		return padLeft("?", 10)
	}
	if e.Dir {
		return "d" + e.Mode.Perm().String()[1:]
	}
	if e.Symlink {
		return "l" + e.Mode.Perm().String()[1:]
	}
	return e.Mode.String()
}

// truncate shortens s to width, ending with an ellipsis when it does not fit.
//
// It is escape-aware: styling is part of the string, so a rune-counting cut
// would slice through an escape sequence and print its tail as text.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

// padRight pads s with spaces to width, or truncates it.
func padRight(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = truncate(s, width)
	if n := width - lipgloss.Width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// padLeft pads s on the left, for right-aligned columns.
func padLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	s = truncate(s, width)
	if n := width - lipgloss.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}

// fuzzyMatch reports whether every rune of needle appears in order in haystack,
// and how good the match is. Higher is better; the score rewards matches at word
// boundaries and penalises gaps, which is what makes a palette feel right.
func fuzzyMatch(haystack, needle string) (int, bool) {
	if needle == "" {
		return 0, true
	}
	h := strings.ToLower(haystack)
	n := strings.ToLower(needle)
	hi, score, streak := 0, 0, 0
	for _, nc := range n {
		found := false
		for ; hi < len(h); hi++ {
			if rune(h[hi]) != nc {
				streak = 0
				continue
			}
			score += 10
			if hi == 0 {
				score += 20 // match at the start of the string is best
			} else {
				switch h[hi-1] {
				case ' ', '-', '_', '/', '.':
					score += 15 // match at a word boundary
				}
			}
			streak++
			score += streak
			hi++
			found = true
			break
		}
		if !found {
			return 0, false
		}
	}
	// Prefer shorter haystacks when the match quality is otherwise equal.
	score -= len(h) / 4
	return score, true
}

// spaces returns n spaces.
func spaces(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(" ", n)
}

// itoa renders a port, and "" for the default.
func itoa(n int) string { return strconv.Itoa(n) }

// itoaZero renders a port, or "" when it is unset (the default of 22).
func itoaZero(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// atoi parses a port.
func atoi(s string) (int, error) { return strconv.Atoi(strings.TrimSpace(s)) }

// spread puts one label on the left and another on the right of one row.
func spread(left, right string, width int) string {
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, width, "…")
	}
	return left + spaces(gap) + right
}

// userHome is os.UserHomeDir, wrapped so callers in this package do not all
// repeat the error handling.
func userHome() (string, error) { return os.UserHomeDir() }

// clamp keeps v inside [lo, hi].
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// plural returns the singular or plural form for a count.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
