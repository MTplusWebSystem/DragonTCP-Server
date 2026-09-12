package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	styleLogDebug = lipgloss.NewStyle().Foreground(ColorSecondary)
	styleLogChunk = lipgloss.NewStyle().Foreground(ColorPrimary)
	styleLogError = lipgloss.NewStyle().Bold(true).Foreground(ColorDanger)
	styleLogStats = lipgloss.NewStyle().Bold(true).Foreground(ColorSuccess)
	styleLogTime  = lipgloss.NewStyle().Foreground(ColorMuted)
)

func formatLogLine(line string) string {
	switch {
	case strings.Contains(line, "[ERROR]"):
		return styleLogError.Render(line)
	case strings.Contains(line, "[CHUNK]"):
		return styleLogChunk.Render(line)
	case strings.Contains(line, "STATS"):
		return styleLogStats.Render(line)
	case strings.Contains(line, "[DEBUG]"):
		return styleLogDebug.Render(line)
	default:
		return styleLogTime.Render(line)
	}
}

func renderLogsAdaptiveView(logs []string, scrollOffset, viewHeight, width int) string {
	var b strings.Builder
	mode := GetViewMode(width)

	header := fmt.Sprintf("LOGS DO SERVIDOR — %d", len(logs))
	b.WriteString(TitleStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(SubtitleStyle.Render("[↑/↓/j/k] Rolar • [g/G] Início/Fim • [0/Esc] Voltar") + "\n\n")

	if len(logs) == 0 {
		b.WriteString(SubtitleStyle.Render("Nenhum log disponível no momento."))
		return b.String()
	}

	total := len(logs)
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset > total-1 {
		scrollOffset = total - 1
	}

	end := scrollOffset + viewHeight
	if end > total {
		end = total
	}

	maxLineLen := width - 4
	if mode == ModeMobile {
		maxLineLen = 34
	} else if maxLineLen < 60 {
		maxLineLen = 60
	}

	visible := logs[scrollOffset:end]
	for _, line := range visible {
		clean := line
		if len(clean) > maxLineLen {
			clean = clean[:maxLineLen-3] + "..."
		}
		b.WriteString(formatLogLine(clean) + "\n")
	}

	b.WriteString("\n" + HelpDescStyle.Render(fmt.Sprintf("Linhas %d-%d de %d", scrollOffset+1, end, total)))
	return b.String()
}
