package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/fakihariefnoto/omssh/internal/actions"
)

// overlayRows is how many list rows an overlay shows before scrolling.
const overlayRows = 10

// renderOverlay draws the modal layer, if one is open. Cards are opaque boxes
// spliced onto the body, so the panes stay visible around them.
func (m *Model) renderOverlay(l layout) string {
	switch m.Overlay {
	case OverlayPalette:
		return m.renderPalette(l)
	case OverlayMenu:
		return m.renderMenu(l)
	case OverlayHelp:
		return m.renderHelp(l)
	case OverlayOnboarding:
		return m.renderOnboarding(l)
	case OverlaySettings:
		return m.renderSettings(l)
	case OverlayConfirm:
		return m.renderConfirm(l)
	case OverlayPrompt:
		return m.renderPrompt(l)
	case OverlayForm:
		return m.renderConnectionForm(l)
	case OverlayHostKey:
		return m.renderHostKey(l)
	case OverlaySecret:
		return m.renderSecret(l)
	case OverlayStaged:
		return m.renderStaged(l)
	case OverlayDeploy:
		return m.renderDeploy(l)
	case OverlayPanes:
		return m.renderPanesChooser(l)
	default:
		return ""
	}
}

// cardWidth sizes a card to the terminal without letting it get too wide to
// read.
func cardWidth(l layout, minWidth, maxWidth int) int {
	return clamp(l.Width-8, minWidth, maxWidth)
}

// cardContent is the space a card has for content: the border and the horizontal
// padding are consumed first. Every row must measure exactly this, or Lip Gloss
// wraps it and the card grows by a line per item.
func cardContent(cardWidth int) int { return cardWidth - 6 }

// listRow lays out a label and its shortcut to exactly the content width, with
// the shortcut in a right-hand column.
func (m *Model) listRow(label, key string, content int) string {
	if key == "" {
		key = "—"
	}
	keyCell := m.st.PaletteKey.Render(key)
	nameWidth := max(1, content-lipgloss.Width(keyCell)-1)
	row := padRight(label, nameWidth) + " " + keyCell
	return ansi.Truncate(row, content, "…")
}

// renderPalette draws the command palette: every action, searchable, each with
// the shortcut that would have run it. This is how shortcuts are learned.
func (m *Model) renderPalette(l layout) string {
	width := cardWidth(l, 44, 74)
	content := cardContent(width)
	items := m.paletteItems()
	ctx := m.actionContext()

	lines := []string{
		m.st.CardTitle.Render("Actions") + " " + m.st.CardMuted.Render("type to filter"),
		m.st.PathInput.Width(content).Render(truncate(m.palette.input.Rendered(), content)),
		"",
	}

	start := clamp(m.palette.cursor-overlayRows/2, 0, max(0, len(items)-overlayRows))
	shown := 0
	for i := start; i < len(items) && shown < overlayRows; i++ {
		a := items[i]
		label := a.Label
		if !a.Applicable(ctx) {
			label += "  (" + m.unavailableReason(a) + ")"
		}
		row := m.listRow(label, m.keys.Primary(a.ID), content)

		switch {
		case i == m.palette.cursor:
			lines = append(lines, m.st.PaletteItemActive.Render(row))
		case !a.Applicable(ctx):
			lines = append(lines, m.st.CardMuted.Render(row))
		default:
			lines = append(lines, m.st.PaletteItem.Render(row))
		}
		shown++
	}
	if shown == 0 {
		lines = append(lines, m.st.CardMuted.Render("No action matches "+m.palette.input.Value))
	}

	lines = append(lines, "", m.st.CardMuted.Render("Enter run · Esc close · ↑↓ move"))
	return m.card(width, lines)
}

// unavailableReason explains why an action cannot run yet, in plain words.
func (m *Model) unavailableReason(a actions.Action) string {
	ctx := m.actionContext()
	switch {
	case a.ID == "file.paste" && ctx.Staged == 0:
		return "nothing copied yet"
	case a.ID == "file.delete" && ctx.Selection == 0 && !ctx.Cursor:
		return "nothing selected"
	case strings.HasPrefix(string(a.ID), "archive.") && !ctx.CursorIsArchive:
		return "needs an archive"
	case a.ID == "deploy.open" && !ctx.Connected:
		return "needs a connection"
	case !ctx.Connected && !ctx.Local:
		return "no connection"
	case a.ID == "nav.switch-pane" && ctx.Panes < 2:
		return "one pane at this width"
	default:
		return "not available here"
	}
}

// renderMenu draws the context menu: every action that applies right now.
func (m *Model) renderMenu(l layout) string {
	width := cardWidth(l, 40, 62)
	content := cardContent(width)
	items := m.menuItems()

	lines := []string{m.st.CardTitle.Render("File actions"), ""}
	if len(items) == 0 {
		lines = append(lines, m.st.CardMuted.Render("Nothing to do here yet"))
	}
	start := clamp(m.menu.cursor-overlayRows/2, 0, max(0, len(items)-overlayRows))
	shown := 0
	for i := start; i < len(items) && shown < overlayRows; i++ {
		a := items[i]
		row := m.listRow(a.Label, m.keys.Primary(a.ID), content)
		switch {
		case i == m.menu.cursor && a.Danger:
			lines = append(lines, m.st.ButtonDanger.Render(row))
		case i == m.menu.cursor:
			lines = append(lines, m.st.PaletteItemActive.Render(row))
		case a.Danger:
			lines = append(lines, m.st.Danger.Render(row))
		default:
			lines = append(lines, m.st.PaletteItem.Render(row))
		}
		shown++
	}
	lines = append(lines, "", m.st.CardMuted.Render("Enter run · Esc close"))
	return m.card(width, lines)
}

// renderHelp draws the searchable keymap: every category with an example or two,
// so it reads as an overview that fits on one screen. The palette is the lookup
// tool.
func (m *Model) renderHelp(l layout) string {
	width := cardWidth(l, 48, 78)
	content := cardContent(width)
	query := m.help.input.Value

	lines := []string{
		m.st.CardTitle.Render("Help") + " " + m.st.CardMuted.Render("type to search"),
		m.st.PathInput.Width(content).Render(truncate(m.help.input.Rendered(), content)),
		"",
	}

	items := m.helpItems()
	if len(items) == 0 {
		lines = append(lines, m.st.CardMuted.Render("Nothing matches "+query))
	}
	perCategory := 1
	if query != "" {
		perCategory = 8
	}
	for _, category := range actions.Categories {
		group := make([]actions.Action, 0, perCategory)
		for _, a := range items {
			if a.Category == category {
				group = append(group, a)
			}
		}
		if len(group) == 0 {
			continue
		}
		lines = append(lines, m.st.PaletteGroup.Render(string(category)))
		for i, a := range group {
			if i == perCategory {
				lines = append(lines, m.st.CardMuted.Render(fmt.Sprintf("  … %d more", len(group)-perCategory)))
				break
			}
			lines = append(lines, m.st.PaletteItem.Render(m.listRow(a.Label, m.keys.Primary(a.ID), content)))
		}
	}
	if dropped := m.dropped; len(dropped) > 0 {
		lines = append(lines, "", m.st.Warning.Render("Unavailable in this terminal"))
		for _, d := range dropped {
			lines = append(lines, m.st.CardMuted.Render(truncate(fmt.Sprintf("%s: %s", d.Key, d.Reason), content)))
		}
	}
	lines = append(lines, "", m.st.CardMuted.Render("Ctrl+K searches every action · Esc close"))
	return m.card(width, lines)
}

// renderOnboarding is the first-run card: two useful things and six lines of
// orientation.
func (m *Model) renderOnboarding(l layout) string {
	width := cardWidth(l, 50, 76)
	lines := []string{
		m.st.CardTitle.Render("Welcome to omssh"),
		"",
		m.st.CardText.Render("Connections live in ~/.ssh/config. Deploy profiles, themes"),
		m.st.CardText.Render("and keybindings live in omssh's own config."),
		"",
		m.button(true, "Import hosts") + "  " + m.button(false, "Add a connection") + "  " + m.button(false, "Skip"),
		"",
		m.st.CardMuted.Render("Getting around"),
		m.keyLine("Ctrl+K", "find any action", "F5", "copy to the server"),
		m.keyLine("Ctrl+C / Ctrl+V", "copy & paste", "Ctrl+E", "extract an archive"),
		m.keyLine("F8 / Del", "delete (asks first)", "Ctrl+Q", "quit"),
		m.keyLine("F1 / ?", "full help", "Esc", "back, never quit"),
	}
	return m.card(width, lines)
}

// keyLine renders two key/description pairs on one row.
func (m *Model) keyLine(k1, d1, k2, d2 string) string {
	left := m.st.StatusKey.Render(k1) + " " + m.st.CardMuted.Render(d1)
	right := m.st.StatusKey.Render(k2) + " " + m.st.CardMuted.Render(d2)
	return padRight(left, 38) + right
}

// button renders a button, highlighted when it is the default.
func (m *Model) button(active bool, label string) string {
	if active {
		return m.st.ButtonActive.Render(label)
	}
	return m.st.Button.Render(label)
}

// renderSettings draws the small settings card.
func (m *Model) renderSettings(l layout) string {
	width := cardWidth(l, 44, 64)
	content := cardContent(width)
	options := m.settingsOptions()

	lines := []string{m.st.CardTitle.Render("Settings and theme"), "", m.st.CardMuted.Render("Theme")}
	for i, name := range options {
		mark := " "
		if name == m.themeName {
			mark = m.st.Success.Render("✓")
		}
		row := padRight(mark+"  "+name, content)
		if i == m.settings.cursor {
			lines = append(lines, m.st.PaletteItemActive.Render(row))
		} else {
			lines = append(lines, m.st.PaletteItem.Render(row))
		}
	}
	lines = append(lines, "",
		m.st.CardMuted.Render("Icons: "+m.cfg.UI.Icons+"   Motion: "+m.cfg.UI.Motion),
		"",
	)

	// The keys live here too: "how does this behave" and "what can I press" are
	// the same question asked at different moments.
	helpRow := m.listRow("Keyboard and help", "?", content)
	if m.settings.cursor == len(options) {
		lines = append(lines, m.st.PaletteItemActive.Render(helpRow))
	} else {
		lines = append(lines, m.st.PaletteItem.Render(helpRow))
	}
	lines = append(lines,
		"",
		m.st.CardMuted.Render("↑↓ choose · Enter apply · ? opens the keymap · Ctrl+, close"),
	)
	return m.card(width, lines)
}

// renderConfirm draws the confirmation for a destructive action. v1 has no trash
// and no undo, so this card carries the whole safety burden.
func (m *Model) renderConfirm(l layout) string {
	width := cardWidth(l, 46, 70)
	content := cardContent(width)

	lines := []string{m.st.CardTitle.Render(m.confirm.Title), ""}
	for _, line := range m.confirm.Body {
		lines = append(lines, m.st.CardText.Render(truncate(line, content)))
	}
	lines = append(lines, "")
	if m.confirm.Danger {
		lines = append(lines,
			m.button(false, "Cancel")+"   "+m.st.ButtonDanger.Render("Delete"),
			"",
			m.st.CardMuted.Render("Permanent: omssh has no trash and no undo."),
		)
	} else {
		lines = append(lines, m.button(false, "Cancel")+"   "+m.button(true, "Confirm"))
	}
	return m.card(width, lines)
}

// renderPrompt draws a single-field prompt: new folder, new file, rename, path.
//
// The path prompt adds the folders that continue what is typed, so going to a
// directory is choosing from a list rather than recalling a path.
func (m *Model) renderPrompt(l layout) string {
	width := cardWidth(l, 44, 70)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render(m.prompt.Title),
		"",
		m.st.CardMuted.Render(truncate(m.prompt.Hint, content)),
		m.st.PathInput.Width(content).Render(truncate(m.prompt.input.Rendered(), content)),
	}

	if m.hasSuggestions() {
		lines = append(lines, "")
		limit := min(6, len(m.prompt.suggestions))
		start := clamp(m.prompt.suggestCursor-limit/2, 0, max(0, len(m.prompt.suggestions)-limit))
		for i := start; i < start+limit; i++ {
			suggestion := m.prompt.suggestions[i]
			label := truncate(contractHome(suggestion), content-2)
			if i == m.prompt.suggestCursor && m.prompt.suggestPicked {
				lines = append(lines, m.st.Accent.Bold(true).Render("▸ "+label))
				continue
			}
			if i == 0 && !m.prompt.suggestPicked {
				// The first entry is what Tab would take; mark it quietly.
				lines = append(lines, m.st.CardText.Render("  "+label)+m.st.CardMuted.Render("  Tab"))
				continue
			}
			lines = append(lines, m.st.CardMuted.Render("  "+label))
		}
		lines = append(lines, "", m.st.CardMuted.Render("↑↓ pick · Tab complete · Enter go · Esc cancel"))
		return m.card(width, lines)
	}

	lines = append(lines, "", m.st.CardMuted.Render("Enter confirm · Esc cancel"))
	return m.card(width, lines)
}

// card renders a card, padding every line to the content width so nothing wraps
// and the box stays rectangular.
func (m *Model) card(width int, lines []string) string {
	content := cardContent(width)
	padded := make([]string, 0, len(lines))
	for _, line := range lines {
		padded = append(padded, padVisible(ansi.Truncate(line, content, "…"), content))
	}
	return m.st.Card.Width(width).Render(strings.Join(padded, "\n"))
}

// renderStaged is the review screen for the files marked for copying: what is
// marked, where it is going, and the rule for a file that is already there.
func (m *Model) renderStaged(l layout) string {
	width := cardWidth(l, 54, 82)
	content := cardContent(width)

	verb := "copying"
	if m.staged.Cut {
		verb = "moving"
	}
	lines := []string{m.st.CardTitle.Render("Marked for " + verb), ""}

	entries := m.stagedEntries()
	limit := min(10, len(entries))
	for _, entry := range entries[:limit] {
		lines = append(lines, m.row(entry.Name, humanSize(entry.Size), content))
	}
	if len(entries) > limit {
		lines = append(lines, m.st.CardMuted.Render(fmt.Sprintf("  … %d more", len(entries)-limit)))
	}

	from, to := m.stagedRoute()
	lines = append(lines, "",
		m.st.StatusMuted.Render(padRight("from", 6))+m.st.CardText.Render(truncate(from, content-6)),
		m.st.StatusMuted.Render(padRight("to", 6))+m.st.CardText.Render(truncate(to, content-6)),
		m.st.StatusMuted.Render(padRight("size", 6))+m.st.CardText.Render(humanSize(m.stagedBytes())),
		"",
		m.st.CardMuted.Render(truncate("A file already there is replaced only if the source is newer.", content)),
		"",
		m.st.CardMuted.Render("Enter "+verb+" · u unmark · Esc close"),
	)
	return m.card(width, lines)
}

// row lays a name and a value out at the two ends of one line.
func (m *Model) row(name, value string, content int) string {
	width := max(1, content-lipgloss.Width(value)-1)
	return padRight(name, width) + " " + m.st.StatusMuted.Render(value)
}

// paletteItems is the palette list, filtered and sorted by match quality.
func (m *Model) paletteItems() []actions.Action {
	all := m.reg.Palette()
	query := m.palette.input.Value
	if query == "" {
		return all
	}
	type scored struct {
		a     actions.Action
		score int
	}
	matches := make([]scored, 0, len(all))
	for _, a := range all {
		best := -1 << 30
		if s, ok := fuzzyMatch(a.Label, query); ok {
			best = s
		}
		if s, ok := fuzzyMatch(string(a.ID), query); ok && s > best {
			best = s
		}
		if s, ok := fuzzyMatch(a.Desc, query); ok && s/2 > best {
			best = s / 2
		}
		if s, ok := fuzzyMatch(string(a.Category), query); ok && s/2 > best {
			best = s / 2
		}
		if best > -1<<29 {
			matches = append(matches, scored{a, best})
		}
	}
	// Insertion sort by descending score: stable, and the list is small.
	for i := 1; i < len(matches); i++ {
		for j := i; j > 0 && matches[j].score > matches[j-1].score; j-- {
			matches[j], matches[j-1] = matches[j-1], matches[j]
		}
	}
	out := make([]actions.Action, 0, len(matches))
	for _, s := range matches {
		out = append(out, s.a)
	}
	return out
}

// menuItems is the context menu: applicable actions, destructive ones last.
func (m *Model) menuItems() []actions.Action {
	ctx := m.actionContext()
	applicable := m.reg.Applicable(ctx)
	out := make([]actions.Action, 0, len(applicable))
	for _, a := range applicable {
		if !a.Danger {
			out = append(out, a)
		}
	}
	for _, a := range applicable {
		if a.Danger {
			out = append(out, a)
		}
	}
	return out
}

// helpItems is the help list, filtered by the search box.
func (m *Model) helpItems() []actions.Action {
	all := m.reg.Palette()
	query := m.help.input.Value
	if query == "" {
		return all
	}
	out := make([]actions.Action, 0, len(all))
	for _, a := range all {
		if _, ok := fuzzyMatch(a.Label, query); ok {
			out = append(out, a)
			continue
		}
		if _, ok := fuzzyMatch(string(a.Category), query); ok {
			out = append(out, a)
		}
	}
	return out
}

// settingsOptions is the theme list for the settings card.
func (m *Model) settingsOptions() []string { return m.themeNames }
