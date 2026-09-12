package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

func renderUsersListView(users []UserItem, selectedIdx int, searchFilter string, width int) string {
	mode := GetViewMode(width)
	var b strings.Builder

	header := fmt.Sprintf("USUÁRIOS — %d", len(users))
	b.WriteString(TitleStyle.Render(header))
	b.WriteString("\n")

	if searchFilter != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Render("🔍 Filtro: "+searchFilter) + "\n")
	}
	b.WriteString(SubtitleStyle.Render("[+] Novo Usuário • [/] Pesquisar • [ENTER] Ações • [0/Esc] Voltar") + "\n\n")

	if len(users) == 0 {
		if searchFilter != "" {
			b.WriteString(SubtitleStyle.Render("Nenhum usuário encontrado para a busca.\nPressione [Esc] para limpar o filtro."))
		} else {
			b.WriteString(SubtitleStyle.Render("Nenhum usuário cadastrado.\nPressione [+] para criar o primeiro usuário."))
		}
		return b.String()
	}

	if mode == ModeMobile {
		for i, u := range users {
			selected := (i == selectedIdx)
			online := !u.Disabled
			ip := "192.168.1.20"
			proto := "SSH"
			up := "0 B/s"
			down := "0 B/s"

			card := RenderUserCard(u.Username, ip, proto, online, u.Disabled, false, up, down, selected)
			b.WriteString(card + "\n")
		}
		return b.String()
	}

	// ModeCompact / ModeDesktop: Clean multi-column table
	tableHeader := fmt.Sprintf("   %-16s %-12s %-16s %-8s %-10s", "USUÁRIO", "STATUS", "IP / ALVO", "PROTO", "LIMITE")
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Render(tableHeader) + "\n")
	b.WriteString(lipgloss.NewStyle().Foreground(ColorMuted).Render("   "+strings.Repeat("─", 68)) + "\n")

	for i, u := range users {
		prefix := "  "
		lineStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#CDD6F4"))
		if i == selectedIdx {
			prefix = "▶ "
			lineStyle = lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary)
		}

		statusBadge := BadgeOnline
		if u.Disabled {
			statusBadge = BadgeBlocked
		}

		connsLimit := "Ilimitado"
		if u.MaxConnections > 0 {
			connsLimit = fmt.Sprintf("%d conexões", u.MaxConnections)
		}

		row := fmt.Sprintf("%s%-16s %-12s %-16s %-8s %-10s",
			prefix,
			lineStyle.Render(u.Username),
			statusBadge,
			"192.168.1.20",
			"SSH",
			connsLimit,
		)
		b.WriteString(row + "\n")
	}

	return b.String()
}

func renderUserDetailCard(u UserItem, width int) string {
	headerSection := []string{
		TitleStyle.Render("USUÁRIO"),
	}

	statusPill := BadgeOnline
	uptimeStr := "02h 31m"
	dlStr := "18.2 MB/s"
	ulStr := "2.4 MB/s"
	ipStr := "192.168.1.20"

	if u.Disabled {
		statusPill = BadgeBlocked
		uptimeStr = "-"
		dlStr = "0 B/s"
		ulStr = "0 B/s"
	}

	blockActionText := "[2] Bloquear"
	if u.Disabled {
		blockActionText = "[2] Desbloquear"
	}

	detailSection := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Render(u.Username),
		"",
		fmt.Sprintf("%-11s %s", "Status", statusPill),
		fmt.Sprintf("%-11s %s", "IP", ipStr),
		fmt.Sprintf("%-11s %s", "Protocolo", "SSH"),
		fmt.Sprintf("%-11s %s", "Uptime", uptimeStr),
		fmt.Sprintf("%-11s %s", "Download", dlStr),
		fmt.Sprintf("%-11s %s", "Upload", ulStr),
		"",
		"[1] Derrubar conexão",
		blockActionText,
		"[3] Detalhes",
		"[4] Remover",
		"",
		"[0] Voltar",
	}

	return RenderAdaptiveBox([][]string{headerSection, detailSection}, width)
}

func renderUserFullDetailsBox(u UserItem, width int) string {
	headerSection := []string{
		TitleStyle.Render("CREDENCIAS DO USUÁRIO"),
	}

	expiryStr := "Permanente"
	if !u.ExpiresAt.IsZero() {
		expiryStr = u.ExpiresAt.Format("02/01/2006 15:04")
	}

	detailSection := []string{
		fmt.Sprintf("%-12s %s", "Login", u.Username),
		fmt.Sprintf("%-12s %s", "Status", func() string {
			if u.Disabled {
				return "Bloqueado"
			}
			return "Ativo"
		}()),
		fmt.Sprintf("%-12s %s", "Expira", expiryStr),
		fmt.Sprintf("%-12s %d", "Max Conns", u.MaxConnections),
		fmt.Sprintf("%-12s %s", "Hash", func() string {
			if len(u.PasswordHash) > 12 {
				return u.PasswordHash[:12] + "..."
			}
			return "(definida)"
		}()),
		"",
		"[0] Voltar",
	}

	return RenderAdaptiveBox([][]string{headerSection, detailSection}, width)
}

// NewCreateUserForm constrói o formulário interativo Charm Huh para criar usuário
func NewCreateUserForm(username, password, daysStr, maxConnsStr *string) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Nome de Usuário").
				Description("Ex: fabricio").
				Value(username).
				Validate(func(s string) error {
					s = strings.TrimSpace(s)
					if s == "" {
						return fmt.Errorf("o nome de usuário é obrigatório")
					}
					return nil
				}),

			huh.NewInput().
				Title("Senha").
				Description("Deixe vazio para gerar chave segura de 24 caracteres.").
				EchoMode(huh.EchoModePassword).
				Value(password),

			huh.NewInput().
				Title("Validade (Dias)").
				Description("Ex: 30 (0 = permanente)").
				Value(daysStr).
				Validate(func(s string) error {
					v, err := strconv.Atoi(strings.TrimSpace(s))
					if err != nil || v < 0 {
						return fmt.Errorf("número inválido (>= 0)")
					}
					return nil
				}),

			huh.NewInput().
				Title("Max Conexões").
				Description("Ex: 2 (0 = ilimitado)").
				Value(maxConnsStr).
				Validate(func(s string) error {
					v, err := strconv.Atoi(strings.TrimSpace(s))
					if err != nil || v < 0 {
						return fmt.Errorf("número inválido (>= 0)")
					}
					return nil
				}),
		),
	).WithTheme(huh.ThemeCharm()).WithKeyMap(NewConfigSectionKeyMap())
}

// NewDeleteUserConfirmForm cria o formulário para confirmar a remoção de usuário
func NewDeleteUserConfirmForm(username string, confirmed *bool) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(fmt.Sprintf("⚠️  Remover Usuário %q?", username)).
				Description("Esta ação é permanente e excluirá o login e senha.").
				Affirmative("Sim, Remover").
				Negative("Cancelar").
				Value(confirmed),
		),
	).WithTheme(huh.ThemeCharm()).WithKeyMap(NewConfigSectionKeyMap())
}
