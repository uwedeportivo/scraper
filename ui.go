package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Messages ---

type msgPageScraped struct {
	url   string
	links int
}

type msgImageDownloaded struct {
	filename string
}

type msgError struct {
	url string
}

type msgDone struct{}

type tickMsg time.Time

// --- Rotating status messages ---

var statusMessages = []string{
	"Scouring the web...",
	"Hunting for images...",
	"Spelunking through HTML...",
	"Interrogating anchor tags...",
	"Stalking the DOM...",
	"Tracing the breadcrumbs...",
	"Hoarding pixels...",
	"Vacuuming the internet...",
	"Herding images into the corral...",
	"Following every rabbit hole...",
	"Cataloguing the artifacts...",
	"Mapping the territory...",
	"Descending into the link tree...",
	"Trawling the deep web...",
}

const maxLogLines = 10

// --- Model ---

type uiStats struct {
	scraped    int
	downloaded int
	errors     int
}

type uiModel struct {
	spinner   spinner.Model
	log       []string
	stats     uiStats
	statusIdx int
	done      bool
}

func newModel() uiModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = styleSpin
	return uiModel{spinner: s}
}

func tickCmd() tea.Cmd {
	return tea.Tick(4*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m uiModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, tickCmd())
}

func (m uiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tickMsg:
		m.statusIdx = (m.statusIdx + 1) % len(statusMessages)
		return m, tickCmd()

	case msgPageScraped:
		m.stats.scraped++
		m.addLog(fmt.Sprintf("◆ scraped  %s  (%d links)", truncate(msg.url, 48), msg.links))
		return m, nil

	case msgImageDownloaded:
		m.stats.downloaded++
		m.addLog(fmt.Sprintf("%s %s", styleCheck.Render("✓"), truncate(msg.filename, 58)))
		return m, nil

	case msgError:
		m.stats.errors++
		m.addLog(fmt.Sprintf("%s %s", styleCross.Render("✗"), truncate(msg.url, 58)))
		return m, nil

	case msgDone:
		m.done = true
		return m, tea.Quit

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

// --- Styles ---

var (
	styleCheck  = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	styleCross  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styleSpin   = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	styleLabel  = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleValue  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("252"))
	styleDone   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	styleLogBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238")).
			Padding(0, 1).
			Width(70)
)

func (m uiModel) View() string {
	if m.done {
		return "\n" + styleDone.Render(fmt.Sprintf(
			"  ✓ Done — scraped %d pages, downloaded %d images, %d errors.",
			m.stats.scraped, m.stats.downloaded, m.stats.errors,
		)) + "\n\n"
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString("  " + styleTitle.Render("🕷  scraper") + "\n\n")
	b.WriteString("  " + m.spinner.View() + " " + statusMessages[m.statusIdx] + "\n\n")

	if len(m.log) > 0 {
		var lines strings.Builder
		for _, l := range m.log {
			lines.WriteString(styleDim.Render(l) + "\n")
		}
		b.WriteString(styleLogBox.Render(strings.TrimRight(lines.String(), "\n")))
		b.WriteString("\n\n")
	}

	b.WriteString(fmt.Sprintf("  %s %s   %s %s   %s %s\n",
		styleLabel.Render("pages:"), styleValue.Render(fmt.Sprintf("%d", m.stats.scraped)),
		styleLabel.Render("images:"), styleValue.Render(fmt.Sprintf("%d", m.stats.downloaded)),
		styleLabel.Render("errors:"), styleValue.Render(fmt.Sprintf("%d", m.stats.errors)),
	))
	b.WriteString("\n")
	return b.String()
}

func (m *uiModel) addLog(entry string) {
	m.log = append(m.log, entry)
	if len(m.log) > maxLogLines {
		m.log = m.log[len(m.log)-maxLogLines:]
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
