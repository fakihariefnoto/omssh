package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/config"
	"github.com/fakihariefnoto/omssh/internal/deploy"
	"github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/sshconfig"
	"github.com/fakihariefnoto/omssh/internal/vfs"
)

// A deploy is a copy with an opinion: this local tree goes to that remote tree,
// these files never move, and — in sync mode — what is not here should not be
// there. So the view is the same two-step gesture as copying: say what will
// happen, then do it.

// deployState is the deploy card's state.
type deployState struct {
	cursor  int
	profile string
	plan    *deploy.Plan
	loading bool
	applied bool
	err     string
}

// deployPlanMsg carries a finished review.
type deployPlanMsg struct {
	profile string
	plan    deploy.Plan
	err     error
}

// deployDoneMsg carries a finished deploy.
type deployDoneMsg struct {
	profile string
	plan    deploy.Plan
	err     error
}

// deployProfiles names the configured profiles, sorted, so the list has a stable
// order no matter how the map was decoded.
func (m *Model) deployProfiles() []string {
	names := make([]string, 0, len(m.cfg.Deploy.Profiles))
	for name := range m.cfg.Deploy.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// openDeploy shows the deploy card, or explains where profiles come from.
func (m *Model) openDeploy() {
	if len(m.cfg.Deploy.Profiles) == 0 {
		m.toastInfo("No deploy profiles yet · add one under deploy.profiles in the config")
		return
	}
	m.deploy = deployState{}
	m.Overlay = OverlayDeploy
	m.mode = "DEPLOY"
}

// startDeploy reviews one profile, connecting first if that host is not up yet.
func (m *Model) startDeploy(name string) tea.Cmd {
	profile, ok := m.cfg.Deploy.Profiles[name]
	if !ok {
		return nil
	}
	if sess, live := m.live[profile.Host]; live {
		return m.reviewDeploy(name, profile, sess.FS)
	}
	target, ok := m.targetForAlias(profile.Host)
	if !ok {
		m.toastError("No host named " + profile.Host + " in the interface or ~/.ssh/config")
		return nil
	}
	m.pendingDeploy = name
	m.toastInfo("Connecting to " + profile.Host + " to review the deploy…")
	return m.connect(target)
}

// targetForAlias builds a connection target from the host's ssh config entry.
func (m *Model) targetForAlias(alias string) (ssh.Target, bool) {
	if m.conn == nil {
		m.loadConnections()
	}
	if m.conn != nil {
		for _, entry := range m.conn.entries {
			if entry.Alias != alias {
				continue
			}
			target, err := ssh.TargetFrom(sshconfig.Spec{
				Alias:        entry.Alias,
				HostName:     entry.HostName,
				User:         entry.User,
				Port:         entry.Port,
				IdentityFile: entry.Identity,
				ProxyJump:    entry.ProxyJump,
			})
			return target, err == nil
		}
	}
	// Not in the config: ssh's own defaults are still a reasonable answer.
	target, err := ssh.TargetFrom(sshconfig.Spec{Alias: alias})
	return target, err == nil
}

// reviewDeploy diffs the local tree against the remote one, off the update loop.
func (m *Model) reviewDeploy(name string, profile config.Profile, remote vfs.FS) tea.Cmd {
	local, opts, err := localRootedFS(profile)
	if err != nil {
		m.toastError("Cannot read " + profile.LocalRoot + ": " + err.Error())
		return nil
	}
	m.deploy.loading = true
	m.deploy.profile = name
	m.deploy.plan = nil
	m.deploy.err = ""
	m.Overlay = OverlayDeploy

	ctx := m.connectContext()
	return func() tea.Msg {
		plan, err := deploy.Diff(ctx, local, remote, opts)
		return deployPlanMsg{profile: name, plan: plan, err: err}
	}
}

// runDeploy applies the reviewed plan.
func (m *Model) runDeploy(name string, remote vfs.FS) tea.Cmd {
	profile, ok := m.cfg.Deploy.Profiles[name]
	if !ok {
		return nil
	}
	local, opts, err := localRootedFS(profile)
	if err != nil {
		m.toastError(err.Error())
		return nil
	}
	m.deploy.loading = true
	ctx := m.connectContext()

	return func() tea.Msg {
		plan, err := deploy.Apply(ctx, local, remote, opts, nil)
		return deployDoneMsg{profile: name, plan: plan, err: err}
	}
}

// localRootedFS opens the profile's local tree as a filesystem of its own, so
// every path the engine sees is relative to that root. ~ in the config is the
// user's own home, as it is everywhere else.
func localRootedFS(profile config.Profile) (vfs.FS, deploy.Options, error) {
	root := expandHome(strings.TrimSpace(profile.LocalRoot))
	if root == "" {
		return nil, deploy.Options{}, errNoLocalRoot
	}
	remoteRoot := strings.TrimSpace(profile.RemoteRoot)
	if !strings.HasPrefix(remoteRoot, "/") {
		return nil, deploy.Options{}, destError("remote_root must be an absolute path, like /var/www/site")
	}
	local, err := vfs.NewLocal(root)
	if err != nil {
		return nil, deploy.Options{}, err
	}
	return local, deploy.Options{
		LocalRoot:     "/",
		RemoteRoot:    remoteRoot,
		Exclude:       profile.Exclude,
		IncludeHidden: profile.IncludeHidden,
		Delete:        profile.Mode == "sync",
	}, nil
}

const errNoLocalRoot = destError("the profile has no local_root")

// handleDeployKey drives the deploy card: choose a profile, review it, then run
// it. Escape walks back one step at a time.
func (m *Model) handleDeployKey(msg tea.KeyPressMsg) tea.Cmd {
	profiles := m.deployProfiles()
	switch msg.String() {
	case "esc":
		switch {
		case m.deploy.plan != nil:
			m.deploy.plan = nil
		default:
			m.Overlay = OverlayNone
			m.mode = "NORMAL"
		}
	case "up", "k":
		if m.deploy.plan == nil && len(profiles) > 0 {
			m.deploy.cursor = (m.deploy.cursor + len(profiles) - 1) % len(profiles)
		}
	case "down", "j":
		if m.deploy.plan == nil && len(profiles) > 0 {
			m.deploy.cursor = (m.deploy.cursor + 1) % len(profiles)
		}
	case "enter":
		if len(profiles) == 0 {
			return nil
		}
		name := profiles[clamp(m.deploy.cursor, 0, len(profiles)-1)]
		if m.deploy.plan == nil {
			return m.startDeploy(name)
		}
		profile, ok := m.cfg.Deploy.Profiles[name]
		if !ok {
			return nil
		}
		sess, live := m.live[profile.Host]
		if !live {
			m.toastError(profile.Host + " is not connected")
			return nil
		}
		return m.runDeploy(name, sess.FS)
	}
	return nil
}

// renderDeploy draws the two states of the card: the profile list, and the plan.
func (m *Model) renderDeploy(l layout) string {
	width := cardWidth(l, 58, 96)
	content := cardContent(width)

	if m.deploy.plan != nil {
		return m.renderDeployPlan(width, content)
	}

	lines := []string{
		m.st.CardTitle.Render("Deploy"),
		m.st.CardMuted.Render("Enter reviews a profile · Esc closes"),
		"",
	}
	profiles := m.deployProfiles()
	if len(profiles) == 0 {
		lines = append(lines, m.st.PaneEmpty.Render("No profiles in the config"))
	}
	for i, name := range profiles {
		profile := m.cfg.Deploy.Profiles[name]
		mode := profile.Mode
		if mode == "" {
			mode = "update"
		}
		row := fmt.Sprintf("%-14s %-12s %s → %s  %s",
			truncate(name, 14), truncate(profile.Host, 12),
			contractHomePath(profile.LocalRoot), profile.RemoteRoot, mode)
		if i == m.deploy.cursor {
			lines = append(lines, m.st.PaletteItemActive.Render(truncate("▸ "+row, content)))
			continue
		}
		lines = append(lines, m.st.CardText.Render(truncate("  "+row, content)))
	}
	if m.deploy.loading {
		lines = append(lines, "", m.st.CardMuted.Render("Comparing…"))
	}
	if m.deploy.err != "" {
		lines = append(lines, "", m.st.Danger.Render(truncate(m.deploy.err, content)))
	}
	return m.st.Card.Width(width).Render(strings.Join(lines, "\n"))
}

func (m *Model) renderDeployPlan(width, content int) string {
	plan := *m.deploy.plan
	create, update, remove := plan.Counts()
	lines := []string{
		m.st.CardTitle.Render("Deploy " + m.deploy.profile),
		m.st.CardMuted.Render(fmt.Sprintf("%d create · %d update · %d delete · %s",
			create, update, remove, humanSize(plan.Bytes))),
		"",
	}
	limit := max(4, m.height-12)
	shown := 0
	for _, change := range plan.Changes {
		if change.Kind == deploy.Same {
			continue
		}
		if shown >= limit {
			lines = append(lines, m.st.CardMuted.Render(fmt.Sprintf("… %d more", len(plan.Changes)-shown)))
			break
		}
		mark, style := "+", m.st.Success
		switch change.Kind {
		case deploy.Update:
			mark = "~"
		case deploy.Delete:
			mark, style = "-", m.st.Danger
		case deploy.Same, deploy.Create:
		}
		lines = append(lines, style.Render(truncate(mark+" "+change.Path, content)))
		shown++
	}
	if shown == 0 {
		lines = append(lines, m.st.CardMuted.Render("Nothing to do: the remote tree is up to date"))
	}
	lines = append(lines, "", m.st.CardMuted.Render("Enter deploys · Esc back"))
	if m.deploy.loading {
		lines = append(lines, m.st.CardMuted.Render("Working…"))
	}
	return m.st.Card.Width(width).Render(strings.Join(lines, "\n"))
}

// contractHomePath shows long local paths the way the person thinks of them.
func contractHomePath(path string) string {
	home, err := userHome()
	if err != nil || home == "" {
		return path
	}
	if replaced, ok := strings.CutPrefix(path, home); ok {
		return "~" + replaced
	}
	return path
}
