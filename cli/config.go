package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
)

type ConfigSection int

const (
	ConfigSectionNetwork ConfigSection = iota
	ConfigSectionSecurity
	ConfigSectionTuning
	ConfigSectionDNS
	ConfigSectionSSH
	ConfigSectionUDPGW
	ConfigSectionDiagnostics
)

// calculateParallelWorkers calculates how many concurrent connections/workers are required
// to achieve 1024 KB (1 Mbps equivalent) per round based on the calibrated safe chunkSize.
// Formula: workers = ceil(1024 * 1024 / chunkSize), clamped to [1, 64].
func calculateParallelWorkers(chunkSize int) int {
	if chunkSize <= 0 {
		return 1
	}
	targetBytes := 1024 * 1024 // 1024 KB = 1 Mbps per round
	workers := (targetBytes + chunkSize - 1) / chunkSize
	if workers < 1 {
		workers = 1
	}
	if workers > 64 {
		workers = 64
	}
	return workers
}

// NewConfigSectionKeyMap creates a smooth navigation keymap for Huh forms,
// supporting Down/Up arrow keys as well as Enter and Tab for seamless terminal experience.
func NewConfigSectionKeyMap() *huh.KeyMap {
	km := huh.NewDefaultKeyMap()
	km.Input.Next = key.NewBinding(key.WithKeys("enter", "tab", "down"))
	km.Input.Prev = key.NewBinding(key.WithKeys("shift+tab", "up"))
	km.Confirm.Next = key.NewBinding(key.WithKeys("enter", "tab", "down"))
	km.Confirm.Prev = key.NewBinding(key.WithKeys("shift+tab", "up"))
	km.Select.Next = key.NewBinding(key.WithKeys("enter", "tab"))
	km.Select.Prev = key.NewBinding(key.WithKeys("shift+tab"))
	return km
}

type ConfigFormVars struct {
	// Network
	Host       string
	PortStr    string
	PortAltStr string

	// Security
	Token        string
	MaxConnsStr  string
	AllowPrivate bool

	// Tuning & Workers
	ChunkMaxStr      string
	ChunkBufferedStr string
	ChunkPollWait    string
	ChunkSessionTime string
	TCPBufferStr     string

	// DNS
	DNSCacheTTL     string
	DNSCacheSizeStr string

	// SSH
	SSHEnable       bool
	SSHListen       string
	SSHInternalHost string
	SSHHostKey      string
	SSHUsers        string

	// UDPGW
	UDPGWEnable        bool
	UDPGWListen        string
	UDPGWInternalHost  string
	UDPGWMaxClientsStr string
	UDPGWMode          string // "native" (ABI Linux), "tun" (TUN device), "standard" (BadVPN)
	UDPGWInterface     string // "auto", "eth0", etc.
	UDPGWBusyPollUSStr string // SO_BUSY_POLL in µs (padrão: 50)
	UDPGWDebug         bool

	// Diagnostics
	AdminAddr          string
	Debug              bool
	DebugChunks        bool
	DebugStatsInterval string
}

func (v *ConfigFormVars) LoadFrom(cfg *YAMLConfig) {
	v.Host = cfg.Host
	v.PortStr = strconv.Itoa(cfg.Port)
	v.PortAltStr = strconv.Itoa(cfg.PortAlt)

	v.Token = cfg.Token
	v.MaxConnsStr = strconv.Itoa(cfg.MaxConnections)
	v.AllowPrivate = cfg.AllowPrivate

	v.ChunkMaxStr = strconv.Itoa(cfg.ChunkMax)
	v.ChunkBufferedStr = strconv.Itoa(cfg.ChunkBuffered)
	v.ChunkPollWait = cfg.ChunkPollWait
	v.ChunkSessionTime = cfg.ChunkSessionTime
	v.TCPBufferStr = strconv.Itoa(cfg.TCPBuffer)

	v.DNSCacheTTL = cfg.DNSCacheTTL
	v.DNSCacheSizeStr = strconv.Itoa(cfg.DNSCacheSize)

	v.SSHEnable = cfg.SSH.Enable
	v.SSHListen = cfg.SSH.Listen
	v.SSHInternalHost = cfg.SSH.InternalHost
	v.SSHHostKey = cfg.SSH.HostKey
	v.SSHUsers = cfg.SSH.Users

	v.UDPGWEnable = cfg.UDPGW.Enable
	v.UDPGWListen = cfg.UDPGW.Listen
	v.UDPGWInternalHost = cfg.UDPGW.InternalHost
	v.UDPGWMaxClientsStr = strconv.Itoa(cfg.UDPGW.MaxClients)
	v.UDPGWMode = cfg.UDPGW.Mode
	if v.UDPGWMode == "" {
		v.UDPGWMode = "native"
	}
	v.UDPGWInterface = cfg.UDPGW.Interface
	if v.UDPGWInterface == "" {
		v.UDPGWInterface = "auto"
	}
	v.UDPGWBusyPollUSStr = strconv.Itoa(cfg.UDPGW.BusyPollUS)
	if cfg.UDPGW.BusyPollUS <= 0 && cfg.UDPGW.Mode != "standard" {
		v.UDPGWBusyPollUSStr = "50"
	}
	v.UDPGWDebug = cfg.UDPGW.Debug

	v.AdminAddr = cfg.AdminAddr
	v.Debug = cfg.Debug
	v.DebugChunks = cfg.DebugChunks
	v.DebugStatsInterval = cfg.DebugStatsInterval
}

func (v *ConfigFormVars) ApplyTo(cfg *YAMLConfig) {
	cfg.Host = strings.TrimSpace(v.Host)
	if p, err := strconv.Atoi(strings.TrimSpace(v.PortStr)); err == nil {
		cfg.Port = p
	}
	if pa, err := strconv.Atoi(strings.TrimSpace(v.PortAltStr)); err == nil {
		cfg.PortAlt = pa
	}

	cfg.Token = strings.TrimSpace(v.Token)
	if mc, err := strconv.Atoi(strings.TrimSpace(v.MaxConnsStr)); err == nil {
		cfg.MaxConnections = mc
	}
	cfg.AllowPrivate = v.AllowPrivate

	if cm, err := strconv.Atoi(strings.TrimSpace(v.ChunkMaxStr)); err == nil {
		cfg.ChunkMax = cm
	}
	if cb, err := strconv.Atoi(strings.TrimSpace(v.ChunkBufferedStr)); err == nil {
		cfg.ChunkBuffered = cb
	}
	cfg.ChunkPollWait = strings.TrimSpace(v.ChunkPollWait)
	cfg.ChunkSessionTime = strings.TrimSpace(v.ChunkSessionTime)
	if tb, err := strconv.Atoi(strings.TrimSpace(v.TCPBufferStr)); err == nil {
		cfg.TCPBuffer = tb
	}

	cfg.DNSCacheTTL = strings.TrimSpace(v.DNSCacheTTL)
	if ds, err := strconv.Atoi(strings.TrimSpace(v.DNSCacheSizeStr)); err == nil {
		cfg.DNSCacheSize = ds
	}

	cfg.SSH.Enable = v.SSHEnable
	cfg.SSH.Listen = strings.TrimSpace(v.SSHListen)
	cfg.SSH.InternalHost = strings.TrimSpace(v.SSHInternalHost)
	cfg.SSH.HostKey = strings.TrimSpace(v.SSHHostKey)
	cfg.SSH.Users = strings.TrimSpace(v.SSHUsers)

	cfg.UDPGW.Enable = v.UDPGWEnable
	cfg.UDPGW.Listen = strings.TrimSpace(v.UDPGWListen)
	cfg.UDPGW.InternalHost = strings.TrimSpace(v.UDPGWInternalHost)
	if mc, err := strconv.Atoi(strings.TrimSpace(v.UDPGWMaxClientsStr)); err == nil {
		cfg.UDPGW.MaxClients = mc
	}
	cfg.UDPGW.Mode = strings.TrimSpace(v.UDPGWMode)
	cfg.UDPGW.Interface = strings.TrimSpace(v.UDPGWInterface)
	if bp, err := strconv.Atoi(strings.TrimSpace(v.UDPGWBusyPollUSStr)); err == nil {
		cfg.UDPGW.BusyPollUS = bp
	}
	cfg.UDPGW.Debug = v.UDPGWDebug

	cfg.AdminAddr = strings.TrimSpace(v.AdminAddr)
	cfg.Debug = v.Debug
	cfg.DebugChunks = v.DebugChunks
	cfg.DebugStatsInterval = strings.TrimSpace(v.DebugStatsInterval)
}

func renderConfigMenuView(cfg *YAMLConfig, width int) string {
	mode := GetViewMode(width)

	headerSection := []string{
		TitleStyle.Render("CONFIGURAÇÃO (dragontcp.yaml)"),
	}

	workers := calculateParallelWorkers(cfg.ChunkMax)

	if mode == ModeMobile {
		udpgwShort := "Off"
		if cfg.UDPGW.Enable {
			switch cfg.UDPGW.Mode {
			case "tun":
				udpgwShort = "TUN"
			case "standard":
				udpgwShort = "Pad"
			default:
				udpgwShort = "ABI"
			}
		}

		menuSection := []string{
			"[1] Rede & Portas",
			"[2] Segurança & Limites",
			fmt.Sprintf("[3] Tuning & Chunks (%d wrk)", workers),
			"[4] Cache DNS",
			"[5] Fake SSH Interno",
			fmt.Sprintf("[6] UDPGW (%s)", udpgwShort),
			"[7] Diagnósticos & Admin",
			"",
			"[8] Salvar no Arquivo",
			"[R] Recarregar",
			"",
			"[0] Voltar",
		}
		return RenderBoxWithWidth([][]string{headerSection, menuSection}, MobileInnerWidth)
	}

	// ModeCompact or ModeDesktop: show options with live preview values
	sshStatus := "Ativo (porta " + cfg.SSH.Listen + ")"
	if !cfg.SSH.Enable {
		sshStatus = "Desativado"
	}
	udpgwStatus := "Desativado"
	if cfg.UDPGW.Enable {
		modeName := "ABI Linux"
		switch cfg.UDPGW.Mode {
		case "tun":
			modeName = "TUN"
		case "standard":
			modeName = "Padrão"
		}
		udpgwStatus = fmt.Sprintf("Ativo (%s | %s)", cfg.UDPGW.Listen, modeName)
	}
	tokenDisplay := "Público"
	if cfg.Token != "" {
		tokenDisplay = "Definido"
	}

	menuSection := []string{
		fmt.Sprintf("[1] %-24s Host: %s | Portas: %d / %d", "Rede & Portas", cfg.Host, cfg.Port, cfg.PortAlt),
		fmt.Sprintf("[2] %-24s Max Conns: %d | Token: %s", "Segurança & Limites", cfg.MaxConnections, tokenDisplay),
		fmt.Sprintf("[3] %-24s Chunks: %d B | Workers: %d | Wait: %s", "Tuning & Chunks", cfg.ChunkMax, workers, cfg.ChunkPollWait),
		fmt.Sprintf("[4] %-24s TTL: %s | Tamanho: %d", "Cache DNS", cfg.DNSCacheTTL, cfg.DNSCacheSize),
		fmt.Sprintf("[5] %-24s %s", "Fake SSH Interno", sshStatus),
		fmt.Sprintf("[6] %-24s %s", "BadVPN UDPGW", udpgwStatus),
		fmt.Sprintf("[7] %-24s Admin: %s | Debug: %t", "Diagnósticos & Admin", cfg.AdminAddr, cfg.Debug),
		"",
		"[8] Salvar Alterações no YAML       [R] Recarregar do Disco",
		"",
		"[0] Voltar",
	}

	targetWidth := width - 4
	if targetWidth < 68 {
		targetWidth = 68
	} else if targetWidth > 90 {
		targetWidth = 90
	}
	return RenderBoxWithWidth([][]string{headerSection, menuSection}, targetWidth)
}

func NewConfigSectionForm(sec ConfigSection, vars *ConfigFormVars, confirmed *bool) *huh.Form {
	var fields []huh.Field
	theme := huh.ThemeCharm()

	switch sec {
	case ConfigSectionNetwork:
		fields = []huh.Field{
			huh.NewInput().
				Title("Listen Host / Interface").
				Description("Interface de rede IP para escuta (padrão: 0.0.0.0)").
				Value(&vars.Host),
			huh.NewInput().
				Title("Porta Primária (DNS)").
				Description("Porta padrão do túnel (padrão: 53)").
				Value(&vars.PortStr),
			huh.NewInput().
				Title("Porta Secundária (HTTP)").
				Description("Porta alternativa simultânea (ex: 80, ou 0 para desativar)").
				Value(&vars.PortAltStr),
		}

	case ConfigSectionSecurity:
		fields = []huh.Field{
			huh.NewInput().
				Title("Token de Autorização").
				Description("Deixe vazio para acesso público sem token").
				Value(&vars.Token),
			huh.NewInput().
				Title("Máximo de Conexões").
				Description("Limite total de túneis simultâneos (ex: 20000)").
				Value(&vars.MaxConnsStr),
			huh.NewConfirm().
				Title("Permitir alvos em IPs privados?").
				Description("allow_private: autoriza túneis apontarem para localhost ou 192.168.x.x").
				Affirmative("Sim").
				Negative("Não").
				Value(&vars.AllowPrivate),
		}

	case ConfigSectionTuning:
		currentChunk, _ := strconv.Atoi(strings.TrimSpace(vars.ChunkMaxStr))
		if currentChunk <= 0 {
			currentChunk = 1048576
		}
		workers := calculateParallelWorkers(currentChunk)
		workersNote := fmt.Sprintf("Chunk de %d bytes resulta em %d workers paralelos (alvo 1024 KB / 1 Mbps).", currentChunk, workers)

		fields = []huh.Field{
			huh.NewNote().
				Title("Escalonamento de Workers Paralelos (AWP)").
				Description(workersNote),
			huh.NewInput().
				Title("Chunk Máximo (Bytes)").
				Description("Tamanho máximo do payload adaptativo (ex: 1048576 = 1 worker, 16384 = 64 workers)").
				Value(&vars.ChunkMaxStr),
			huh.NewInput().
				Title("Chunk Buffered (Blocos 64K)").
				Description("Buffer de download por sessão (ex: 32 = ~2 MiB, 64 = ~4 MiB)").
				Value(&vars.ChunkBufferedStr),
			huh.NewInput().
				Title("Long-poll Wait Interval").
				Description("Espera pelo alvo no primeiro chunk (ex: 200ms)").
				Value(&vars.ChunkPollWait),
			huh.NewInput().
				Title("Timeout de Sessão Inativa").
				Description("Tempo para expirar sessões órfãs (ex: 2m)").
				Value(&vars.ChunkSessionTime),
			huh.NewInput().
				Title("TCP Buffer Socket").
				Description("Buffer socket SO_RCVBUF/SO_SNDBUF em bytes (0 = kernel autotune)").
				Value(&vars.TCPBufferStr),
		}

	case ConfigSectionDNS:
		fields = []huh.Field{
			huh.NewInput().
				Title("TTL do Cache DNS").
				Description("Tempo de vida em cache para resoluções DNS (ex: 30s)").
				Value(&vars.DNSCacheTTL),
			huh.NewInput().
				Title("Tamanho Máximo do Cache DNS").
				Description("Número máximo de registros de resolução cacheados (ex: 4096)").
				Value(&vars.DNSCacheSizeStr),
		}

	case ConfigSectionSSH:
		fields = []huh.Field{
			huh.NewConfirm().
				Title("Habilitar Fake SSH Interno?").
				Affirmative("Sim (habilitado)").
				Negative("Não (desabilitado)").
				Value(&vars.SSHEnable),
			huh.NewInput().
				Title("SSH Listen Address").
				Description("Endereço de bind interno (ex: 127.0.0.1:2222)").
				Value(&vars.SSHListen),
			huh.NewInput().
				Title("SSH Hostname Interno").
				Description("Domínio interceptado para SSH (ex: dragontcp-ssh.internal)").
				Value(&vars.SSHInternalHost),
			huh.NewInput().
				Title("Arquivo de Chave Host SSH").
				Description("Arquivo da chave privada ed25519 do servidor SSH").
				Value(&vars.SSHHostKey),
			huh.NewInput().
				Title("Banco de Usuários SSH (JSON)").
				Description("Caminho do arquivo de logins e senhas (dragontcp-users.json)").
				Value(&vars.SSHUsers),
		}

	case ConfigSectionUDPGW:
		fields = []huh.Field{
			huh.NewConfirm().
				Title("Habilitar BadVPN UDPGW?").
				Affirmative("Sim (habilitado)").
				Negative("Não (desabilitado)").
				Value(&vars.UDPGWEnable),
			huh.NewSelect[string]().
				Title("Modo de Descarga UDP").
				Description("Selecione a engine de processamento e descarga de pacotes UDP").
				Options(
					huh.NewOption("Descarga ABI Linux (SO_BINDTODEVICE + SO_BUSY_POLL)", "native"),
					huh.NewOption("Dispositivo TUN (/dev/net/tun)", "tun"),
					huh.NewOption("UDPGW Padrão (BadVPN compatível / sem root)", "standard"),
				).
				Value(&vars.UDPGWMode),
			huh.NewInput().
				Title("Interface de Rede (NIC)").
				Description("Placa física para SO_BINDTODEVICE (ex: auto, eth0, ens3, bond0)").
				Value(&vars.UDPGWInterface),
			huh.NewInput().
				Title("SO_BUSY_POLL (µs)").
				Description("Polling direto na fila da NIC (padrão: 50µs, 0 para desativar)").
				Value(&vars.UDPGWBusyPollUSStr),
			huh.NewInput().
				Title("UDPGW Listen Address").
				Description("Endereço de bind interno (ex: 127.0.0.1:7400)").
				Value(&vars.UDPGWListen),
			huh.NewInput().
				Title("UDPGW Hostname Interno").
				Description("Domínio interceptado para UDPGW (ex: dragontcp-udpgw.internal)").
				Value(&vars.UDPGWInternalHost),
			huh.NewInput().
				Title("Máximo de Clientes UDPGW").
				Description("Limite concorrente de clientes UDP (ex: 10000)").
				Value(&vars.UDPGWMaxClientsStr),
			huh.NewConfirm().
				Title("Logs Detalhados UDPGW (debug)?").
				Affirmative("Sim").
				Negative("Não").
				Value(&vars.UDPGWDebug),
		}

	case ConfigSectionDiagnostics:
		fields = []huh.Field{
			huh.NewInput().
				Title("Endereço Admin / CLI API").
				Description("Bind HTTP da API administrativa local (ex: 127.0.0.1:53080)").
				Value(&vars.AdminAddr),
			huh.NewConfirm().
				Title("Modo Debug Geral?").
				Description("Registra eventos de conexões e estatísticas").
				Affirmative("Sim").
				Negative("Não").
				Value(&vars.Debug),
			huh.NewConfirm().
				Title("Debug de Chunks?").
				Description("Muito verboso: grava cada frame binário trafegado").
				Affirmative("Sim").
				Negative("Não").
				Value(&vars.DebugChunks),
			huh.NewInput().
				Title("Intervalo de Estatísticas Debug").
				Description("Intervalo periódico de log (ex: 5s, ou 0 para desativar)").
				Value(&vars.DebugStatsInterval),
		}
	}

	fields = append(fields, huh.NewConfirm().
		Title("Salvar alterações desta seção?").
		Affirmative("Salvar").
		Negative("Cancelar").
		Value(confirmed))

	return huh.NewForm(
		huh.NewGroup(fields...),
	).WithTheme(theme).WithKeyMap(NewConfigSectionKeyMap())
}
