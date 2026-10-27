package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// A copy is the one operation with a middle. It can take minutes, it can be
// abandoned, and the interface must keep answering while it runs — so the copy
// runs in a command and reports through a channel, and the sidebar's TRANSFERS
// section is where that report lands.

// transfer is one copy in flight.
type transfer struct {
	id         int
	label      string
	name       string
	done       int64
	total      int64
	started    time.Time
	finished   bool
	finishedAt time.Time
}

// transferUpdate is one progress report from a running copy.
type transferUpdate struct {
	id    int
	name  string
	done  int64
	total int64
}

// armProgress makes sure exactly one reader is waiting for transfer reports. It is
// called at startup and again when a paste begins, because the channel is created
// lazily: without this, a copy's reports filled the buffer and nothing ever read
// them, which is why the sidebar said "working…" forever.
func (m *Model) armProgress() tea.Cmd {
	if m.progress == nil {
		m.progress = make(chan transferUpdate, 64)
	}
	if m.progressArmed {
		return nil
	}
	m.progressArmed = true
	return waitForTransfer(m.progress)
}

// waitForTransfer blocks until the next report, then delivers it as a message.
// It is re-armed after every report, the same way the connection prompts are.
func waitForTransfer(ch chan transferUpdate) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg { return <-ch }
}

// startTransfer registers a copy and returns its id and a reporter for the engine.
// The reporter never blocks: a full channel means the interface is busy drawing,
// and dropping a progress tick is better than stalling the copy.
func (m *Model) startTransfer(label string) (id int, report func(done, total int64, name string)) {
	if m.transfers == nil {
		m.transfers = map[int]transfer{}
	}
	if m.progress == nil {
		m.progress = make(chan transferUpdate, 64)
	}
	m.transferSeq++
	id = m.transferSeq
	m.transfers[id] = transfer{id: id, label: label, started: time.Now()}
	// Say it out loud as well: the sidebar may be hidden on a narrow terminal, and
	// a copy with no visible start reads as a copy that did not happen.
	m.toastInfo(label + " started")
	ch := m.progress
	report = func(done, total int64, name string) {
		select {
		case ch <- transferUpdate{id: id, name: name, done: done, total: total}:
		default:
		}
	}
	return id, report
}

// finishTransfer marks a copy finished and says how long it took. It stays in the
// sidebar for a moment: a copy of a few small files is over before the next frame,
// and a job that never appears reads as a job that never ran.
func (m *Model) finishTransfer(id int) time.Duration {
	job, ok := m.transfers[id]
	if !ok {
		return 0
	}
	took := time.Since(job.started)
	job.finished = true
	job.finishedAt = time.Now()
	job.done = job.total
	m.transfers[id] = job
	return took
}

// pruneTransfers forgets finished copies after they have been visible long
// enough to read.
func (m *Model) pruneTransfers(now time.Time) {
	const linger = 10 * time.Second
	for id, job := range m.transfers {
		if job.finished && now.Sub(job.finishedAt) > linger {
			delete(m.transfers, id)
		}
	}
}

// elapsed is how long the transfer has been running, and rate its average speed.
func (t transfer) elapsed() time.Duration {
	if t.finished {
		return t.finishedAt.Sub(t.started)
	}
	return time.Since(t.started)
}

func (t transfer) rate() string {
	seconds := t.elapsed().Seconds()
	if seconds < 0.2 || t.done == 0 {
		return "—"
	}
	return humanSize(int64(float64(t.done)/seconds)) + "/s"
}

// transferPercent is the copy's progress, or -1 when it cannot be known yet (a
// single large file reports only when it lands).
func (t transfer) percent() int {
	if t.total <= 0 {
		return -1
	}
	percent := int(t.done * 100 / t.total)
	if percent > 100 {
		percent = 100
	}
	return percent
}

// openTransfersIfBig shows the transfer screen for a copy large enough to be
// worth watching, unless something else is already open: taking over a dialog is
// worse than a small copy staying quiet.
func (m *Model) openTransfersIfBig(items int, bytes int64) {
	if m.Overlay != OverlayNone || !bigTransfer(items, bytes) {
		return
	}
	m.Overlay = OverlayTransfers
}

// bigTransfer is when a copy deserves the screen rather than a line in the
// sidebar: many files, or a lot of bytes. The numbers are a heuristic, not a
// promise, and they are deliberately high enough that a routine copy stays quiet.
func bigTransfer(items int, bytes int64) bool {
	return items >= 50 || bytes >= 100<<20
}

// renderTransfersScreen is the transfer screen: every job, its bar, its current
// file, speed and time. It exists because a big copy is the one operation a person
// wants to watch and cannot see in a sidebar that is three sections deep.
func (m *Model) renderTransfersScreen(l layout) string {
	width := cardWidth(l, 60, 96)
	content := cardContent(width)
	lines := []string{
		m.st.CardTitle.Render("Transfers"),
		m.st.CardMuted.Render(truncate(fmt.Sprintf("%d running · jobs continue in the background",
			m.Jobs()), content)),
		"",
	}

	ids := make([]int, 0, len(m.transfers))
	for id := range m.transfers {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		job := m.transfers[id]
		state := fmt.Sprintf("%s · %s", job.rate(), job.elapsed().Round(time.Second))
		if job.finished {
			state = "done · " + job.elapsed().Round(100*time.Millisecond).String()
		}
		lines = append(lines, spread(m.st.Accent.Bold(true).Render(truncate(job.label, content)), m.st.CardMuted.Render(state), content))

		percent := job.percent()
		if percent < 0 {
			lines = append(lines, m.st.CardMuted.Render(truncate("working…  "+job.name, content)))
		} else {
			lines = append(lines, m.progressBar(percent, content))
		}
		if job.name != "" && percent >= 0 {
			lines = append(lines, m.st.CardText.Render(truncate(job.name, content)))
		}
		if job.total > 0 {
			lines = append(lines, m.st.CardMuted.Render(truncate(fmt.Sprintf("%s of %s",
				humanSize(job.done), humanSize(job.total)), content)))
		}
		lines = append(lines, "")
	}
	if len(ids) == 0 {
		lines = append(lines, m.st.PaneEmpty.Render("Nothing running"))
	}
	lines = append(lines, m.st.CardMuted.Render("Esc closes · the copy keeps going"))
	return m.card(width, lines)
}

// renderTransfers draws the TRANSFERS section of the sidebar: one block per copy,
// with a bar, the percentage and the file it is on.
func (m *Model) renderTransfers(width int) []string {
	if len(m.transfers) == 0 {
		return nil
	}
	rows := make([]string, 0, len(m.transfers)*3)
	// Oldest first, so the list does not reorder while it is being read.
	ids := make([]int, 0, len(m.transfers))
	for id := range m.transfers {
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	for _, id := range ids {
		job := m.transfers[id]
		rows = append(rows, m.st.SidebarItem.Render(truncate(job.label, width)))
		if job.finished {
			rows = append(rows, m.st.Success.Render(truncate("done", width)))
			continue
		}
		percent := job.percent()
		if percent < 0 {
			rows = append(rows, m.st.StatusMuted.Render(truncate("working…", width)))
		} else {
			rows = append(rows, m.progressBar(percent, width))
		}
		if job.name != "" {
			rows = append(rows, m.st.PaneEmpty.Render(truncate(job.name, width)))
		}
	}
	return rows
}

// progressBar is the sidebar's compact bar: shape first, number second, because
// the sidebar is narrow and "nearly full" is read faster than a percentage.
func (m *Model) progressBar(percent, width int) string {
	const barWidth = 8
	filled := clamp(percent*barWidth/100, 0, barWidth)
	style := m.st.Success
	switch {
	case percent >= 80:
		style = m.st.Danger
	case percent >= 60:
		style = m.st.Warning
	}
	bar := style.Render(strings.Repeat("█", filled)) + m.st.CardMuted.Render(strings.Repeat("░", barWidth-filled))
	return truncate(fmt.Sprintf("%s %3d%%", bar, percent), width)
}
