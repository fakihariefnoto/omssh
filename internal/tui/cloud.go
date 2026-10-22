package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/cloudcred"
	"github.com/fakihariefnoto/omssh/internal/session"
	omssh "github.com/fakihariefnoto/omssh/internal/ssh"
	"github.com/fakihariefnoto/omssh/internal/vfs"
	"github.com/fakihariefnoto/omssh/internal/vfs/object"
)

// A bucket is a place like a host: it gets a pane, a tab and its own path. It has
// no shell and no permissions, and the interface says so rather than offering a
// terminal that cannot exist.

const cloudPrefix = "cloud:"

func isCloudHost(host string) bool { return strings.HasPrefix(host, cloudPrefix) }

// cloudReadyMsg carries a bucket that has been opened.
type cloudReadyMsg struct {
	name string
	fsys vfs.FS
	err  error
}

// openCloudPane builds the client and the filesystem off the update loop: the
// credential chain can read files, and a listing is a network round trip.
func (m *Model) openCloudPane(name string) tea.Cmd {
	profile, ok := m.cfg.Cloud.Profiles[name]
	if !ok {
		m.toastError("No cloud profile named " + name)
		return nil
	}
	m.mode = "CONNECTING"
	m.toastInfo("Opening " + name + "…")
	ctx := m.connectContext()
	// The keyring is read here, on the update loop, and the pair is captured by
	// the command; the store is not safe to touch from the goroutine.
	accessKey, secretKey, _ := cloudcred.Load(m.credStore(), name)

	return func() tea.Msg {
		client, err := object.NewClient(ctx, object.ClientOptions{
			Provider:        profile.Provider,
			Region:          profile.Region,
			Endpoint:        profile.Endpoint,
			PathStyle:       profile.PathStyle,
			AccessKeyID:     accessKey,
			SecretAccessKey: secretKey,
		})
		if err != nil {
			return cloudReadyMsg{name: name, err: err}
		}
		fsys, err := object.New(object.Options{
			API: client, Bucket: profile.Bucket, Prefix: profile.Prefix, Label: name,
		})
		return cloudReadyMsg{name: name, fsys: fsys, err: err}
	}
}

// handleCloudReady binds a pane to the bucket. It is registered as a session so
// the tab strip, the sidebar and every pane operation treat it like any other
// place; only the shell is absent.
func (m *Model) handleCloudReady(msg cloudReadyMsg) tea.Cmd {
	m.mode = "NORMAL"
	if msg.err != nil {
		m.toastError(msg.name + ": " + msg.err.Error())
		return nil
	}
	live := &session.Session{
		ID:     cloudPrefix + msg.name,
		Target: omssh.Target{Alias: cloudPrefix + msg.name, Host: msg.name},
		FS:     msg.fsys,
		State:  session.StateReady,
		Root:   "/",
		Base:   msg.fsys.String(),
	}
	m.rememberSession(live)
	m.addSessionTab(live)
	index := m.paneForSession(live)
	m.Focused = index
	m.tabState(live.Target.Alias, StateReady)
	m.screen = screenFiles
	m.toastSuccess("Opened " + msg.name)
	return m.loadPaneAsync(index)
}

// cloudLabel is what the interface calls a bucket host.
func cloudLabel(host string) string { return strings.TrimPrefix(host, cloudPrefix) }
