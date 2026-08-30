package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/xilistudios/lele/pkg/cron"
	"github.com/xilistudios/lele/pkg/tui/i18n"
)

// loadCronJobs refreshes the cron modal list from the cron store. It reloads
// the store from disk so the list reflects jobs created/edited by the gateway.
func (m *Model) loadCronJobs() {
	m.modalItems = nil
	m.cronModalKeys = nil
	m.cronDetailMode = false
	m.cronDetailJobID = ""

	if m.cronService == nil {
		m.modalItems = append(m.modalItems, i18n.T("tui.cronUnavailable"))
		return
	}

	// Reload from disk to pick up external changes.
	_ = m.cronService.Load()

	jobs := m.cronService.ListJobs(true) // include disabled
	if len(jobs) == 0 {
		m.modalItems = append(m.modalItems, i18n.T("tui.noCronJobs"))
		return
	}

	for _, j := range jobs {
		m.modalItems = append(m.modalItems, formatCronJobLine(j))
		m.cronModalKeys = append(m.cronModalKeys, j.ID)
	}

	// The list may have shrunk (e.g. a job was deleted while the cursor was on
	// the last row) — keep the cursor within bounds.
	m.clampModalCursor()
}

// formatCronJobLine renders a single cron job as a compact list line.
func formatCronJobLine(j cron.CronJob) string {
	state := "●" // enabled
	stateColor := SecondaryColor
	if !j.Enabled {
		state = "○"
		stateColor = CommentColor
	}

	name := j.Name
	if name == "" {
		name = j.Payload.Message
	}
	if name == "" {
		name = j.Payload.Command
	}
	if name == "" {
		name = j.ID
	}
	// Truncate long names for the list view.
	if len(name) > 40 {
		name = name[:37] + "..."
	}

	sched := formatCronSchedule(j.Schedule)
	prefix := lipgloss.NewStyle().Foreground(stateColor).Render(state)
	return fmt.Sprintf("%s %s  %s", prefix, name, CommentColorStyle.Render("["+sched+"]"))
}

// formatCronSchedule returns a short human-readable description of a schedule.
func formatCronSchedule(s cron.CronSchedule) string {
	switch s.Kind {
	case "at":
		if s.AtMS != nil {
			t := time.UnixMilli(*s.AtMS)
			return "at " + t.Format("2006-01-02 15:04")
		}
		return "at ?"
	case "every":
		if s.EveryMS != nil {
			d := time.Duration(*s.EveryMS) * time.Millisecond
			return "every " + shortDuration(d)
		}
		return "every ?"
	case "cron":
		if s.Expr != "" {
			return "cron " + s.Expr
		}
		return "cron ?"
	default:
		return s.Kind
	}
}

// shortDuration renders a duration compactly (e.g. "30s", "5m", "2h", "1d").
func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// renderCronDetail renders the detail view for the currently selected cron job.
func (m *Model) renderCronDetail() string {
	job := m.cronService.GetJob(m.cronDetailJobID)
	if job == nil {
		// Job was deleted; fall back to the list.
		m.cronDetailMode = false
		m.loadCronJobs()
		return m.renderModal(i18n.T("tui.cronJobs"))
	}

	var sb strings.Builder

	// Title line: name + enabled state
	name := job.Name
	if name == "" {
		name = job.ID
	}
	stateText := i18n.T("tui.cronEnabled")
	stateColor := SecondaryColor
	if !job.Enabled {
		stateText = i18n.T("tui.cronDisabled")
		stateColor = CommentColor
	}
	titleLine := lipgloss.JoinHorizontal(lipgloss.Center,
		TitleStyle.Render(name),
		"  ",
		lipgloss.NewStyle().Foreground(stateColor).Render("["+stateText+"]"),
	)
	sb.WriteString(titleLine + "\n\n")

	// Detail fields
	labelStyle := lipgloss.NewStyle().Foreground(AccentColor)
	addField := func(label, value string) {
		sb.WriteString(labelStyle.Render(label+": ") + value + "\n")
	}

	addField(i18n.T("tui.cronID"), job.ID)
	if job.Scope != "" {
		addField("Scope", job.Scope)
	}
	addField(i18n.T("tui.cronSchedule"), formatCronSchedule(job.Schedule))
	if job.Schedule.TZ != "" {
		addField(i18n.T("tui.cronTimezone"), job.Schedule.TZ)
	}
	if job.Payload.Message != "" {
		addField(i18n.T("tui.cronMessage"), job.Payload.Message)
	}
	if job.Payload.Command != "" {
		addField(i18n.T("tui.cronCommand"), job.Payload.Command)
	}
	if job.Payload.Channel != "" {
		addField(i18n.T("tui.cronChannel"), job.Payload.Channel)
	}
	if job.Payload.To != "" {
		addField(i18n.T("tui.cronTo"), job.Payload.To)
	}
	if job.Payload.SessionKey != "" {
		addField("Session", job.Payload.SessionKey)
	}
	addField(i18n.T("tui.cronDeliver"), fmt.Sprintf("%v", job.Payload.Deliver))

	if job.State.NextRunAtMS != nil {
		addField(i18n.T("tui.cronNextRun"), time.UnixMilli(*job.State.NextRunAtMS).Format("2006-01-02 15:04:05"))
	}
	if job.State.LastRunAtMS != nil {
		addField(i18n.T("tui.cronLastRun"), time.UnixMilli(*job.State.LastRunAtMS).Format("2006-01-02 15:04:05"))
	}
	if job.State.LastStatus != "" {
		addField(i18n.T("tui.cronLastStatus"), job.State.LastStatus)
	}
	if job.State.LastError != "" {
		addField(i18n.T("tui.cronLastError"), lipgloss.NewStyle().Foreground(PrimaryColor).Render(job.State.LastError))
	}

	sb.WriteString("\n")
	sb.WriteString(CommentColorStyle.Render(i18n.T("tui.cronDetailHints")))

	box := ModalContainer.Width(m.width - 10).Render(sb.String())
	return m.paintFrame(box)
}

// selectedCronJobID returns the ID of the currently selected cron job in the
// modal list, or "" if none is selected (e.g. empty list).
func (m *Model) selectedCronJobID() string {
	if m.cronDetailMode && m.cronDetailJobID != "" {
		return m.cronDetailJobID
	}
	if m.modalSelectedIdx < len(m.cronModalKeys) {
		return m.cronModalKeys[m.modalSelectedIdx]
	}
	return ""
}

// reselectCronJob restores the selection cursor (and detail mode) to the job
// with the given ID after the list has been reloaded.
func (m *Model) reselectCronJob(jobID string) {
	for i, id := range m.cronModalKeys {
		if id == jobID {
			m.modalSelectedIdx = i
			if m.cronDetailMode {
				m.cronDetailJobID = jobID
			}
			return
		}
	}
	// Job no longer present; reset selection.
	if m.modalSelectedIdx >= len(m.cronModalKeys) && len(m.cronModalKeys) > 0 {
		m.modalSelectedIdx = len(m.cronModalKeys) - 1
	}
}
