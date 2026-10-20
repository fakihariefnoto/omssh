package tui

import (
	"fmt"
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
	id      int
	label   string
	name    string
	done    int64
	total   int64
	started time.Time
}

// transferUpdate is one progress report from a running copy.
type transferUpdate struct {
	id    int
	name  string
	done  int64
	total int64
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
	ch := m.progress
	report = func(done, total int64, name string) {
		select {
		case ch <- transferUpdate{id: id, name: name, done: done, total: total}:
		default:
		}
	}
	return id, report
}

// finishTransfer forgets a copy and says how long it took.
func (m *Model) finishTransfer(id int) time.Duration {
	job, ok := m.transfers[id]
	if !ok {
		return 0
	}
	delete(m.transfers, id)
	return time.Since(job.started)
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
