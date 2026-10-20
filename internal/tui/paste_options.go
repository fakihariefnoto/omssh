package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/archive"
	"github.com/fakihariefnoto/omssh/internal/xfer"
)

// A paste is usually one gesture, but not always: the files may need to replace
// what is there, or be unpacked on arrival, or the gesture may really be a move.
// The options card is that choice, in one place, without a second dialog per
// question.

// pasteAfter is what a paste does once the bytes have arrived.
type pasteAfter int

const (
	// pastePlain copies the files as they are.
	pastePlain pasteAfter = iota
	// pasteExtract unpacks one marked archive into a folder named after it.
	pasteExtract
	// pastePack writes the marked set into one archive at the destination instead
	// of copying the files loose.
	pastePack
)

// openPasteOptions shows the card, or explains why there is nothing to paste.
func (m *Model) openPasteOptions() {
	if m.stagedCount() == 0 {
		m.quitHint = "Nothing marked · y marks the cursor entry"
		return
	}
	m.Overlay = OverlayPaste
	m.mode = "PASTE"
}

// handlePasteOptionsKey changes the options in place; Enter runs the paste.
func (m *Model) handlePasteOptionsKey(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.Overlay = OverlayNone
		m.mode = "NORMAL"
	case "m":
		m.staged.Cut = !m.staged.Cut
	case "u":
		m.staged.Policy = int(xfer.PolicyUpdateIfNewer)
	case "o":
		m.staged.Policy = int(xfer.PolicyOverwrite)
	case "s":
		m.staged.Policy = int(xfer.PolicySkip)
	case "n":
		m.staged.After = pastePlain
	case "e":
		if !m.canExtractOnPaste() {
			m.toastInfo("Extract on paste works with one archive marked")
			break
		}
		m.staged.After = pasteExtract
	case "c":
		m.staged.After = pastePack
	case "enter":
		m.Overlay = OverlayNone
		return m.pasteStaged()
	}
	return nil
}

// packName is the archive a pack-paste would write.
func (m *Model) packName() string {
	return archive.DefaultName(m.staged.Paths)
}

// canExtractOnPaste reports whether the marked set is one archive.
func (m *Model) canExtractOnPaste() bool {
	if m.stagedCount() != 1 {
		return false
	}
	return archive.Detect(m.staged.Paths[0]) != archive.Unknown
}

// policyLabel names the conflict rule.
func pastePolicyLabel(policy int) string {
	switch xfer.ConflictPolicy(policy) {
	case xfer.PolicyOverwrite:
		return "overwrite"
	case xfer.PolicySkip:
		return "keep what is there"
	default:
		return "update if newer"
	}
}

// renderPasteOptions draws the card.
func (m *Model) renderPasteOptions(l layout) string {
	width := cardWidth(l, 48, 72)
	content := cardContent(width)

	_, to := m.stagedRoute()
	verb := "copy"
	if m.staged.Cut {
		verb = "move"
	}
	lines := []string{
		m.st.CardTitle.Render("Paste options"),
		m.st.CardMuted.Render(truncate(fmt.Sprintf("%d %s · %s → %s",
			m.stagedCount(), plural(m.stagedCount(), "item", "items"), verb, to), content)),
		"",
		m.optionRow("m", "mode", verb, content),
		m.rowChoice("u", "conflicts", "update if newer", pastePolicyLabel(m.staged.Policy) == "update if newer", content),
		m.rowChoice("o", "", "overwrite", pastePolicyLabel(m.staged.Policy) == "overwrite", content),
		m.rowChoice("s", "", "keep what is there", pastePolicyLabel(m.staged.Policy) == "keep what is there", content),
		"",
	}
	lines = append(lines,
		m.optionRow("", "after", "what arrives", content),
		m.rowChoice("n", "", "the files themselves", m.staged.After == pastePlain, content),
	)
	if m.canExtractOnPaste() {
		lines = append(lines, m.rowChoice("e", "", "unpacked into "+archive.Stem(m.staged.Paths[0])+"/",
			m.staged.After == pasteExtract, content))
	} else {
		lines = append(lines, m.st.CardMuted.Render(truncate(
			"e           unpack an archive (mark one archive)", content)))
	}
	lines = append(lines,
		m.rowChoice("c", "", "packed into "+m.packName(), m.staged.After == pastePack, content),
		"",
		m.st.CardMuted.Render(truncate("Enter pastes · Esc cancels", content)))
	return m.card(width, lines)
}

// row is one option line with its key.
func (m *Model) optionRow(key, label, value string, width int) string {
	text := fmt.Sprintf("%s  %-10s %s", key, label, value)
	return m.st.CardText.Render(truncate(text, width))
}

// rowChoice marks the chosen value with a filled dot.
func (m *Model) rowChoice(key, label, value string, chosen bool, width int) string {
	mark := "○"
	if chosen {
		mark = "●"
	}
	text := fmt.Sprintf("%s  %-10s %s %s", key, label, mark, value)
	if chosen {
		return m.st.Accent.Render(truncate(text, width))
	}
	return m.st.CardMuted.Render(truncate(text, width))
}
