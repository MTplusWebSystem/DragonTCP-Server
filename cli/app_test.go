package main

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAppModelViewsAndNavigation(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "dragontcp.yaml")
	usersPath := filepath.Join(tempDir, "users.json")

	client := NewAdminClient("http://127.0.0.1:53080", configPath, usersPath)
	app := NewAppModel(client)

	// 1. Initial State is Main Menu Box
	if app.state != StateMainMenu {
		t.Fatalf("expected StateMainMenu, got %v", app.state)
	}

	view := app.View()
	if !strings.Contains(view, "MTW SISTEMAS") || !strings.Contains(view, "DragonTCP") {
		t.Fatalf("main menu view missing expected labels:\n%s", view)
	}
	if !strings.Contains(view, "[1] Servidores") || !strings.Contains(view, "[2] Usuários") {
		t.Fatalf("main menu missing expected numeric options:\n%s", view)
	}

	// 2. Direct numeric hotkey: '1' -> Servidores Menu
	m, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	app = m.(*AppModel)
	if app.state != StateServersMenu {
		t.Fatalf("expected StateServersMenu, got %v", app.state)
	}
	view = app.View()
	if !strings.Contains(view, "SERVIDORES") || !strings.Contains(view, "[1] Status Geral") {
		t.Fatalf("servers menu missing expected options:\n%s", view)
	}

	// 3. Numeric hotkey: '1' -> Status Geral
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	app = m.(*AppModel)
	if app.state != StateServerStatus {
		t.Fatalf("expected StateServerStatus, got %v", app.state)
	}
	view = app.View()
	if !strings.Contains(view, "STATUS DO SERVIDOR") || !strings.Contains(view, "[0] Voltar") {
		t.Fatalf("server status view mismatch:\n%s", view)
	}

	// 4. Hotkey '0' to return to Servidores Menu
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	app = m.(*AppModel)
	if app.state != StateServersMenu {
		t.Fatalf("expected StateServersMenu after '0', got %v", app.state)
	}

	// 5. Hotkey '2' -> CPU / RAM
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	app = m.(*AppModel)
	if app.state != StateServerMetrics {
		t.Fatalf("expected StateServerMetrics, got %v", app.state)
	}
	view = app.View()
	if !strings.Contains(view, "CPU / RAM") {
		t.Fatalf("metrics view mismatch:\n%s", view)
	}

	// 6. Esc to return to Servers Menu, Esc to return to Main Menu
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app = m.(*AppModel)
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	app = m.(*AppModel)
	if app.state != StateMainMenu {
		t.Fatalf("expected StateMainMenu, got %v", app.state)
	}

	// 7. Direct numeric hotkey: '2' -> Usuários (Cards list)
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	app = m.(*AppModel)
	if app.state != StateUsersList {
		t.Fatalf("expected StateUsersList, got %v", app.state)
	}
	view = app.View()
	if !strings.Contains(view, "USUÁRIOS") {
		t.Fatalf("users list view mismatch:\n%s", view)
	}

	// 8. Add a dummy user and test selecting it into User Action Card
	_ = client.CreateUser("fabricio", "pass123", 30, 2)
	app.refreshAll()
	view = app.View()
	if !strings.Contains(view, "fabricio") {
		t.Fatalf("expected fabricio in user cards list:\n%s", view)
	}

	// Select fabricio (Enter) -> opens User Action Card
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = m.(*AppModel)
	if app.state != StateUserActionMenu {
		t.Fatalf("expected StateUserActionMenu, got %v", app.state)
	}
	view = app.View()
	if !strings.Contains(view, "USUÁRIO") || !strings.Contains(view, "fabricio") || !strings.Contains(view, "[1] Derrubar conexão") {
		t.Fatalf("user action card mismatch:\n%s", view)
	}

	// In action card: '2' toggles block
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	app = m.(*AppModel)
	if !app.selectedUser.Disabled {
		t.Fatal("expected fabricio to be blocked")
	}

	// '0' returns to Users List
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	app = m.(*AppModel)
	if app.state != StateUsersList {
		t.Fatalf("expected StateUsersList after '0', got %v", app.state)
	}

	// '0' returns to Main Menu
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'0'}})
	app = m.(*AppModel)
	if app.state != StateMainMenu {
		t.Fatalf("expected StateMainMenu, got %v", app.state)
	}

	// 9. Hotkey '6' -> Sistema
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	app = m.(*AppModel)
	if app.state != StateSystemView {
		t.Fatalf("expected StateSystemView, got %v", app.state)
	}
	view = app.View()
	if !strings.Contains(view, "SISTEMA") {
		t.Fatalf("system view mismatch:\n%s", view)
	}
}

func TestFormAdvancementThroughAppUpdate(t *testing.T) {
	tempDir := t.TempDir()
	client := NewAdminClient("http://127.0.0.1:53080", filepath.Join(tempDir, "dragontcp.yaml"), filepath.Join(tempDir, "users.json"))
	app := NewAppModel(client)

	// Enter Section 5: Fake SSH ('5' then '5')
	m, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	app = m.(*AppModel)
	m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	app = m.(*AppModel)

	if app.activeForm == nil {
		t.Fatal("expected activeForm to be non-nil")
	}

	// Press Enter on the first field (Habilitar Fake SSH Interno?)
	m, cmd = app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	app = m.(*AppModel)
	if cmd == nil {
		t.Fatal("expected cmd from pressing Enter")
	}

	// Forward the message generated by cmd() back to app.Update
	msg := cmd()
	m, _ = app.Update(msg)
	app = m.(*AppModel)

	view := app.View()
	// The focus bar ┃ must now be on "SSH Listen Address"
	if !strings.Contains(view, "SSH Listen Address") {
		t.Fatalf("expected view to contain SSH Listen Address:\n%s", view)
	}
}

func TestAdaptiveViewModes(t *testing.T) {
	tempDir := t.TempDir()
	client := NewAdminClient("http://127.0.0.1:53080", filepath.Join(tempDir, "dragontcp.yaml"), filepath.Join(tempDir, "users.json"))
	app := NewAppModel(client)

	// Mode 1: Mobile (< 60 colunas)
	if GetViewMode(40) != ModeMobile {
		t.Fatalf("expected ModeMobile for width 40")
	}
	m, _ := app.Update(tea.WindowSizeMsg{Width: 45, Height: 24})
	app = m.(*AppModel)
	mobileView := app.View()
	if !strings.Contains(mobileView, "MTW SISTEMAS") || !strings.Contains(mobileView, "DragonTCP") {
		t.Fatalf("expected mobile box in view:\n%s", mobileView)
	}

	// Mode 2: Compact (60–100 colunas)
	if GetViewMode(80) != ModeCompact {
		t.Fatalf("expected ModeCompact for width 80")
	}
	m, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app = m.(*AppModel)
	compactView := app.View()
	if !strings.Contains(compactView, "DragonTCP Server") {
		t.Fatalf("expected compact layout in view:\n%s", compactView)
	}

	// Mode 3: Desktop (> 100 colunas)
	if GetViewMode(120) != ModeDesktop {
		t.Fatalf("expected ModeDesktop for width 120")
	}
	m, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	app = m.(*AppModel)
	desktopView := app.View()
	if !strings.Contains(desktopView, "PAINEL OPERACIONAL") || !strings.Contains(desktopView, "NOC") {
		t.Fatalf("expected desktop split NOC view:\n%s", desktopView)
	}
}

func TestConfigSectionEditing(t *testing.T) {
	tempDir := t.TempDir()
	cfgPath := filepath.Join(tempDir, "dragontcp.yaml")
	client := NewAdminClient("http://127.0.0.1:53080", cfgPath, filepath.Join(tempDir, "users.json"))
	app := NewAppModel(client)

	// 1. Enter Configuration Menu ('5')
	m, _ := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
	app = m.(*AppModel)
	if app.state != StateConfigMenu {
		t.Fatalf("expected StateConfigMenu, got %v", app.state)
	}
	view := app.View()
	if !strings.Contains(view, "dragontcp.yaml") || !strings.Contains(view, "Rede & Portas") {
		t.Fatalf("config menu view mismatch:\n%s", view)
	}

	// 2. Open Section 1: Rede & Portas ('1')
	m, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	app = m.(*AppModel)
	if app.state != StateConfigSectionEdit {
		t.Fatalf("expected StateConfigSectionEdit, got %v", app.state)
	}
	if app.activeForm == nil {
		t.Fatal("expected activeForm to be non-nil")
	}
	if cmd == nil {
		t.Fatal("expected activeForm.Init() command to be returned")
	}

	// 3. Edit Port to 5353 and Save
	app.formConfigVars.PortStr = "5353"
	app.formConfirmed = true
	app.handleFormCompletion()

	if app.yamlConfig.Port != 5353 {
		t.Fatalf("expected port 5353, got %d", app.yamlConfig.Port)
	}

	// 4. Verify reloaded YAML on disk preserves all full keys
	reloaded, err := client.LoadConfigYAML()
	if err != nil {
		t.Fatalf("LoadConfigYAML: %v", err)
	}
	if reloaded.Port != 5353 {
		t.Fatalf("reloaded port mismatch: %d", reloaded.Port)
	}
	if !reloaded.SSH.Enable || reloaded.SSH.Listen != "127.0.0.1:2222" {
		t.Fatalf("SSH section mismatch: %+v", reloaded.SSH)
	}
	if !reloaded.UDPGW.Enable || reloaded.UDPGW.MaxClients != 10000 {
		t.Fatalf("UDPGW section mismatch: %+v", reloaded.UDPGW)
	}

	// 5. Test Section 6: BadVPN UDPGW Form with native/tun/standard mode
	app.state = StateConfigMenu
	m, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'6'}})
	app = m.(*AppModel)
	if app.state != StateConfigSectionEdit || app.activeForm == nil {
		t.Fatalf("expected StateConfigSectionEdit for UDPGW, got %v", app.state)
	}
	app.formConfigVars.UDPGWMode = "tun"
	app.formConfigVars.UDPGWInterface = "eth1"
	app.formConfigVars.UDPGWBusyPollUSStr = "100"
	app.formConfirmed = true
	app.handleFormCompletion()

	if app.yamlConfig.UDPGW.Mode != "tun" || app.yamlConfig.UDPGW.Interface != "eth1" || app.yamlConfig.UDPGW.BusyPollUS != 100 {
		t.Fatalf("expected UDPGW mode tun / eth1 / 100, got %+v", app.yamlConfig.UDPGW)
	}

	// 6. Test Parallel Workers calculation formula
	if w := calculateParallelWorkers(1048576); w != 1 {
		t.Fatalf("expected 1 worker for 1048576 chunk, got %d", w)
	}
	if w := calculateParallelWorkers(16384); w != 64 {
		t.Fatalf("expected 64 workers for 16384 chunk, got %d", w)
	}
	if w := calculateParallelWorkers(32768); w != 32 {
		t.Fatalf("expected 32 workers for 32768 chunk, got %d", w)
	}
}
