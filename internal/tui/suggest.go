package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// pathSuggestions lists folders that continue what the user has typed in the
// "go to a folder" prompt.
//
// Only directories are offered: the prompt's destination is a folder, so
// suggesting files would be noise. Hidden folders appear only when the user has
// already typed a dot, which is the convention that makes dotfiles reachable
// without filling the list.
func pathSuggestions(value string, limit int) []string {
	home, _ := userHome()

	var dir, base string
	switch {
	case value == "" || value == "~":
		dir, base = home, ""
	case strings.HasSuffix(value, "/"):
		dir, base = expandHome(value), ""
	default:
		dir, base = filepath.Split(expandHome(value))
		if dir == "" {
			dir = "."
		}
	}
	if dir == "" {
		dir = "/"
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	needle := strings.ToLower(base)
	showHidden := strings.HasPrefix(base, ".")

	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !showHidden && strings.HasPrefix(name, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), needle) {
			continue
		}
		if !isDirEntry(filepath.Join(dir, name), entry) {
			continue
		}
		// A trailing slash marks it as a directory and keeps the next keystroke
		// descending into it.
		out = append(out, contractHome(filepath.Join(dir, name))+"/")
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// isDirEntry follows symlinks, because a symlinked folder is still a folder.
func isDirEntry(path string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// expandHome turns a leading ~ into the home directory.
func expandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := userHome()
	if err != nil {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
}

// contractHome is the inverse, so suggestions read the way they were typed.
func contractHome(path string) string {
	home, err := userHome()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~/" + strings.TrimPrefix(path, home+string(filepath.Separator))
	}
	return path
}
