package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	// Brand Colors
	ColorPrimary   = lipgloss.Color("#7D56F4") // Dragon Purple
	ColorSecondary = lipgloss.Color("#00F0FF") // Cyber Cyan
	ColorAccent    = lipgloss.Color("#FF79C6") // Neon Pink
	ColorSuccess   = lipgloss.Color("#00E5A3") // Emerald Green
	ColorWarning   = lipgloss.Color("#FFB800") // Amber Yellow
	ColorDanger    = lipgloss.Color("#FF3366") // Crimson Red
	ColorMuted     = lipgloss.Color("#6C7086") // Slate Gray
	ColorDark      = lipgloss.Color("#1E1E2E") // Midnight
	ColorPanel     = lipgloss.Color("#181825") // Deep Panel

	// Typography & Layout Styles
	LogoStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorSecondary)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Italic(true)

	// Status Badges
	BadgeOnline = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorSuccess).
			Render("● ONLINE")

	BadgeOffline = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorDanger).
			Render("○ OFFLINE")

	BadgeActive = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorSuccess).
			Render("● ATIVO")

	BadgeBlocked = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorDanger).
			Render("■ BLOQUEADO")

	BadgeExpired = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorWarning).
			Render("▲ EXPIRADO")

	// Menu Item Styles
	MenuItemStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#CDD6F4"))

	MenuSelectedStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorSecondary)

	// Key-Value Style
	KeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorMuted).
			Width(12)

	ValueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFFFF"))

	// Footer & Hints
	FooterStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	HelpKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorSecondary)

	HelpDescStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)
)

type ViewMode int

const (
	ModeMobile  ViewMode = iota // < 60 colunas
	ModeCompact                 // 60 - 100 colunas
	ModeDesktop                 // > 100 colunas
)

func GetViewMode(width int) ViewMode {
	if width < 60 {
		return ModeMobile
	} else if width <= 100 {
		return ModeCompact
	}
	return ModeDesktop
}

const MobileInnerWidth = 32 // Visible width inside the borders: 32 chars + 2 for borders = 34 chars

// RenderBoxWithWidth constrói uma caixa Unicode com largura interna arbitrária
func RenderBoxWithWidth(sections [][]string, innerWidth int) string {
	if innerWidth < 30 {
		innerWidth = 32
	}
	borderColor := lipgloss.NewStyle().Foreground(ColorPrimary)

	top := borderColor.Render("┌" + strings.Repeat("─", innerWidth) + "┐")
	divider := borderColor.Render("├" + strings.Repeat("─", innerWidth) + "┤")
	bottom := borderColor.Render("└" + strings.Repeat("─", innerWidth) + "┘")
	pipe := borderColor.Render("│")

	var lines []string
	lines = append(lines, top)

	for sIdx, sec := range sections {
		if sIdx > 0 {
			lines = append(lines, divider)
		}
		for _, line := range sec {
			visLen := lipgloss.Width(line)
			padLen := innerWidth - 3 - visLen // 2 spaces left, at least 1 space right padding
			if padLen < 0 {
				padLen = 0
			}
			padded := "  " + line + strings.Repeat(" ", padLen) + " "
			lines = append(lines, pipe+padded+pipe)
		}
	}

	lines = append(lines, bottom)
	return strings.Join(lines, "\n")
}

// RenderMobileBox constrói uma caixa Unicode com largura fixa adaptada a telas mobile/Termius (34 chars)
func RenderMobileBox(sections [][]string) string {
	return RenderBoxWithWidth(sections, MobileInnerWidth)
}

// RenderAdaptiveBox ajusta automaticamente a largura da caixa conforme a largura do terminal
func RenderAdaptiveBox(sections [][]string, width int) string {
	mode := GetViewMode(width)
	switch mode {
	case ModeMobile:
		return RenderBoxWithWidth(sections, MobileInnerWidth)
	case ModeCompact:
		target := width - 4
		if target < 56 {
			target = 56
		} else if target > 76 {
			target = 76
		}
		return RenderBoxWithWidth(sections, target)
	default: // ModeDesktop
		target := width - 4
		if target < 80 {
			target = 80
		} else if target > 110 {
			target = 110
		}
		return RenderBoxWithWidth(sections, target)
	}
}

// RenderDesktopSplit constrói um layout lado a lado (painel esquerdo e conteúdo direito)
func RenderDesktopSplit(leftContent, rightContent string, totalWidth int) string {
	leftWidth := 36
	rightWidth := totalWidth - leftWidth - 4
	if rightWidth < 45 {
		rightWidth = 45
	}

	leftCol := lipgloss.NewStyle().Width(leftWidth).Render(leftContent)
	rightCol := lipgloss.NewStyle().Width(rightWidth).Render(rightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, "  ", rightCol)
}

// RenderUserCard renderiza um card vertical de usuário ideal para visualização em celular
func RenderUserCard(username, ip, proto string, online, blocked, expired bool, up, down string, selected bool) string {
	var b strings.Builder

	dotColor := ColorSuccess
	dotSymbol := "●"
	statusText := "Online"
	if blocked {
		dotColor = ColorDanger
		dotSymbol = "■"
		statusText = "Bloqueado"
	} else if expired {
		dotColor = ColorWarning
		dotSymbol = "▲"
		statusText = "Expirado"
	} else if !online {
		dotColor = ColorMuted
		dotSymbol = "○"
		statusText = "Offline"
	}

	headerColor := lipgloss.Color("#FFFFFF")
	prefix := " "
	if selected {
		headerColor = ColorSecondary
		prefix = "▶"
	}

	header := lipgloss.NewStyle().Foreground(dotColor).Render(dotSymbol) + " " +
		lipgloss.NewStyle().Bold(true).Foreground(headerColor).Render(username)
	b.WriteString(prefix + " " + header + "\n")

	if ip == "" {
		ip = "192.168.1.20"
	}
	b.WriteString("  " + lipgloss.NewStyle().Foreground(ColorMuted).Render(ip) + "\n")

	protoLine := proto + " · " + statusText
	b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#A6ADC8")).Render(protoLine) + "\n")

	if online && (up != "" || down != "") {
		traffic := fmt.Sprintf("↑ %s ↓ %s", up, down)
		b.WriteString("  " + lipgloss.NewStyle().Foreground(ColorAccent).Render(traffic) + "\n")
	}

	return b.String()
}

// RenderConnCard renderiza um card vertical de conexão ativa
func RenderConnCard(sid, target string, active bool, up, down string, age string, selected bool) string {
	var b strings.Builder

	dotColor := ColorSuccess
	dotSymbol := "●"
	if !active {
		dotColor = ColorDanger
		dotSymbol = "○"
	}

	headerColor := lipgloss.Color("#FFFFFF")
	prefix := " "
	if selected {
		headerColor = ColorSecondary
		prefix = "▶"
	}

	shortID := sid
	if len(shortID) > 10 {
		shortID = shortID[:8] + "..."
	}

	header := lipgloss.NewStyle().Foreground(dotColor).Render(dotSymbol) + " " +
		lipgloss.NewStyle().Bold(true).Foreground(headerColor).Render(shortID)
	b.WriteString(prefix + " " + header + "\n")

	b.WriteString("  " + lipgloss.NewStyle().Foreground(ColorMuted).Render(target) + "\n")

	stateLine := "DragonTCP · " + age
	b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#A6ADC8")).Render(stateLine) + "\n")

	if up != "" || down != "" {
		traffic := fmt.Sprintf("↑ %s ↓ %s", up, down)
		b.WriteString("  " + lipgloss.NewStyle().Foreground(ColorAccent).Render(traffic) + "\n")
	}

	return b.String()
}
