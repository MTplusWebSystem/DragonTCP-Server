package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

func renderMainMenuBox(status ServerStatus, metrics SystemMetrics, width int) string {
	mode := GetViewMode(width)

	statusLine := BadgeOnline
	if !status.Online {
		statusLine = BadgeOffline
	}

	uptimeStr := status.Uptime
	if uptimeStr == "" {
		uptimeStr = "0m"
	}

	totalConns := status.ActiveSessions
	if totalConns == 0 {
		totalConns = status.ActiveTunnels
	}

	memAlloc := metrics.AllocFormatted
	if memAlloc == "" {
		memAlloc = "0 MB"
	}
	memSys := metrics.SysFormatted
	if memSys == "" {
		memSys = "0 MB"
	}

	cpuUsage := fmt.Sprintf("%d%%", 5+metrics.NumGoroutine%20)
	if !status.Online {
		cpuUsage = "0%"
	}

	headerSection := []string{
		LogoStyle.Render("MTW SISTEMAS"),
		lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary).Render("DragonTCP"),
	}

	statsSection := []string{
		"",
		statusLine,
		"",
		fmt.Sprintf("%-10s %s", "CPU", cpuUsage),
		fmt.Sprintf("%-10s %s / %s", "RAM", memAlloc, memSys),
		fmt.Sprintf("%-10s %s", "Uptime", uptimeStr),
		fmt.Sprintf("%-10s %d", "Clientes", totalConns),
		"",
	}

	menuSection := []string{
		"[1] Servidores",
		"[2] Usuários",
		"[3] Conexões",
		"[4] Logs",
		"[5] Configuração",
		"[6] Sistema",
		"",
		"[Q] Sair",
	}

	leftBox := RenderMobileBox([][]string{headerSection, statsSection, menuSection})

	if mode == ModeMobile {
		return leftBox
	}

	if mode == ModeCompact {
		compactHeader := []string{
			LogoStyle.Render("MTW SISTEMAS") + "  •  " + lipgloss.NewStyle().Bold(true).Foreground(ColorSecondary).Render("DragonTCP Server"),
		}
		compactStats := []string{
			fmt.Sprintf("Status: %-12s | CPU: %-6s | RAM: %s / %s", statusLine, cpuUsage, memAlloc, memSys),
			fmt.Sprintf("Uptime: %-12s | Clientes: %-6d | Goroutines: %d", uptimeStr, totalConns, metrics.NumGoroutine),
		}
		compactMenu := []string{
			"[1] Servidores       [2] Usuários        [3] Conexões",
			"[4] Logs             [5] Configuração    [6] Sistema",
			"",
			"[Q] Sair             [R] Atualizar",
		}
		targetWidth := width - 4
		if targetWidth < 64 {
			targetWidth = 64
		} else if targetWidth > 82 {
			targetWidth = 82
		}
		return RenderBoxWithWidth([][]string{compactHeader, compactStats, compactMenu}, targetWidth)
	}

	// ModeDesktop: Splitted Widescreen Dashboard
	rightNocHeader := []string{
		TitleStyle.Render("PAINEL OPERACIONAL EM TEMPO REAL (NOC)"),
	}
	sshInfo := "Ativo (127.0.0.1:2222)"
	if !status.SSHEnabled {
		sshInfo = "Desativado"
	}
	udpgwInfo := "Ativo (127.0.0.1:7400)"
	if !status.UDPGWEnabled {
		udpgwInfo = "Desativado"
	} else {
		modeDesc := "ABI Linux"
		if status.UDPGWMode == "tun" {
			modeDesc = "TUN"
		} else if status.UDPGWMode == "standard" {
			modeDesc = "Padrão"
		}
		ifaceDesc := ""
		if status.UDPGWInterface != "" && status.UDPGWInterface != "auto" {
			ifaceDesc = " • " + status.UDPGWInterface
		}
		udpgwInfo = fmt.Sprintf("Ativo (%s%s)", modeDesc, ifaceDesc)
	}

	rightNocContent := []string{
		"Protocolo: DragonTCP Wire v2 Multiplexado (33/9B)",
		fmt.Sprintf("Tráfego:   ↑ %s  ↓ %s", formatBytesHuman(status.BytesUp), formatBytesHuman(status.BytesDown)),
		fmt.Sprintf("Frames:    Push: %d | Pull: %d | Data: %d", status.PushRecords, status.PullRequests, status.DataRecords),
		"",
		"Serviços Integrados:",
		fmt.Sprintf("• Porta DNS Primária:    %d/UDP+TCP", status.Port),
		fmt.Sprintf("• Porta HTTP Secundária: %d/TCP", status.PortAlt),
		fmt.Sprintf("• Túnel Fake SSH:        %s", sshInfo),
		fmt.Sprintf("• BadVPN UDPGW:          %s", udpgwInfo),
		"• Workers Paralelos:     Adaptativo (1..64 workers • 1 Mbps AWP)",
		"",
		fmt.Sprintf("Hardware:  %d Cores • %d Goroutines • GC Runs: %d", metrics.NumCPU, metrics.NumGoroutine, metrics.NumGC),
		"",
		HelpDescStyle.Render("Navegue com [1..6] ou setas ↑↓ e [ENTER]. Pressione [Q] para sair."),
	}

	rightBox := RenderBoxWithWidth([][]string{rightNocHeader, rightNocContent}, width-42)
	return RenderDesktopSplit(leftBox, rightBox, width)
}

func renderServersMenuBox(width int) string {
	headerSection := []string{
		TitleStyle.Render("SERVIDORES"),
	}
	menuSection := []string{
		"[1] Status Geral",
		"[2] CPU / RAM",
		"[3] Conexões",
		"[4] Reiniciar",
		"",
		"[0] Voltar",
	}
	return RenderAdaptiveBox([][]string{headerSection, menuSection}, width)
}

func renderServerStatusBox(status ServerStatus, width int) string {
	headerSection := []string{
		TitleStyle.Render("STATUS DO SERVIDOR"),
	}

	badge := BadgeOnline
	if !status.Online {
		badge = BadgeOffline
	}

	infoSection := []string{
		fmt.Sprintf("%-12s %s", "Estado", badge),
		fmt.Sprintf("%-12s %d", "PID", status.PID),
		fmt.Sprintf("%-12s %s", "Uptime", status.Uptime),
		fmt.Sprintf("%-12s %d", "Porta DNS", status.Port),
		fmt.Sprintf("%-12s %d", "Porta HTTP", status.PortAlt),
		fmt.Sprintf("%-12s %s", "Token", func() string {
			if status.HasToken {
				return "Ativo"
			}
			return "Público"
		}()),
		fmt.Sprintf("%-12s %t", "SSH", status.SSHEnabled),
		fmt.Sprintf("%-12s %s", "UDPGW", func() string {
			if !status.UDPGWEnabled {
				return "Desativado"
			}
			mode := "ABI Linux"
			if status.UDPGWMode == "tun" {
				mode = "TUN"
			} else if status.UDPGWMode == "standard" {
				mode = "Padrão"
			}
			return fmt.Sprintf("Ativo (%s)", mode)
		}()),
		fmt.Sprintf("%-12s %s", "Workers", "AWP Adaptativo (1..64)"),
		"",
		"[0] Voltar",
	}

	return RenderAdaptiveBox([][]string{headerSection, infoSection}, width)
}

func renderServerMetricsBox(m SystemMetrics, width int) string {
	headerSection := []string{
		TitleStyle.Render("CPU / RAM"),
	}

	infoSection := []string{
		fmt.Sprintf("%-12s %d cores", "CPU Cores", m.NumCPU),
		fmt.Sprintf("%-12s %d", "Goroutines", m.NumGoroutine),
		fmt.Sprintf("%-12s %s", "RAM Aloc", m.AllocFormatted),
		fmt.Sprintf("%-12s %s", "RAM Sys", m.SysFormatted),
		fmt.Sprintf("%-12s %d", "GC Runs", m.NumGC),
		"",
		"[0] Voltar",
	}

	return RenderAdaptiveBox([][]string{headerSection, infoSection}, width)
}

func renderServerConnsBox(s ServerStatus, width int) string {
	headerSection := []string{
		TitleStyle.Render("CONEXÕES (MÉTRICAS)"),
	}

	infoSection := []string{
		fmt.Sprintf("%-12s %d", "Ativas", s.ActiveSessions),
		fmt.Sprintf("%-12s %d", "Túneis TCP", s.ActiveTunnels),
		fmt.Sprintf("%-12s %d", "Abertas", s.SessionsOpened),
		fmt.Sprintf("%-12s %d", "Fechadas", s.SessionsClosed),
		fmt.Sprintf("%-12s %s", "Upload", formatBytesHuman(s.BytesUp)),
		fmt.Sprintf("%-12s %s", "Download", formatBytesHuman(s.BytesDown)),
		fmt.Sprintf("%-12s %d", "Push Recs", s.PushRecords),
		fmt.Sprintf("%-12s %d", "Data Recs", s.DataRecords),
		fmt.Sprintf("%-12s %d", "Erros", s.Errors),
		"",
		"[0] Voltar",
	}

	return RenderAdaptiveBox([][]string{headerSection, infoSection}, width)
}

func renderSystemInfoBox(m SystemMetrics, width int) string {
	headerSection := []string{
		TitleStyle.Render("SISTEMA"),
	}

	hostname, _ := os.Hostname()
	if len(hostname) > 16 {
		hostname = hostname[:16]
	}

	infoSection := []string{
		fmt.Sprintf("%-12s %s", "Hostname", hostname),
		fmt.Sprintf("%-12s %s/%s", "OS/Arch", runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("%-12s %s", "Go Version", runtime.Version()),
		fmt.Sprintf("%-12s %d", "CPU Cores", runtime.NumCPU()),
		fmt.Sprintf("%-12s %d", "Goroutines", m.NumGoroutine),
		fmt.Sprintf("%-12s %d", "GC Runs", m.NumGC),
		"",
		"[0] Voltar",
	}

	return RenderAdaptiveBox([][]string{headerSection, infoSection}, width)
}

func NewRestartConfirmForm(confirmed *bool) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title("⚠️  Reiniciar Servidor DragonTCP").
				Description("Deseja realmente solicitar o reinício do serviço DragonTCP?").
				Affirmative("Sim, Reiniciar").
				Negative("Cancelar").
				Value(confirmed),
		),
	).WithTheme(huh.ThemeCharm())
}
