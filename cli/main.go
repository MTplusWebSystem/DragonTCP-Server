package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	adminAddr := flag.String("admin-addr", "http://127.0.0.1:53080", "DragonTCP admin API URL")
	configPath := flag.String("config", "dragontcp.yaml", "DragonTCP YAML configuration file path")
	usersPath := flag.String("users", "dragontcp-users.json", "DragonTCP fake SSH user database JSON path")
	flag.Parse()

	client := NewAdminClient(*adminAddr, *configPath, *usersPath)
	app := NewAppModel(client)

	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Erro ao executar DragonTCP TUI: %v\n", err)
		os.Exit(1)
	}
}
