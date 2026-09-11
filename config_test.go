package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Host != "0.0.0.0" {
		t.Fatalf("expected host 0.0.0.0, got %s", cfg.Host)
	}
	if cfg.Port != 53 {
		t.Fatalf("expected port 53, got %d", cfg.Port)
	}
	if cfg.PortAlt != 80 {
		t.Fatalf("expected port-alt 80, got %d", cfg.PortAlt)
	}
	if cfg.MaxConnections != 20000 {
		t.Fatalf("expected max connections 20000, got %d", cfg.MaxConnections)
	}
	if !cfg.SSH.Enable {
		t.Fatalf("expected SSH enabled by default")
	}
	if cfg.SSH.Listen != "127.0.0.1:2222" {
		t.Fatalf("expected SSH listen 127.0.0.1:2222, got %s", cfg.SSH.Listen)
	}
	if !cfg.UDPGW.Enable {
		t.Fatalf("expected UDPGW enabled by default")
	}
	if cfg.UDPGW.Listen != "127.0.0.1:7400" {
		t.Fatalf("expected UDPGW listen 127.0.0.1:7400, got %s", cfg.UDPGW.Listen)
	}
}

func TestLoadConfigBytes_FullSnake(t *testing.T) {
	yamlData := `
host: "10.0.0.1"
port: 443
port_alt: 8443
token: "my-secret-token"
max_connections: 5000
allow_private: true
dns_cache_ttl: 45s
dns_cache_size: 2048
tcp_buffer: 131072
chunk_max: 524288
chunk_buffered: 16
chunk_poll_wait: 150ms
chunk_session_timeout: 3m
debug: true
debug_chunks: true
debug_stats_interval: 10s

ssh:
  enable: true
  listen: "127.0.0.1:22222"
  internal_host: "custom-ssh.internal"
  host_key: "/etc/ssh/key"
  users: "/etc/ssh/users.json"

udpgw:
  enable: true
  listen: "127.0.0.1:7401"
  internal_host: "custom-udpgw.internal"
  max_clients: 500
  debug: true
`
	cfg, err := LoadConfigBytes([]byte(yamlData))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.Host != "10.0.0.1" || cfg.Port != 443 || cfg.PortAlt != 8443 {
		t.Fatalf("unexpected host/port: %+v", cfg)
	}
	if cfg.Token != "my-secret-token" {
		t.Fatalf("unexpected token: %s", cfg.Token)
	}
	if cfg.MaxConnections != 5000 || !cfg.AllowPrivate {
		t.Fatalf("unexpected max connections or allow private: %+v", cfg)
	}
	if cfg.DNSCacheTTL != 45*time.Second || cfg.DNSCacheSize != 2048 {
		t.Fatalf("unexpected dns settings: %+v", cfg)
	}
	if cfg.TCPBuffer != 131072 || cfg.ChunkMax != 524288 || cfg.ChunkBuffered != 16 {
		t.Fatalf("unexpected chunk settings: %+v", cfg)
	}
	if cfg.ChunkPollWait != 150*time.Millisecond || cfg.SessionTimeout != 3*time.Minute {
		t.Fatalf("unexpected timing settings: %+v", cfg)
	}
	if !cfg.Debug || !cfg.DebugChunks || cfg.DebugStats != 10*time.Second {
		t.Fatalf("unexpected debug settings: %+v", cfg)
	}
	if cfg.SSH.Listen != "127.0.0.1:22222" || cfg.SSH.InternalHost != "custom-ssh.internal" {
		t.Fatalf("unexpected ssh settings: %+v", cfg.SSH)
	}
	if cfg.UDPGW.Listen != "127.0.0.1:7401" || cfg.UDPGW.MaxClients != 500 || !cfg.UDPGW.Debug {
		t.Fatalf("unexpected udpgw settings: %+v", cfg.UDPGW)
	}
}

func TestLoadConfigBytes_KebabAndFlat(t *testing.T) {
	yamlData := `
port-alt: 9000
max-connections: 1234
allow-private: true
dns-cache-ttl: 1m
dns-cache-size: 512
tcp-buffer: 32768
chunk-max: 65536
chunk-buffered: 8
chunk-poll-wait: 50ms
chunk-session-timeout: 1m
debug-chunks: true
debug-stats-interval: 2s

ssh-enable: false
ssh-listen: "0.0.0.0:2200"
ssh-internal-host: "remote-ssh.internal"
ssh-host-key: "my_key"
ssh-users: "custom_users.json"

udpgw-enable: false
udpgw-listen: "0.0.0.0:7450"
udpgw-internal-host: "remote-udpgw.internal"
udpgw-max-clients: 250
udpgw-debug: true
`
	cfg, err := LoadConfigBytes([]byte(yamlData))
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.PortAlt != 9000 || cfg.MaxConnections != 1234 || !cfg.AllowPrivate {
		t.Fatalf("unexpected values: %+v", cfg)
	}
	if cfg.DNSCacheTTL != time.Minute || cfg.DNSCacheSize != 512 {
		t.Fatalf("unexpected dns: %+v", cfg)
	}
	if cfg.TCPBuffer != 32768 || cfg.ChunkMax != 65536 || cfg.ChunkBuffered != 8 {
		t.Fatalf("unexpected buffer: %+v", cfg)
	}
	if cfg.SSH.Enable || cfg.SSH.Listen != "0.0.0.0:2200" || cfg.SSH.InternalHost != "remote-ssh.internal" {
		t.Fatalf("unexpected ssh flat: %+v", cfg.SSH)
	}
	if cfg.SSH.Users != "custom_users.json" || cfg.SSH.HostKey != "my_key" {
		t.Fatalf("unexpected ssh users/key: %+v", cfg.SSH)
	}
	if cfg.UDPGW.Enable || cfg.UDPGW.Listen != "0.0.0.0:7450" || cfg.UDPGW.MaxClients != 250 || !cfg.UDPGW.Debug {
		t.Fatalf("unexpected udpgw flat: %+v", cfg.UDPGW)
	}
}

func TestLoadConfigBytes_PartialPreservesDefaults(t *testing.T) {
	yamlData := `
port: 8080
token: "my-token"
`
	cfg, err := LoadConfigBytes([]byte(yamlData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Port != 8080 {
		t.Fatalf("expected port 8080, got %d", cfg.Port)
	}
	if cfg.Token != "my-token" {
		t.Fatalf("expected token my-token, got %s", cfg.Token)
	}
	// Defaults should be preserved
	if cfg.Host != "0.0.0.0" {
		t.Fatalf("expected default host 0.0.0.0, got %s", cfg.Host)
	}
	if cfg.PortAlt != 80 {
		t.Fatalf("expected default port-alt 80, got %d", cfg.PortAlt)
	}
	if cfg.MaxConnections != 20000 {
		t.Fatalf("expected default max connections 20000, got %d", cfg.MaxConnections)
	}
	if !cfg.SSH.Enable {
		t.Fatalf("expected default SSH enabled")
	}
}

func TestApplyConfig_CLIPrecedence(t *testing.T) {
	cfg := &ServerConfig{
		Host:           "127.0.0.1",
		Port:           8080,
		PortAlt:        8443,
		Token:          "yaml-token",
		MaxConnections: 1000,
	}

	// Flag pointers initially set to defaults
	host := "0.0.0.0"
	port := 9999 // Simulated CLI flag passed --port 9999
	portAlt := 80
	token := ""
	maxConnections := 20000

	targets := serverFlagTargets{
		host:           &host,
		port:           &port,
		portAlt:        &portAlt,
		token:          &token,
		maxConnections: &maxConnections,
	}

	// Suppose "port" was visited on the CLI
	visited := map[string]bool{
		"port": true,
	}

	applyConfig(cfg, visited, targets)

	// Port should retain CLI value 9999
	if port != 9999 {
		t.Fatalf("expected CLI flag to prevail with 9999, got %d", port)
	}
	// Host should be updated from YAML
	if host != "127.0.0.1" {
		t.Fatalf("expected host from YAML 127.0.0.1, got %s", host)
	}
	// PortAlt should be updated from YAML
	if portAlt != 8443 {
		t.Fatalf("expected portAlt from YAML 8443, got %d", portAlt)
	}
	// Token should be updated from YAML
	if token != "yaml-token" {
		t.Fatalf("expected token from YAML yaml-token, got %s", token)
	}
	// MaxConnections should be updated from YAML
	if maxConnections != 1000 {
		t.Fatalf("expected maxConnections from YAML 1000, got %d", maxConnections)
	}
}

func TestLoadConfigFile_Disk(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "server.yaml")
	content := []byte("port: 5050\ntoken: secret-on-disk\n")
	if err := os.WriteFile(configPath, content, 0600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	cfg, err := LoadConfigFile(configPath)
	if err != nil {
		t.Fatalf("failed to load config from disk: %v", err)
	}
	if cfg.Port != 5050 || cfg.Token != "secret-on-disk" {
		t.Fatalf("unexpected values loaded from disk: %+v", cfg)
	}

	// Missing file should return error
	_, err = LoadConfigFile(filepath.Join(dir, "nonexistent.yaml"))
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}

	// Invalid YAML should return error
	invalidPath := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(invalidPath, []byte("port: [invalid"), 0600); err != nil {
		t.Fatalf("failed to write invalid file: %v", err)
	}
	_, err = LoadConfigFile(invalidPath)
	if err == nil {
		t.Fatalf("expected error for invalid YAML, got nil")
	}
}
