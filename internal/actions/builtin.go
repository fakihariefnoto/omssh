package actions

// fileSelected reports whether the focused pane has a selection to operate on.
func fileSelected(ctx Context) bool { return ctx.Cursor || ctx.Selection > 0 }

// always is the predicate for actions that apply whenever a pane has focus.
func always(Context) bool { return true }

// onScreen scopes an action to one top-level screen.
func onScreen(name string) func(Context) bool {
	return func(ctx Context) bool { return ctx.Screen == name }
}

// onScreenWithCursor is onScreen plus "the list has a highlighted row".
func onScreenWithCursor(name string) func(Context) bool {
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

// Default returns the built-in registry. The bindings here are the "standard"
// preset from docs/UX.md §7; the vim preset and the user's overrides are layered
// on top in package keymap.
func Default() *Registry {
	return New(standardPreset()...)
}

func standardPreset() []Action {
	return []Action{
		// ---- Navigation ------------------------------------------------------
		{ID: "nav.up", Label: "Move up", Category: CatNavigation, Default: "up",
			Desc: "Move the cursor up one entry", Enabled: f(always)},
		{ID: "nav.down", Label: "Move down", Category: CatNavigation, Default: "down",
			Desc: "Move the cursor down one entry", Enabled: f(always)},
		{ID: "nav.parent", Label: "Go to parent", Category: CatNavigation, Default: "left",
			Desc: "Open the parent directory", Enabled: f(func(ctx Context) bool { return ctx.Local || ctx.Connected })},
		{ID: "nav.open", Label: "Open", Category: CatNavigation, Default: "right", Keys: []string{"enter"},
			Desc: "Enter a directory, or view a file", Enabled: f(func(ctx Context) bool { return ctx.Cursor || ctx.Panes > 0 })},
		{ID: "nav.page-up", Label: "Page up", Category: CatNavigation, Default: "pgup",
			Desc: "Move the cursor one page up", Enabled: f(always)},
		{ID: "nav.page-down", Label: "Page down", Category: CatNavigation, Default: "pgdown",
			Desc: "Move the cursor one page down", Enabled: f(always)},
		{ID: "nav.first", Label: "Go to first", Category: CatNavigation, Default: "home",
			Desc: "Move the cursor to the first entry", Enabled: f(always)},
		{ID: "nav.last", Label: "Go to last", Category: CatNavigation, Default: "end",
			Desc: "Move the cursor to the last entry", Enabled: f(always)},
		{ID: "nav.home", Label: "Go home", Category: CatNavigation,
			Desc: "Jump to the home directory of this pane", Enabled: f(always)},
		{ID: "nav.last-dir", Label: "Last directory", Category: CatNavigation,
			Desc: "Jump back to the previous directory, like cd -", Enabled: f(always)},
		{ID: "nav.back", Label: "History back", Category: CatNavigation, Default: "alt+left",
			Desc: "Walk back through visited directories", Enabled: f(always)},
		{ID: "nav.forward", Label: "History forward", Category: CatNavigation, Default: "alt+right",
			Desc: "Walk forward through visited directories", Enabled: f(always)},
		{ID: "nav.path", Label: "Edit path", Category: CatNavigation, Default: "ctrl+l",
			Desc: "Edit the breadcrumb as a path, with completion", Enabled: f(always)},
		{ID: "nav.refresh", Label: "Refresh", Category: CatNavigation, Default: "ctrl+r",
			Desc: "Reload the current directory listing", Enabled: f(always)},
		{ID: "nav.jump", Label: "Jump to path", Category: CatNavigation, Default: "ctrl+p",
			Desc: "Fuzzy-find a directory by path", Enabled: f(always)},
		{ID: "nav.find", Label: "Find in subtree", Category: CatNavigation, Default: "alt+f",
			Desc: "Search recursively below the cursor", Enabled: f(always)},
		{ID: "nav.filter", Label: "Filter listing", Category: CatNavigation, Default: "ctrl+f",
			Desc: "Narrow the listing as you type", Enabled: f(always)},
		{ID: "nav.filter-next", Label: "Next match", Category: CatNavigation,
			Desc: "Jump to the next entry matching the filter", Enabled: f(always)},
		{ID: "nav.filter-prev", Label: "Previous match", Category: CatNavigation,
			Desc: "Jump to the previous entry matching the filter", Enabled: f(always)},
		{ID: "nav.hidden", Label: "Toggle hidden files", Category: CatNavigation, Default: "alt+h",
			Desc: "Show or hide dotfiles and ignored directories", Enabled: f(always)},
		{ID: "nav.preview", Label: "Toggle preview", Category: CatNavigation, Default: "alt+p",
			Desc: "Show or hide the preview pane", Enabled: f(always)},
		{ID: "nav.switch-pane", Label: "Switch pane", Category: CatNavigation, Default: "tab",
			Desc: "Move focus to the other pane", Enabled: f(always)},
		{ID: "nav.switch-pane-prev", Label: "Switch pane (back)", Category: CatNavigation, Default: "shift+tab",
			Desc: "Move focus to the previous pane", Enabled: f(always)},
		{ID: "nav.reveal", Label: "Reveal in other pane", Category: CatNavigation, Default: "alt+o",
			Desc: "Open the cursor in the other pane", Enabled: f(func(ctx Context) bool { return ctx.Panes > 1 })},
		{ID: "nav.send", Label: "Send to other pane", Category: CatNavigation,
			Desc:    "Copy the selection to the other pane, the way the action bar does",
			Enabled: f(func(ctx Context) bool { return ctx.Panes > 1 && fileSelected(ctx) })},
		{ID: "nav.fetch", Label: "Fetch from other pane", Category: CatNavigation,
			Desc:    "Copy from the other pane into this one",
			Enabled: func(ctx Context) bool { return ctx.Overlay == "" && !ctx.Terminal && ctx.Jobs >= 0 }},

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
		// Esc is one key with layered meaning (dismiss, clear, go back), so the
		// registry binds it once, to app.back: clearing a selection is what
		// app.back does when there is a selection. This entry exists for the
		// palette and the context menu.
		{ID: "sel.clear", Label: "Clear selection", Category: CatSelection,
			Desc:    "Drop the selection without leaving the pane",
			Enabled: func(ctx Context) bool { return ctx.Overlay == "" && !ctx.Terminal && ctx.Selection > 0 }},

		// ---- File operations -------------------------------------------------
		{ID: "file.view", Label: "View", Category: CatFile, Default: "f3",
			Desc: "Preview the entry; archives list their contents", Enabled: f(func(ctx Context) bool { return ctx.Cursor })},
		{ID: "file.edit", Label: "Edit", Category: CatFile, Default: "f4",
			Desc:    "Open in $EDITOR; remote files are fetched, edited and sent back",
			Enabled: f(func(ctx Context) bool { return ctx.Cursor && !ctx.CursorIsDir })},
		{ID: "file.rename", Label: "Rename", Category: CatFile, Default: "f2",
			Desc: "Rename the entry, or batch-rename a selection", Enabled: f(fileSelected)},
		{ID: "file.copy-to", Label: "Copy to…", Category: CatFile, Default: "f5",
			Desc: "Copy the selection, choosing the destination", Enabled: f(fileSelected)},
		{ID: "file.move-to", Label: "Move to…", Category: CatFile, Default: "f6",
			Desc: "Move the selection, choosing the destination", Enabled: f(fileSelected)},
		{ID: "file.copy", Label: "Copy", Category: CatFile, Default: "ctrl+c",
			Desc: "Stage the selection for pasting", Enabled: f(fileSelected)},
		{ID: "file.cut", Label: "Cut", Category: CatFile, Default: "ctrl+x",
			Desc: "Stage the selection to be moved on paste", Enabled: f(fileSelected)},
		{ID: "file.paste", Label: "Paste", Category: CatFile, Default: "ctrl+v",
			Desc: "Paste staged entries into this pane", Enabled: f(func(ctx Context) bool { return ctx.Staged > 0 })},
		{ID: "file.mkdir", Label: "New folder", Category: CatFile, Default: "f7",
			Desc: "Create a directory in this pane", Enabled: f(always)},
		{ID: "file.new", Label: "New file", Category: CatFile, Default: "ctrl+n",
			Desc: "Create an empty file in this pane", Enabled: f(always)},
		{ID: "file.delete", Label: "Delete", Category: CatFile, Default: "f8", Keys: []string{"del"}, Danger: true,
			Desc: "Delete the selection; always asks first", Enabled: f(fileSelected)},
		{ID: "file.chmod", Label: "Change permissions", Category: CatFile, Default: "",
			Desc: "Change mode bits on the selection", Enabled: f(fileSelected)},
		{ID: "file.symlink", Label: "Create symlink", Category: CatFile, Default: "",
			Desc: "Create a symbolic link pointing at the cursor", Enabled: f(func(ctx Context) bool {
				return ctx.Cursor && (ctx.Local || ctx.Connected)
			})},
		{ID: "file.menu", Label: "All file actions", Category: CatFile, Default: "f9",
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
		{ID: "session.connect", Label: "Connect", Category: CatSession, Default: "c",
			Desc: "Open a session to the highlighted host", Enabled: onScreenWithCursor("connections")},
		{ID: "session.add", Label: "Add connection", Category: CatSession, Default: "a",
			Desc: "Add a host block to ~/.ssh/config", Enabled: onScreen("connections")},
		{ID: "session.edit", Label: "Edit connection", Category: CatSession, Default: "e",
			Desc: "Edit the highlighted host", Enabled: onScreenWithCursor("connections")},
		{ID: "session.delete", Label: "Delete connection", Category: CatSession, Default: "d", Danger: true,
			Desc: "Remove the highlighted host from ~/.ssh/config", Enabled: onScreenWithCursor("connections")},
		{ID: "session.reload", Label: "Reload hosts", Category: CatSession, Default: "r",
			Desc: "Re-read ~/.ssh/config from disk", Enabled: onScreen("connections")},
		{ID: "session.open-config", Label: "Edit ~/.ssh/config", Category: CatSession, Default: "o",
			Desc: "Open the file in $EDITOR", Enabled: onScreen("connections")},
		{ID: "session.new-tab", Label: "New tab here", Category: CatSession, Default: "ctrl+t",
			Desc:    "Open a terminal tab in this directory",
			Enabled: f(func(ctx Context) bool { return ctx.Local || ctx.Connected })},
		{ID: "session.close-tab", Label: "Close tab", Category: CatSession, Default: "ctrl+w",
			Desc: "Close the current tab", Enabled: f(always)},
		{ID: "session.tab", Label: "Switch tab", Category: CatSession,
			Keys: []string{"alt+1", "alt+2", "alt+3", "alt+4", "alt+5", "alt+6", "alt+7", "alt+8", "alt+9"},
			Desc: "Jump to a session tab by number",
			Enabled: func(Context) bool {
				return true
			}},
		{ID: "session.tab-next", Label: "Next tab", Category: CatSession, Default: "ctrl+pgdown",
			Desc: "Move to the next session tab", Enabled: always},
		{ID: "session.tab-prev", Label: "Previous tab", Category: CatSession, Default: "ctrl+pgup",
			Desc: "Move to the previous session tab", Enabled: always},
		{ID: "terminal.new", Label: "New terminal", Category: CatSession, Default: "ctrl+space n",
			Desc:    "Open another terminal tab in this session",
			Enabled: f(func(ctx Context) bool { return ctx.Connected || ctx.Local })},
		{ID: "terminal.close", Label: "Close terminal", Category: CatSession, Default: "ctrl+space w",
			Desc: "Close the focused terminal tab", Enabled: func(ctx Context) bool { return ctx.Terminal }},
		{ID: "terminal.copy-mode", Label: "Terminal copy mode", Category: CatSession, Default: "ctrl+space [",
			Desc: "Select and copy text from the terminal buffer", Enabled: func(ctx Context) bool { return ctx.Terminal }},
		{ID: "terminal.leave", Label: "Leave terminal", Category: CatSession, Default: "ctrl+space q",
			Desc: "Return to the file panes, leaving the session alive", Enabled: func(ctx Context) bool { return ctx.Terminal }},

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
		{ID: "deploy.history", Label: "Deploy history", Category: CatDeploy,
			Desc:    "Inspect and re-run past deploys",
			Enabled: func(ctx Context) bool { return ctx.Connected }},

		// ---- View toggles ----------------------------------------------------
		{ID: "view.sidebar", Label: "Toggle sidebar", Category: CatView, Default: "alt+b",
			Desc: "Show or hide hosts, bookmarks and jobs", Enabled: always},
		{ID: "view.jobs", Label: "Toggle job queue", Category: CatView, Default: "alt+j",
			Desc: "Expand the transfer queue", Enabled: always},
		{ID: "view.follow-background", Label: "Follow terminal background", Category: CatView,
			Desc:    "Ask the terminal whether it is light or dark and pick the theme to match",
			Enabled: func(Context) bool { return true }},
		{ID: "view.compare", Label: "Compare panes", Category: CatView, Default: "alt+c",
			Desc: "Show a read-only diff of the two panes", Enabled: f(func(ctx Context) bool { return ctx.Panes > 1 })},
		{ID: "view.sync", Label: "Toggle sync browse", Category: CatView, Default: "alt+s",
			Desc: "Keep both panes on the same relative path", Enabled: f(func(ctx Context) bool { return ctx.Panes > 1 })},

		// ---- Application -----------------------------------------------------
		{ID: "app.files", Label: "Browse local files", Category: CatApp,
			Desc: "Leave the launcher and open the local file manager", Enabled: always},
		{ID: "app.home", Label: "Home screen", Category: CatApp,
			Desc: "Return to the launcher", Enabled: always},
		{ID: "app.palette", Label: "Command palette", Category: CatApp, Default: "ctrl+k",
			Desc: "Find any action by name, with its shortcut", Enabled: always},
		{ID: "app.help", Label: "Help", Category: CatApp, Default: "f1", Keys: []string{"?"},
			Desc: "Searchable keymap, grouped by task", Enabled: always},
		{ID: "app.settings", Label: "Settings", Category: CatApp, Default: "ctrl+,",
			Desc: "Change theme, icons, motion and keymap", Enabled: always},
		{ID: "app.tour", Label: "Tour", Category: CatApp,
			Desc: "Replay the six-line overview", Enabled: always},
		{ID: "app.onboarding", Label: "Import hosts from ~/.ssh/config", Category: CatApp,
			Desc:    "Add the connections already in your SSH config",
			Enabled: always},
		{ID: "app.back", Label: "Back", Category: CatApp, Default: "esc",
			Desc: "Dismiss, cancel or go up one level; never quits", Enabled: always},
		{ID: "app.quit", Label: "Quit", Category: CatApp, Default: "ctrl+q", Keys: []string{"f10"},
			Desc: "Leave omssh, asking first when jobs are running", Enabled: always},
		{ID: "app.version", Label: "Version", Category: CatApp,
			Desc: "Show build information", Enabled: always},
	}
}
