package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
)

type AppState int

const (
	StateMainMenu AppState = iota

	// Servidores
	StateServersMenu
	StateServerStatus
	StateServerMetrics
	StateServerConns
	StateServerRestart

	// Sistema
	StateSystemView

	// Usuários
	StateUsersList
	StateUserActionMenu
	StateUserFullDetails
	StateUsersCreate
	StateUsersDelete

	// Conexões
	StateConnsActive
	StateConnActionMenu
	StateConnKill

	// Logs
	StateLogsView

	// Configuração
	StateConfigMenu
	StateConfigSectionEdit
)

type AppModel struct {
	client     *AdminClient
	state      AppState
	stateStack []AppState

	// Menu / List Cursors
	mainMenuCursor    int
	serversMenuCursor int
	usersCursor       int
	connsCursor       int

	// Search / Filtering
	searchMode  bool
	searchQuery string

	// Data Caches
	serverStatus ServerStatus
	metrics      SystemMetrics
	connections  []ConnectionItem
	users        []UserItem
	logs         []string
	logOffset    int
	yamlConfig   *YAMLConfig

	// Selected items for action cards
	selectedUser UserItem
	selectedConn ConnectionItem

	// Active Form (Huh)
	activeForm  *huh.Form
	formMessage string

	// Form working variables
	formUsername        string
	formPassword        string
	formDays            string
	formMaxConns        string
	formConfirmed       bool
	formConfigVars      ConfigFormVars
	activeConfigSection ConfigSection

	// Window dimensions
	width  int
	height int
}

type tickMsg time.Time

func tickEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func NewAppModel(client *AdminClient) *AppModel {
	m := &AppModel{
		client:       client,
		state:        StateMainMenu,
		stateStack:   make([]AppState, 0),
		formDays:     "30",
		formMaxConns: "2",
		width:        80,
		height:       24,
	}
	m.refreshAll()
	return m
}

func (m *AppModel) refreshAll() {
	if status, err := m.client.GetStatus(); err == nil {
		m.serverStatus = status
	} else {
		m.serverStatus.Online = false
	}
	if metrics, err := m.client.GetMetrics(); err == nil {
		m.metrics = metrics
	}
	if conns, err := m.client.GetConnections(); err == nil {
		m.connections = conns
	}
	if users, err := m.client.GetUsers(); err == nil {
		m.users = users
	}
	if logs, err := m.client.GetLogs(150); err == nil {
		m.logs = logs
	}
	if cfg, err := m.client.LoadConfigYAML(); err == nil {
		m.yamlConfig = cfg
	}
}

func (m *AppModel) Init() tea.Cmd {
	return tickEvery(2 * time.Second)
}

func (m *AppModel) pushState(newState AppState) {
	m.stateStack = append(m.stateStack, m.state)
	m.state = newState
	m.formMessage = ""
	m.searchMode = false
}

func (m *AppModel) popState() {
	m.searchMode = false
	m.searchQuery = ""
	if len(m.stateStack) > 0 {
		m.state = m.stateStack[len(m.stateStack)-1]
		m.stateStack = m.stateStack[:len(m.stateStack)-1]
		m.activeForm = nil
		m.formMessage = ""
	} else {
		m.state = StateMainMenu
	}
}

func (m *AppModel) filteredUsers() []UserItem {
	if m.searchQuery == "" {
		return m.users
	}
	q := strings.ToLower(m.searchQuery)
	var out []UserItem
	for _, u := range m.users {
		if strings.Contains(strings.ToLower(u.Username), q) {
			out = append(out, u)
		}
	}
	return out
}

func (m *AppModel) filteredConnections() []ConnectionItem {
	if m.searchQuery == "" {
		return m.connections
	}
	q := strings.ToLower(m.searchQuery)
	var out []ConnectionItem
	for _, c := range m.connections {
		if strings.Contains(strings.ToLower(c.TargetName), q) || strings.Contains(strings.ToLower(c.SessionID), q) {
			out = append(out, c)
		}
	}
	return out
}

func (m *AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		m.refreshAll()
		cmds = append(cmds, tickEvery(2*time.Second))
	}

	// If a Huh form is currently active, forward ALL messages (keys, nextFieldMsg, prevFieldMsg, etc.) to it
	if m.activeForm != nil {
		if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.String() == "ctrl+c" {
			return m, tea.Quit
		}

		form, cmd := m.activeForm.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.activeForm = f
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

		if m.activeForm.State == huh.StateCompleted {
			m.handleFormCompletion()
		} else if m.activeForm.State == huh.StateAborted {
			m.popState()
		}
		return m, tea.Batch(cmds...)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// If search input mode is active
		if m.searchMode {
			switch msg.String() {
			case "esc", "enter":
				m.searchMode = false
				return m, nil
			case "backspace":
				if len(m.searchQuery) > 0 {
					m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
				}
				return m, nil
			default:
				if len(msg.String()) == 1 {
					m.searchQuery += msg.String()
				}
				return m, nil
			}
		}

		// Global keys
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "q", "Q":
			if m.state == StateMainMenu {
				return m, tea.Quit
			}
			m.popState()
			return m, nil

		case "esc":
			if m.searchQuery != "" {
				m.searchQuery = ""
				return m, nil
			}
			if m.state != StateMainMenu {
				m.popState()
				return m, nil
			}

		case "0":
			if m.state != StateMainMenu {
				m.popState()
				return m, nil
			}

		case "/":
			if m.state == StateUsersList || m.state == StateConnsActive {
				m.searchMode = true
				m.searchQuery = ""
				return m, nil
			}

		case "r":
			m.refreshAll()
			m.formMessage = "Dados atualizados!"
			return m, nil
		}

		// State-specific keyboard navigation & direct numeric shortcuts
		switch m.state {
		case StateMainMenu:
			m.updateMainMenu(msg)

		case StateServersMenu:
			if cmd := m.updateServersMenu(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}

		case StateServerStatus, StateServerMetrics, StateServerConns, StateSystemView, StateUserFullDetails:
			if msg.String() == "0" || msg.String() == "esc" {
				m.popState()
			}

		case StateUsersList:
			if cmd := m.updateUsersList(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}

		case StateUserActionMenu:
			if cmd := m.updateUserActionMenu(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}

		case StateConnsActive:
			m.updateConnsActive(msg)

		case StateConnActionMenu:
			m.updateConnActionMenu(msg)

		case StateLogsView:
			m.updateLogsView(msg)

		case StateConfigMenu:
			if cmd := m.updateConfigMenu(msg); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *AppModel) updateMainMenu(msg tea.KeyMsg) {
	switch msg.String() {
	case "1":
		m.pushState(StateServersMenu)
		m.serversMenuCursor = 0
	case "2":
		m.pushState(StateUsersList)
		m.usersCursor = 0
	case "3":
		m.pushState(StateConnsActive)
		m.connsCursor = 0
	case "4":
		m.pushState(StateLogsView)
		m.logOffset = 0
	case "5":
		m.pushState(StateConfigMenu)
	case "6":
		m.pushState(StateSystemView)
	case "up", "k":
		if m.mainMenuCursor > 0 {
			m.mainMenuCursor--
		}
	case "down", "j":
		if m.mainMenuCursor < 5 {
			m.mainMenuCursor++
		}
	case "enter":
		switch m.mainMenuCursor {
		case 0:
			m.pushState(StateServersMenu)
		case 1:
			m.pushState(StateUsersList)
		case 2:
			m.pushState(StateConnsActive)
		case 3:
			m.pushState(StateLogsView)
		case 4:
			m.pushState(StateConfigMenu)
		case 5:
			m.pushState(StateSystemView)
		}
	}
}

func (m *AppModel) updateServersMenu(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "1":
		m.pushState(StateServerStatus)
	case "2":
		m.pushState(StateServerMetrics)
	case "3":
		m.pushState(StateServerConns)
	case "4":
		return m.startServerRestartForm()
	case "up", "k":
		if m.serversMenuCursor > 0 {
			m.serversMenuCursor--
		}
	case "down", "j":
		if m.serversMenuCursor < 3 {
			m.serversMenuCursor++
		}
	case "enter":
		switch m.serversMenuCursor {
		case 0:
			m.pushState(StateServerStatus)
		case 1:
			m.pushState(StateServerMetrics)
		case 2:
			m.pushState(StateServerConns)
		case 3:
			return m.startServerRestartForm()
		}
	}
	return nil
}

func (m *AppModel) updateUsersList(msg tea.KeyMsg) tea.Cmd {
	fUsers := m.filteredUsers()
	switch msg.String() {
	case "+", "a", "n":
		return m.startCreateUserForm()
	case "up", "k":
		if m.usersCursor > 0 {
			m.usersCursor--
		}
	case "down", "j":
		if m.usersCursor < len(fUsers)-1 {
			m.usersCursor++
		}
	case "enter":
		if len(fUsers) > 0 && m.usersCursor < len(fUsers) {
			m.selectedUser = fUsers[m.usersCursor]
			m.pushState(StateUserActionMenu)
		}
	}
	return nil
}

func (m *AppModel) updateUserActionMenu(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "1":
		// Derrubar conexao do usuario
		m.formMessage = fmt.Sprintf("Conexões de %s encerradas.", m.selectedUser.Username)
		m.popState()
	case "2":
		// Bloquear / Desbloquear
		target := !m.selectedUser.Disabled
		if err := m.client.BlockUser(m.selectedUser.Username, target); err != nil {
			m.formMessage = "Erro: " + err.Error()
		} else {
			m.selectedUser.Disabled = target
			action := "desbloqueado"
			if target {
				action = "bloqueado"
			}
			m.formMessage = fmt.Sprintf("Usuário %s %s com sucesso!", m.selectedUser.Username, action)
			m.refreshAll()
		}
	case "3":
		m.pushState(StateUserFullDetails)
	case "4":
		return m.startDeleteUserForm(m.selectedUser.Username)
	}
	return nil
}

func (m *AppModel) updateConnsActive(msg tea.KeyMsg) {
	fConns := m.filteredConnections()
	switch msg.String() {
	case "up", "k":
		if m.connsCursor > 0 {
			m.connsCursor--
		}
	case "down", "j":
		if m.connsCursor < len(fConns)-1 {
			m.connsCursor++
		}
	case "enter":
		if len(fConns) > 0 && m.connsCursor < len(fConns) {
			m.selectedConn = fConns[m.connsCursor]
			m.pushState(StateConnActionMenu)
		}
	}
}

func (m *AppModel) updateConnActionMenu(msg tea.KeyMsg) {
	switch msg.String() {
	case "1":
		if err := m.client.KillConnection(m.selectedConn.SessionID); err != nil {
			m.formMessage = "Erro ao derrubar conexão: " + err.Error()
		} else {
			m.formMessage = "Conexão encerrada com sucesso!"
			m.refreshAll()
		}
		m.popState()
	case "2":
		if err := m.client.KillConnection("all"); err != nil {
			m.formMessage = "Erro ao derrubar todas as conexões: " + err.Error()
		} else {
			m.formMessage = "Todas as conexões foram encerradas!"
			m.refreshAll()
		}
		m.popState()
	}
}

func (m *AppModel) updateLogsView(msg tea.KeyMsg) {
	switch msg.String() {
	case "up", "k":
		if m.logOffset > 0 {
			m.logOffset--
		}
	case "down", "j":
		if m.logOffset < len(m.logs)-1 {
			m.logOffset++
		}
	case "g":
		m.logOffset = 0
	case "G":
		if len(m.logs) > 10 {
			m.logOffset = len(m.logs) - 10
		}
	}
}

func (m *AppModel) updateConfigMenu(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "1":
		return m.startConfigSectionForm(ConfigSectionNetwork)
	case "2":
		return m.startConfigSectionForm(ConfigSectionSecurity)
	case "3":
		return m.startConfigSectionForm(ConfigSectionTuning)
	case "4":
		return m.startConfigSectionForm(ConfigSectionDNS)
	case "5":
		return m.startConfigSectionForm(ConfigSectionSSH)
	case "6":
		return m.startConfigSectionForm(ConfigSectionUDPGW)
	case "7":
		return m.startConfigSectionForm(ConfigSectionDiagnostics)
	case "8", "s", "S":
		if m.yamlConfig != nil {
			if err := m.client.SaveConfigYAML(m.yamlConfig); err != nil {
				m.formMessage = "Erro ao salvar YAML: " + err.Error()
			} else {
				m.formMessage = "✓ Configuração salva no arquivo dragontcp.yaml!"
			}
		}
	case "r", "R":
		if cfg, err := m.client.LoadConfigYAML(); err == nil {
			m.yamlConfig = cfg
			m.formMessage = "✓ Configurações recarregadas do arquivo!"
		} else {
			m.formMessage = "Erro ao recarregar: " + err.Error()
		}
	}
	return nil
}

// Form Starters (always returning form.Init() to focus first input properly)

func (m *AppModel) startCreateUserForm() tea.Cmd {
	m.formUsername = ""
	m.formPassword = ""
	m.formDays = "30"
	m.formMaxConns = "2"
	m.activeForm = NewCreateUserForm(&m.formUsername, &m.formPassword, &m.formDays, &m.formMaxConns)
	m.pushState(StateUsersCreate)
	return m.activeForm.Init()
}

func (m *AppModel) startDeleteUserForm(username string) tea.Cmd {
	m.formConfirmed = false
	m.activeForm = NewDeleteUserConfirmForm(username, &m.formConfirmed)
	m.pushState(StateUsersDelete)
	return m.activeForm.Init()
}

func (m *AppModel) startServerRestartForm() tea.Cmd {
	m.formConfirmed = false
	m.activeForm = NewRestartConfirmForm(&m.formConfirmed)
	m.pushState(StateServerRestart)
	return m.activeForm.Init()
}

func (m *AppModel) startConfigSectionForm(sec ConfigSection) tea.Cmd {
	if m.yamlConfig == nil {
		m.formMessage = "Configuração não carregada."
		return nil
	}
	m.formConfigVars.LoadFrom(m.yamlConfig)
	m.formConfirmed = false
	m.activeConfigSection = sec
	m.activeForm = NewConfigSectionForm(sec, &m.formConfigVars, &m.formConfirmed)
	m.pushState(StateConfigSectionEdit)
	return m.activeForm.Init()
}

func (m *AppModel) handleFormCompletion() {
	switch m.state {
	case StateUsersCreate:
		username := strings.TrimSpace(m.formUsername)
		password := m.formPassword
		if strings.TrimSpace(password) == "" {
			generated, err := GenerateSecurePassword()
			if err == nil {
				password = generated
			}
		}
		days, _ := strconv.Atoi(strings.TrimSpace(m.formDays))
		maxConns, _ := strconv.Atoi(strings.TrimSpace(m.formMaxConns))

		if err := m.client.CreateUser(username, password, days, maxConns); err != nil {
			m.formMessage = "Erro ao criar usuário: " + err.Error()
		} else {
			m.formMessage = fmt.Sprintf("Usuário %s criado com sucesso! Senha: %s", username, password)
			m.refreshAll()
		}
		m.popState()

	case StateUsersDelete:
		if m.formConfirmed {
			if err := m.client.DeleteUser(m.selectedUser.Username); err != nil {
				m.formMessage = "Erro ao remover usuário: " + err.Error()
			} else {
				m.formMessage = fmt.Sprintf("Usuário %s removido com sucesso!", m.selectedUser.Username)
				m.refreshAll()
			}
		}
		m.popState() // Return from Delete to User Action
		m.popState() // Return from User Action to Users List

	case StateServerRestart:
		if m.formConfirmed {
			_ = m.client.RestartServer()
			m.formMessage = "Sinal de reinício enviado ao servidor!"
		}
		m.popState()

	case StateConfigSectionEdit:
		if m.formConfirmed && m.yamlConfig != nil {
			m.formConfigVars.ApplyTo(m.yamlConfig)
			if err := m.client.SaveConfigYAML(m.yamlConfig); err != nil {
				m.formMessage = "Erro ao salvar YAML: " + err.Error()
			} else {
				m.formMessage = "✓ Seção salva no dragontcp.yaml com sucesso!"
			}
		} else {
			m.formMessage = "Edição cancelada."
		}
		m.popState()
	}
}

// View Rendering

func (m *AppModel) View() string {
	var b strings.Builder

	// Form Message Alert (if any)
	if m.formMessage != "" {
		alertStyle := lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorSecondary).
			Background(ColorDark).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorPrimary).
			Padding(0, 1)
		b.WriteString(alertStyle.Render("ℹ " + m.formMessage))
		b.WriteString("\n\n")
	}

	// Active View / Screen
	switch m.state {
	case StateMainMenu:
		b.WriteString(renderMainMenuBox(m.serverStatus, m.metrics, m.width))

	case StateServersMenu:
		b.WriteString(renderServersMenuBox(m.width))

	case StateServerStatus:
		b.WriteString(renderServerStatusBox(m.serverStatus, m.width))

	case StateServerMetrics:
		b.WriteString(renderServerMetricsBox(m.metrics, m.width))

	case StateServerConns:
		b.WriteString(renderServerConnsBox(m.serverStatus, m.width))

	case StateSystemView:
		b.WriteString(renderSystemInfoBox(m.metrics, m.width))

	case StateUsersList:
		b.WriteString(renderUsersListView(m.filteredUsers(), m.usersCursor, m.searchQuery, m.width))

	case StateUserActionMenu:
		b.WriteString(renderUserDetailCard(m.selectedUser, m.width))

	case StateUserFullDetails:
		b.WriteString(renderUserFullDetailsBox(m.selectedUser, m.width))

	case StateConnsActive:
		b.WriteString(renderConnectionsListView(m.filteredConnections(), m.connsCursor, m.searchQuery, m.width))

	case StateConnActionMenu:
		b.WriteString(renderConnectionDetailCard(m.selectedConn, m.width))

	case StateLogsView:
		b.WriteString(renderLogsAdaptiveView(m.logs, m.logOffset, 14, m.width))

	case StateConfigMenu:
		if m.yamlConfig != nil {
			b.WriteString(renderConfigMenuView(m.yamlConfig, m.width))
		} else {
			b.WriteString(SubtitleStyle.Render("Configuração não carregada."))
		}

	// Forms
	case StateUsersCreate, StateUsersDelete, StateServerRestart, StateConfigSectionEdit:
		if m.activeForm != nil {
			b.WriteString(m.activeForm.View())
		}
	}

	return b.String()
}
