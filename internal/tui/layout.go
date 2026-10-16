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
	Compact   bool

	// SidebarWidth is zero when the sidebar is hidden.
	SidebarWidth int
	// Panes is 1 or 2.
	Panes int
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
	l.ActionBarRows = 1
	l.ActionBar = availableRows > 6
	if l.ActionBar {
		availableRows -= l.ActionBarRows
	}
	availableRows-- // status bar

	switch {
	case width >= wideWidth:
		l.Panes = 2
	case width >= narrowWidth:
		l.Panes = 1
		l.Compact = true
	default:
		l.Panes = 1
		l.Compact = true
	}
	l.Panes = min(l.Panes, len(m.Panes))
	if width >= mediumWidth && width < narrowWidth {
		l.Compact = false
	}

	body := max(availableRows, 1)
	paneArea := width
	if l.Sidebar {
		paneArea -= l.SidebarWidth
	}
	if l.Panes == 2 {
		l.PaneWidth = (paneArea - 1) / 2 // one cell of air between the panes
	} else {
		l.PaneWidth = paneArea
	}
	l.PaneWidth = max(l.PaneWidth, 12)
	// Two rows are lost to the border and the breadcrumb line.
	l.PaneRows = max(body-3, 1)

	// The preview needs real room: it is only shown when the listing keeps
	// enough width for names to stay readable beside it. A preview that turns
	// every name into "n…" is worse than no preview.
	if m.preview && width >= mediumWidth && l.PaneWidth >= 70 {
		l.PreviewWidth = l.PaneWidth * 2 / 5
	}
	return l
}

// paneRows is how many listing rows are visible, used for paging.
func (m *Model) paneRows() int {
	rows := m.measure().PaneRows
	if rows <= 0 {
		return 1
	}
	return rows
}
