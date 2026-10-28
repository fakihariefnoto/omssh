package actions

// The keymap is one map, not a set of presets: plain letters are commands, the
// way ranger, lf and every file manager in a terminal has always done it. Typing
// to find a file is "/" (filter) and Ctrl+F, because a letter that sometimes
// navigates and sometimes types is a letter you cannot learn.
//
// Grammar:
//
//	plain letters   a command (h j k l navigate, d delete, r rename, …)
//	digits          switch to that session tab
//	Space           select
//	Shift+arrows    extend the selection
//	Ctrl+letter     an action with a dialog (copy, paste, extract, palette, quit)
//	Alt+letter      a view toggle (sidebar, jobs, staged, deploy)
//	Esc             back, never quit
//
// Function keys survive only as aliases, for people whose hands already know
// them; nothing in the interface advertises them.

// Default returns the built-in registry: one letter-keyed map, the same on every
// machine.
func Default() *Registry {
	return New(standardPreset()...)
}

// standardPreset is the one map. The name is historical.
func standardPreset() []Action {
	const connections = "connections"

	return []Action{
		// ---- Navigation ------------------------------------------------------
		{ID: "nav.up", Label: "Move up", Category: CatNavigation, Default: "k", Keys: []string{"up"},
			Desc: "Move the cursor up one entry", Enabled: f(always)},
		{ID: "nav.down", Label: "Move down", Category: CatNavigation, Default: "j", Keys: []string{"down"},
			Desc: "Move the cursor down one entry", Enabled: f(always)},
		{ID: "nav.parent", Label: "Go to parent", Category: CatNavigation, Default: "h", Keys: []string{"left", "backspace"},
			Desc: "Open the parent directory", Enabled: f(func(ctx Context) bool { return ctx.Local || ctx.Connected })},
		{ID: "nav.open", Label: "Open", Category: CatNavigation, Default: "l", Keys: []string{"right", "enter"},
			Desc: "Enter a directory, or view a file", Enabled: f(func(ctx Context) bool { return ctx.Cursor || ctx.Panes > 0 })},
		{ID: "nav.page-up", Label: "Page up", Category: CatNavigation, Default: "pgup",
			Desc: "Move the cursor one page up", Enabled: f(always)},
		{ID: "nav.page-down", Label: "Page down", Category: CatNavigation, Default: "pgdown",
			Desc: "Move the cursor one page down", Enabled: f(always)},
		{ID: "nav.first", Label: "Go to first", Category: CatNavigation, Default: "g g", Keys: []string{"home"},
			Desc: "Move the cursor to the first entry", Enabled: f(always)},
		{ID: "nav.last", Label: "Go to last", Category: CatNavigation, Default: "G", Keys: []string{"end"},
			Desc: "Move the cursor to the last entry", Enabled: f(always)},
		{ID: "nav.home", Label: "Go home", Category: CatNavigation, Default: "~",
			Desc: "Jump to the home directory of this pane", Enabled: f(always)},
		{ID: "nav.last-dir", Label: "Last directory", Category: CatNavigation, Default: "-",
			Desc: "Jump back to the previous directory, like cd -", Enabled: f(always)},
		{ID: "nav.back", Label: "History back", Category: CatNavigation, Default: "alt+left",
			Desc: "Walk back through visited directories", Enabled: f(always)},
		{ID: "nav.forward", Label: "History forward", Category: CatNavigation, Default: "alt+right",
			Desc: "Walk forward through visited directories", Enabled: f(always)},
		{ID: "nav.path", Label: "Edit path", Category: CatNavigation, Default: "ctrl+l",
			Desc: "Edit the breadcrumb as a path, with folder suggestions", Enabled: f(always)},
		{ID: "nav.refresh", Label: "Refresh", Category: CatNavigation, Default: "ctrl+r",
			Desc: "Reload the current directory listing", Enabled: f(always)},
		{ID: "nav.jump", Label: "Jump to path", Category: CatNavigation, Default: "ctrl+p",
			Desc: "Fuzzy-find a directory by path", Enabled: f(always)},
		{ID: "nav.find", Label: "Find in subtree", Category: CatNavigation, Default: "alt+f",
			Desc: "Search recursively below the cursor", Enabled: f(always)},
		{ID: "nav.filter", Label: "Filter listing", Category: CatNavigation, Default: "/", Keys: []string{"ctrl+f"},
			Desc: "Narrow the listing as you type", Enabled: f(always)},
		{ID: "nav.hidden", Label: "Toggle hidden files", Category: CatNavigation, Default: ".",
			Desc: "Show or hide dotfiles and ignored directories", Enabled: f(always)},
		{ID: "nav.preview", Label: "Toggle preview", Category: CatNavigation, Default: "alt+p",
			Desc: "Show or hide the preview pane", Enabled: f(always)},
		{ID: "nav.switch-pane", Label: "Switch pane", Category: CatNavigation, Default: "w",
			Desc: "Move focus to the other pane", Enabled: f(always)},
		{ID: "nav.switch-pane-prev", Label: "Switch pane (back)", Category: CatNavigation,
			Desc: "Move focus to the previous pane", Enabled: f(always)},
		{ID: "nav.reveal", Label: "Reveal in other pane", Category: CatNavigation, Default: "o",
			Desc: "Open the cursor in the other pane", Enabled: f(func(ctx Context) bool { return ctx.Panes > 1 })},
		{ID: "nav.send", Label: "Send to other pane", Category: CatNavigation,
			Desc:    "Copy the selection to the other pane, the way the action bar does",
			Enabled: f(func(ctx Context) bool { return ctx.Panes > 1 && fileSelected(ctx) })},
		{ID: "nav.fetch", Label: "Fetch from other pane", Category: CatNavigation,
			Desc:    "Copy from the other pane into this one",
			Enabled: func(ctx Context) bool { return ctx.Overlay == "" && !ctx.Terminal }},

		// ---- Selection -------------------------------------------------------
		{ID: "sel.toggle", Label: "Toggle selection", Category: CatSelection, Default: "space",
			Desc: "Select the entry under the cursor and advance", Enabled: f(func(ctx Context) bool { return ctx.Cursor })},
		{ID: "sel.extend-up", Label: "Extend selection up", Category: CatSelection, Default: "shift+up",
			Desc: "Extend the selection upwards", Enabled: f(always)},
		{ID: "sel.extend-down", Label: "Extend selection down", Category: CatSelection, Default: "shift+down",
			Desc: "Extend the selection downwards", Enabled: f(always)},
		{ID: "sel.all", Label: "Select all", Category: CatSelection, Default: "ctrl+a",
			Desc: "Select every entry in the pane", Enabled: f(always)},
		{ID: "sel.invert", Label: "Invert selection", Category: CatSelection, Default: "alt+a",
			Desc: "Select what is not selected and vice versa", Enabled: f(always)},
		{ID: "sel.mark", Label: "Copy", Category: CatSelection, Default: "y", Keys: []string{"ctrl+c"},
			Desc:    "Mark the selection to be copied to the other pane",
			Enabled: f(fileSelected)},
		{ID: "sel.cut", Label: "Move", Category: CatSelection, Default: "x", Keys: []string{"ctrl+x"},
			Desc:    "Mark the selection to be moved to the other pane",
			Enabled: f(fileSelected)},
		{ID: "sel.paste", Label: "Paste", Category: CatSelection, Default: "p", Keys: []string{"ctrl+v"},
			Desc:    "Copy or move the marked files into this pane",
			Enabled: f(func(ctx Context) bool { return ctx.Staged > 0 })},
		{ID: "sel.paste-options", Label: "Paste options…", Category: CatSelection, Default: "A",
			Desc:    "Choose copy or move, what to do about conflicts, and whether to unpack an archive",
			Enabled: f(func(ctx Context) bool { return ctx.Staged > 0 })},
		{ID: "sel.clear", Label: "Clear selection", Category: CatSelection,
			Desc:    "Drop the selection without leaving the pane",
			Enabled: func(ctx Context) bool { return ctx.Overlay == "" && !ctx.Terminal && ctx.Selection > 0 }},

		// ---- File operations -------------------------------------------------
		{ID: "file.view", Label: "View", Category: CatFile, Default: "v", Keys: []string{"f3"},
			Desc: "Preview the entry; archives list their contents", Enabled: f(func(ctx Context) bool { return ctx.Cursor })},
		{ID: "file.edit", Label: "Edit", Category: CatFile, Default: "e", Keys: []string{"f4"},
			Desc:    "Open in $EDITOR; remote files are fetched, edited and sent back",
			Enabled: f(func(ctx Context) bool { return ctx.Cursor && !ctx.CursorIsDir })},
		{ID: "file.rename", Label: "Rename", Category: CatFile, Default: "r", Keys: []string{"f2"},
			Desc: "Rename the entry, or batch-rename a selection", Enabled: f(fileSelected)},
		{ID: "file.new", Label: "New file", Category: CatFile, Default: "n", Keys: []string{"ctrl+n"},
			Desc: "Create an empty file in this pane", Enabled: f(always)},
		{ID: "file.mkdir", Label: "New folder", Category: CatFile, Default: "N", Keys: []string{"f7"},
			Desc: "Create a directory in this pane", Enabled: f(always)},
		{ID: "file.delete", Label: "Delete", Category: CatFile, Default: "d", Keys: []string{"ctrl+d", "f8", "del"}, Danger: true,
			Desc: "Delete the selection; always asks first", Enabled: f(fileSelected)},
		{ID: "file.copy-to", Label: "Copy to…", Category: CatFile, Default: "C",
			Desc: "Copy the selection, choosing the destination", Enabled: f(fileSelected)},
		{ID: "file.move-to", Label: "Move to…", Category: CatFile, Default: "M",
			Desc: "Move the selection, choosing the destination", Enabled: f(fileSelected)},
		{ID: "file.link", Label: "Copy link", Category: CatFile, Default: "u",
			Desc: "Make a temporary download URL for the object and copy it", Enabled: f(func(ctx Context) bool {
				return ctx.Cursor && !ctx.CursorIsDir
			})},
		{ID: "file.chmod", Label: "Change permissions", Category: CatFile, Default: "P",
			Desc: "Change mode bits on the selection", Enabled: f(fileSelected)},
		{ID: "file.symlink", Label: "Create symlink", Category: CatFile, Default: "L",
			Desc: "Create a symbolic link pointing at the cursor", Enabled: f(func(ctx Context) bool {
				return ctx.Cursor && (ctx.Local || ctx.Connected)
			})},
		{ID: "file.menu", Label: "All file actions", Category: CatFile, Default: "m",
			Desc: "Open the context menu for the selection", Enabled: f(always)},

		// ---- Archives --------------------------------------------------------
		{ID: "archive.extract", Label: "Extract…", Category: CatArchive, Default: "ctrl+e",
			Desc:    "Extract an archive to a directory on either side",
			Enabled: f(func(ctx Context) bool { return ctx.CursorIsArchive })},
		{ID: "archive.extract-here", Label: "Extract here", Category: CatArchive,
			Desc:    "Extract into the current directory of this pane",
			Enabled: f(func(ctx Context) bool { return ctx.CursorIsArchive })},
		{ID: "archive.compress", Label: "Compress…", Category: CatArchive, Default: "ctrl+g",
			Desc: "Compress the selection into an archive", Enabled: f(fileSelected)},
		{ID: "archive.contents", Label: "List archive contents", Category: CatArchive,
			Desc:    "Show what an archive holds without extracting it",
			Enabled: f(func(ctx Context) bool { return ctx.CursorIsArchive })},

		// ---- Sessions and terminals -----------------------------------------
		{ID: "session.open", Label: "SSH connections", Category: CatSession, Default: "ctrl+o",
			Desc: "Manage the hosts in ~/.ssh/config", Enabled: always},
		{ID: "session.connect", Label: "Connect", Category: CatSession, Default: "c", Screen: connections,
			Desc: "Open a session to the highlighted host", Enabled: onScreenWithCursor(connections)},
		{ID: "session.add", Label: "Add connection", Category: CatSession, Default: "a", Screen: connections,
			Desc: "Add a host block to ~/.ssh/config", Enabled: onScreen(connections)},
		{ID: "session.edit", Label: "Edit connection", Category: CatSession, Default: "e", Screen: connections,
			Desc: "Edit the highlighted host", Enabled: onScreenWithCursor(connections)},
		{ID: "session.delete", Label: "Delete connection", Category: CatSession, Default: "d", Screen: connections, Danger: true,
			Desc: "Remove the highlighted host from ~/.ssh/config", Enabled: onScreenWithCursor(connections)},
		{ID: "session.reload", Label: "Reload hosts", Category: CatSession, Default: "r", Screen: connections,
			Desc: "Re-read ~/.ssh/config from disk", Enabled: onScreen(connections)},
		{ID: "session.open-config", Label: "Edit ~/.ssh/config", Category: CatSession, Default: "o", Screen: connections,
			Desc: "Open the file in $EDITOR", Enabled: onScreen(connections)},
		{ID: "session.new-tab", Label: "New tab here", Category: CatSession, Default: "ctrl+t",
			Desc:    "Open a terminal tab in this directory",
			Enabled: f(func(ctx Context) bool { return ctx.Local || ctx.Connected })},
		{ID: "session.disconnect", Label: "Disconnect", Category: CatSession, Default: "Q",
			Danger:  true,
			Desc:    "Close the connection but keep its tab; choosing the tab reconnects",
			Enabled: f(func(ctx Context) bool { return ctx.Connected || ctx.Screen == "connections" })},
		{ID: "session.close-tab", Label: "Close tab", Category: CatSession, Default: "ctrl+w",
			Desc: "Close the current tab", Enabled: f(always)},
		{ID: "session.tab", Label: "Switch tab", Category: CatSession,
			Keys: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"},
			Desc: "Jump to a session tab by its number",
			Enabled: func(Context) bool {
				return true
			}},
		{ID: "session.tab-prev", Label: "Previous tab", Category: CatSession, Default: "shift+tab", Keys: []string{"[", "ctrl+pgup"},
			Desc: "Move to the previous session tab", Enabled: func(Context) bool { return true }},
		{ID: "session.tab-next", Label: "Next tab", Category: CatSession, Default: "tab", Keys: []string{"]", "ctrl+pgdown"},
			Desc: "Move to the next session tab", Enabled: func(Context) bool { return true }},
		{ID: "session.embedded", Label: "Embedded terminal", Category: CatSession, Default: "U",
			Desc: "A terminal inside omssh, recordable and replayable; Ctrl+] detaches", Enabled: f(func(ctx Context) bool {
				return ctx.Connected
			})},
		{ID: "session.replay", Label: "Replay a recording", Category: CatSession, Default: "alt+r",
			Desc: "Play back a .cast recording", Enabled: f(always)},
		{ID: "session.recordings", Label: "Recordings", Category: CatSession, Default: "alt+l",
			Desc: "List recorded sessions", Enabled: f(always)},
		{ID: "session.tunnel", Label: "Start a tunnel", Category: CatSession, Default: "alt+t",
			Desc: "Forward a port through this host: L, R or D", Enabled: f(always)},
		{ID: "session.tunnels", Label: "Tunnels", Category: CatSession, Default: "alt+y",
			Desc: "Watch or stop running forwards", Enabled: f(always)},
		{ID: "terminal.new", Label: "Terminal", Category: CatSession, Default: "T",
			Desc:    "Open a full SSH shell on this host, and come back when you exit it",
			Enabled: f(func(ctx Context) bool { return ctx.Connected || ctx.Local })},
		{ID: "session.terminal", Label: "Open a terminal", Category: CatSession, Default: "t", Screen: connections,
			Desc:    "Open a full SSH shell on the highlighted host",
			Enabled: onScreenWithCursor(connections)},

		// ---- Deploy ----------------------------------------------------------
		{ID: "deploy.open", Label: "Deploy", Category: CatDeploy, Default: "alt+d",
			Desc:    "Open the deploy view for the focused host",
			Enabled: f(func(ctx Context) bool { return ctx.Connected })},
		{ID: "deploy.preview", Label: "Show deploy plan", Category: CatDeploy,
			Desc:    "Compute and show what a deploy would change",
			Enabled: func(ctx Context) bool { return ctx.Connected }},
		{ID: "deploy.run", Label: "Run deploy", Category: CatDeploy, Danger: true,
			Desc:    "Run the selected deploy profile",
			Enabled: func(ctx Context) bool { return ctx.Connected }},

		// ---- View toggles ----------------------------------------------------
		{ID: "view.sidebar", Label: "Toggle sidebar", Category: CatView, Default: "alt+b",
			Desc: "Show or hide hosts, bookmarks and jobs", Enabled: always},
		{ID: "view.jobs", Label: "Toggle job queue", Category: CatView, Default: "alt+j",
			Desc: "Expand the transfer queue", Enabled: always},
		{ID: "view.staged", Label: "Marked files", Category: CatView, Default: "s", Keys: []string{"alt+s"},
			Desc:    "Review what is marked for copying or moving",
			Enabled: func(ctx Context) bool { return ctx.Staged > 0 }},
		{ID: "view.panes", Label: "Choose panes", Category: CatView, Default: "V",
			Desc:    "Pick which sessions are on screen, four at most",
			Enabled: f(always)},
		{ID: "view.sync", Label: "Toggle sync browse", Category: CatView, Default: "Y",
			Desc: "Keep both panes on the same relative path", Enabled: f(func(ctx Context) bool { return ctx.Panes > 1 })},
		{ID: "view.compare", Label: "Compare panes", Category: CatView, Default: "alt+c",
			Desc: "Show a read-only diff of the two panes", Enabled: f(func(ctx Context) bool { return ctx.Panes > 1 })},
		{ID: "view.follow-background", Label: "Follow terminal background", Category: CatView,
			Desc:    "Ask the terminal whether it is light or dark and pick the theme to match",
			Enabled: always},

		// ---- Application -----------------------------------------------------
		{ID: "app.dashboard", Label: "Dashboard", Category: CatApp,
			Desc:    "Choose which places the dashboard shows",
			Enabled: func(Context) bool { return true }},
		{ID: "app.files", Label: "Browse local files", Category: CatApp,
			Desc: "Leave the launcher and open the local file manager", Enabled: always},
		{ID: "app.home", Label: "Home screen", Category: CatApp,
			Desc: "Return to the launcher", Enabled: always},
		{ID: "app.palette", Label: "Command palette", Category: CatApp, Default: "ctrl+k",
			Desc: "Find any action by name, with its shortcut", Enabled: always},
		{ID: "app.help", Label: "Help", Category: CatApp, Default: "?", Keys: []string{"f1"},
			Desc: "Searchable keymap, grouped by task", Enabled: always},
		{ID: "app.settings", Label: "Settings", Category: CatApp, Default: "ctrl+,",
			Desc: "Change theme, icons, motion and keymap", Enabled: always},
		{ID: "app.tour", Label: "Tour", Category: CatApp,
			Desc: "Replay the six-line overview", Enabled: always},
		{ID: "app.onboarding", Label: "Import hosts from ~/.ssh/config", Category: CatApp,
			Desc: "Add the connections already in your SSH config", Enabled: always},
		{ID: "app.back", Label: "Back", Category: CatApp, Default: "esc",
			Desc: "Dismiss, cancel or go up one level; never quits", Enabled: always},
		{ID: "app.quit", Label: "Quit", Category: CatApp, Default: "ctrl+q", Keys: []string{"f10"},
			Desc: "Leave omssh, asking first when jobs are running", Enabled: always},
		{ID: "app.mcp", Label: "MCP for agents", Category: CatApp,
			Desc: "Show the MCP server configuration", Enabled: f(always)},
		{ID: "app.version", Label: "Version", Category: CatApp,
			Desc: "Show build information", Enabled: always},
	}
}

// fileSelected reports whether the focused pane has a selection to operate on.
func fileSelected(ctx Context) bool { return ctx.Cursor || ctx.Selection > 0 }

// always is the predicate for actions that apply whenever a pane has focus.
func always(Context) bool { return true }

// onScreen scopes an action to one top-level screen.
func onScreen(name string) func(Context) bool {
	return func(ctx Context) bool { return ctx.Screen == name }
}

// onScreenWithCursor is onScreen plus "the list has a highlighted row".
func onScreenWithCursor(name string) func(Context) bool { //nolint:unparam // named for symmetry with onScreen
	return func(ctx Context) bool { return ctx.Screen == name && ctx.Cursor }
}

// f identifies the common case: an action available while a file pane has focus.
//
// Applicability is a question about state, not about what is on top of the
// screen: the palette must not grey out Copy merely because the palette is open.
// Keys are routed to an open overlay before the keymap is consulted, so an
// overlay never needs to suppress anything here.
func f(fn func(Context) bool) func(Context) bool {
	return func(ctx Context) bool { return !ctx.Terminal && fn(ctx) }
}
