package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

func renderConnectionsListView(conns []ConnectionItem, selectedIdx int, searchFilter string, width int) string {
	mode := GetViewMode(width)
	var b strings.Builder

	header := fmt.Sprintf("CONEXÕES ATIVAS — %d", len(conns))
	b.WriteString(TitleStyle.Render(header))
	b.WriteString("\n")

	if searchFilter != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Render("🔍 Filtro: "+searchFilter) + "\n")
	}
	b.WriteString(SubtitleStyle.Render("[ENTER] Selecionar • [/] Pesquisar • [0/Esc] Voltar") + "\n\n")

	if len(conns) == 0 {
		if searchFilter != "" {
			b.WriteString(SubtitleStyle.Render("Nenhuma conexão encontrada para o filtro.\nPressione [Esc] para limpar."))
		} else {
			b.WriteString(SubtitleStyle.Render("Nenhuma conexão de tunelamento ativa no momento.\nO servidor está pronto aguardando clientes."))
		}
		return b.String()
	}

	if mode == ModeMobile {
		for i, c := range conns {
			selected := (i == selectedIdx)
			age := fmt.Sprintf("%ds atrás", c.AgeSeconds)
			if c.AgeSeconds <= 0 {
				age = "agora"
			}
			up := formatBytesHuman(c.BaseOffset)
			down := formatBytesHuman(uint64(c.BufferedBytes))

			card := RenderConnCard(c.SessionID, c.TargetName, !c.Closed, up, down, age, selected)
			b.WriteString(card + "\n")
		}
		return b.String()
	}

	// ModeCompact / ModeDesktop: Clean multi-column table
	tableHeader := fmt.Sprintf("   %-24s %-20s %-10s %-10s %-8s", "SESSÃO ID", "ALVO", "BUFFER", "OFFSET", "ESTADO")
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Render(tableHeader) + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render("   "+strings.Repeat("─", 74)) + "\n")

	for i, c := range conns {
		prefix := "  "
		lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#CDD6F4"))
		if i == selectedIdx {
			prefix = "▶ "
			lineStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary)
		}

		shortID := c.SessionID
		if len(shortID) > 22 {
			shortID = shortID[:10] + "..." + shortID[len(shortID)-8:]
		}

		statusBadge := BadgeOnline
		if c.Closed {
			statusBadge = BadgeBlocked
		}

		target := c.TargetName
		if len(target) > 18 {
			target = target[:18]
		}

		row := fmt.Sprintf("%s%-24s %-20s %-10s %-10s %-8s",
			prefix,
			lineStyle.Render(shortID),
			target,
			formatBytesHuman(uint64(c.BufferedBytes)),
			fmt.Sprintf("%d B", c.BaseOffset),
			statusBadge,
		)
		b.WriteString(row + "\n")
	}

	return b.String()
}

func renderConnectionDetailCard(c ConnectionItem, width int) string {
	headerSection := []string{
		TitleStyle.Render("CONEXÃO"),
	}

	statusPill := BadgeOnline
	if c.Closed {
		statusPill = BadgeBlocked
	} else if c.EOF {
		statusPill = BadgeExpired
	}

	shortID := c.SessionID
	if len(shortID) > 16 {
		shortID = shortID[:8] + "..." + shortID[len(shortID)-8:]
	}

	ageStr := fmt.Sprintf("%ds atrás", c.AgeSeconds)
	if c.AgeSeconds <= 0 {
		ageStr = "agora"
	}

	target := c.TargetName
	if len(target) > 16 {
		target = target[:16]
	}

	detailSection := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Render(shortID),
		"",
		fmt.Sprintf("%-10s %s", "Status", statusPill),
		fmt.Sprintf("%-10s %s", "Alvo", target),
		fmt.Sprintf("%-10s %s", "Buffer", formatBytesHuman(uint64(c.BufferedBytes))),
		fmt.Sprintf("%-10s %d B", "Offset", c.BaseOffset),
		fmt.Sprintf("%-10s %s", "Última", ageStr),
		"",
		"[1] Derrubar conexão",
		"[2] Derrubar TODAS",
		"",
		"[0] Voltar",
	}

	return RenderAdaptiveBox([][]string{headerSection, detailSection}, width)
}

func NewKillConnConfirmForm(sid string, isAll bool, confirmed *bool) *huh.Form {
	title := fmt.Sprintf("⚠️  Derrubar Conexão %s?", sid)
	desc := "A sessão será desconectada e o socket encerrado imediatamente."
	if isAll {
		title = "⚠️  DERRUBAR TODAS AS CONEXÕES ATIVAS?"
		desc = "Todas as sessões abertas no servidor serão desconectadas."
	}

	return huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(title).
				Description(desc).
				Affirmative("Sim, Derrubar").
				Negative("Cancelar").
				Value(confirmed),
		),
	).WithTheme(huh.ThemeCharm())
}
