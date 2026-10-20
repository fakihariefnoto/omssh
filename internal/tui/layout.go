package tui

// Breakpoints from UX.md §12. They are named so the rendering code reads like
// the documentation.
const (
	// wideWidth: two panes, sidebar, preview, every metadata column.
	wideWidth = 140
	// mediumWidth: two panes, sidebar collapsed to nothing, preview on demand.
	mediumWidth = 100
	// narrowWidth: one pane at a time, name and size only.
	narrowWidth = 80
	// minHeight below which the chrome is dropped rather than truncated.
	minHeight = 8
)

// layout is the measured result of a resize: every size the renderers need,
// computed once.
type layout struct {
	Width  int
	Height int

	// Chrome visibility.
	Tabs      bool
	Sidebar   bool
	ActionBar bool
	// StagedBar is the marked-files line: it exists only while something is
	// marked, and the panes give up the row rather than overflowing the screen.
	StagedBar bool
	Compact   bool

	// SidebarWidth is zero when the sidebar is hidden.
	SidebarWidth int
	// Panes is 1…4.
	Panes int
	// Grid is true when the panes are laid out two by two instead of side by
	// side. It is what makes four sessions readable at once.
	Grid bool
	// PaneWidth is the width of one pane, borders included.
	PaneWidth int
	// PaneRows is how many listing rows fit inside a bordered pane.
	PaneRows int
	// PreviewWidth is the width of the preview half of the focused pane, zero
	// when there is no room.
	PreviewWidth int
	// ActionBarRows is 1 normally, and 1 even when collapsed (the hint line).
	ActionBarRows int
}

// measure computes the layout for a terminal size.
func (m *Model) measure() layout {
	width := max(m.width, 20)
	height := max(m.height, 4)

	l := layout{Width: width, Height: height}

	// Vertical chrome, most important last: the status bar is never dropped.
	availableRows := height
	if availableRows >= minHeight {
		l.Tabs = true
		availableRows--
	}
	if m.sidebar && width >= wideWidth && availableRows > 6 {
		l.Sidebar = true
		l.SidebarWidth = clamp(width/5, 18, 30)
	}
	if m.screen == screenFiles && m.stagedCount() > 0 && availableRows > 8 {
		l.StagedBar = true
		availableRows--
	}
	l.ActionBarRows = 1
	l.ActionBar = availableRows > 6
	if l.ActionBar {
		availableRows -= l.ActionBarRows
	}
	availableRows-- // status bar

	switch {
	case width >= wideWidth:
		// The dashboard's promise: up to four sessions at once, two by two.
		l.Panes = min(len(m.Panes), 4)
		l.Grid = l.Panes > 2
	case width >= narrowWidth:
		l.Panes = min(len(m.Panes), 2)
		l.Compact = true
	default:
		l.Panes = 1
		l.Compact = true
	}
	if width >= mediumWidth && width < narrowWidth {
		l.Compact = false
	}

	body := max(availableRows, 1)
	paneArea := width
	if l.Sidebar {
		paneArea -= l.SidebarWidth + 1 // the gap between the sidebar and the panes
	}
	if l.Panes >= 2 {
		l.PaneWidth = (paneArea - 1) / 2 // one cell of air between the panes
	} else {
		l.PaneWidth = paneArea
	}
	l.PaneWidth = max(l.PaneWidth, 12)
	if l.Grid {
		// Each block is a breadcrumb, a top border, the rows, a bottom border:
		// rows+3. Two of them plus one row of air is the body.
		l.PaneRows = max(3, (body-7)/2)
		// The preview still sits to the right of the focused listing; four panes
		// are about seeing four listings at once, not about losing the preview.
		l.PreviewWidth = previewWidthFor(l.PaneWidth, m.preview)
		return l
	}
	// Two rows are lost to the border and the breadcrumb line.
	l.PaneRows = max(body-3, 1)

	// The preview needs real room: it is only shown when the listing keeps
	// enough width for names to stay readable beside it. A preview that turns
	// every name into "n…" is worse than no preview.
	if width >= mediumWidth {
		l.PreviewWidth = previewWidthFor(l.PaneWidth, m.preview)
	}
	return l
}

// previewWidthFor is how much of a pane the preview may take, or zero when the
// listing would be squeezed too far. The old threshold of 70 meant a preview
// vanished for anyone with the sidebar open at a normal width; 52 keeps it, with
// two fifths of the pane rather than more.
func previewWidthFor(paneWidth int, wanted bool) int {
	if !wanted || paneWidth < 52 {
		return 0
	}
	return max(18, paneWidth*2/5)
}

// paneRows is how many listing rows are visible, used for paging.
func (m *Model) paneRows() int {
	rows := m.measure().PaneRows
	if rows <= 0 {
		return 1
	}
	return rows
}
