package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/fakihariefnoto/omssh/internal/sshconfig"
)

// Adding something starts with what kind of thing it is. "SSH host" and "S3
// bucket" are not the same form, and making someone remember a letter for each
// is a worse question than showing the three answers and letting them choose.

// addState is the type chooser's cursor.
type addState struct{ cursor int }

// addKind is one choice.
type addKind struct {
	label string
	hint  string
	// provider is empty for an SSH host, "s3" or "r2" for a bucket.
	provider string
	// auth narrows a provider that can be entered several ways: GCS is HMAC
	// keys, ADC, a service account or impersonation.
	auth string
}

// addKinds is the list, in the order the card shows it.
func addKinds() []addKind {
	return []addKind{
		{label: "SSH host", hint: "a machine in ~/.ssh/config"},
		{label: "AWS S3 bucket", hint: "region, bucket, optional prefix", provider: "s3"},
		{label: "Cloudflare R2 bucket", hint: "account endpoint, bucket", provider: "r2"},
		{label: "Backblaze B2 bucket", hint: "region, bucket (S3 API)", provider: "b2"},
		{label: "GCS · HMAC keys", hint: "bucket, S3-compatible API", provider: "gcs", auth: "s3"},
		{label: "GCS · application default", hint: "bucket, the identity this machine already has", provider: "gcs", auth: "adc"},
		{label: "GCS · service account", hint: "bucket, a JSON key file", provider: "gcs", auth: "service_account"},
		{label: "GCS · impersonation", hint: "bucket, become an account from ADC", provider: "gcs", auth: "impersonate"},
		{label: "Alibaba Cloud OSS", hint: "region, bucket (S3 API)", provider: "oss"},
		{label: "Azure Blob container", hint: "account key, SAS or Entra ID", provider: "azure"},
		{label: "WebDAV server", hint: "Nextcloud, Box, SharePoint, a NAS", provider: "webdav"},
	}
}

// openAddChooser shows the three kinds of place omssh knows.
func (m *Model) openAddChooser() {
	m.add = addState{}
	m.Overlay = OverlayAdd
}

// handleAddKey chooses a kind and opens its form.
func (m *Model) handleAddKey(msg tea.KeyPressMsg) {
	kinds := addKinds()
	switch msg.String() {
	case "esc":
		m.Overlay = OverlayNone
	case "up", "k":
		m.add.cursor = (m.add.cursor + len(kinds) - 1) % len(kinds)
	case "down", "j":
		m.add.cursor = (m.add.cursor + 1) % len(kinds)
	case "enter":
		kind := kinds[clamp(m.add.cursor, 0, len(kinds)-1)]
		m.Overlay = OverlayNone
		if kind.provider == "" {
			m.openConnectionForm("", sshconfig.Spec{})
			return
		}
		m.openCloudForm(kind.provider, kind.auth)
	}
}

// renderAddChooser draws the card.
func (m *Model) renderAddChooser(l layout) string {
	width := cardWidth(l, 44, 64)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render("Add"),
		m.st.CardMuted.Render("What kind of place?"),
		"",
	}
	for i, kind := range addKinds() {
		row := fmt.Sprintf("%-22s %s", kind.label, kind.hint)
		if i == m.add.cursor {
			lines = append(lines, m.st.Accent.Bold(true).Render(truncate("▸ "+row, content)))
			continue
		}
		lines = append(lines, m.st.CardText.Render(truncate("  "+row, content)))
	}
	lines = append(lines, "", m.st.CardMuted.Render(truncate("↑↓ choose · Enter next · Esc cancel", content)))
	return m.card(width, lines)
}
