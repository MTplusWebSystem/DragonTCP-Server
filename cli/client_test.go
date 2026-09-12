package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestClientOfflineUserOperations(t *testing.T) {
	tempDir := t.TempDir()
	usersPath := filepath.Join(tempDir, "users.json")
	configPath := filepath.Join(tempDir, "dragontcp.yaml")

	client := NewAdminClient("http://127.0.0.1:53080", configPath, usersPath)

	// 1. Initially empty
	users, err := client.GetUsers()
	if err != nil {
		t.Fatalf("GetUsers on empty: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected 0 users, got %d", len(users))
	}

	// 2. Create user "alice"
	if err := client.CreateUser("alice", "pass123", 30, 2); err != nil {
		t.Fatalf("CreateUser alice: %v", err)
	}

	users, err = client.GetUsers()
	if err != nil || len(users) != 1 {
		t.Fatalf("expected 1 user, got %d (err=%v)", len(users), err)
	}
	if users[0].Username != "alice" || users[0].MaxConnections != 2 || users[0].Disabled {
		t.Fatalf("unexpected user alice: %+v", users[0])
	}

	// 3. Block user "alice"
	if err := client.BlockUser("alice", true); err != nil {
		t.Fatalf("BlockUser alice: %v", err)
	}
	users, _ = client.GetUsers()
	if !users[0].Disabled {
		t.Fatal("expected alice to be disabled")
	}

	// 4. Unblock user "alice"
	if err := client.BlockUser("alice", false); err != nil {
		t.Fatalf("UnblockUser alice: %v", err)
	}
	users, _ = client.GetUsers()
	if users[0].Disabled {
		t.Fatal("expected alice to be active")
	}

	// 5. Delete user "alice"
	if err := client.DeleteUser("alice"); err != nil {
		t.Fatalf("DeleteUser alice: %v", err)
	}
	users, _ = client.GetUsers()
	if len(users) != 0 {
		t.Fatal("expected alice to be deleted")
	}
}

func TestClientOfflineConfigOperations(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "dragontcp.yaml")
	usersPath := filepath.Join(tempDir, "users.json")

	client := NewAdminClient("http://127.0.0.1:53080", configPath, usersPath)

	// 1. Default config returned when file doesn't exist
	cfg, err := client.LoadConfigYAML()
	if err != nil {
		t.Fatalf("LoadConfigYAML default: %v", err)
	}
	if cfg.Port != 53 || cfg.MaxConnections != 20000 {
		t.Fatalf("unexpected default config: %+v", cfg)
	}

	// 2. Save modified config
	cfg.Port = 5353
	cfg.Token = "secret-token"
	if err := client.SaveConfigYAML(cfg); err != nil {
		t.Fatalf("SaveConfigYAML: %v", err)
	}

	// 3. Reload config and verify
	reloaded, err := client.LoadConfigYAML()
	if err != nil {
		t.Fatalf("LoadConfigYAML reloaded: %v", err)
	}
	if reloaded.Port != 5353 || reloaded.Token != "secret-token" {
		t.Fatalf("reloaded config mismatch: %+v", reloaded)
	}
}

func TestClientOnlineAPIOperations(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ServerStatus{
			Online:         true,
			PID:            1234,
			Uptime:         "1h 30m",
			Host:           "0.0.0.0",
			Port:           53,
			ActiveSessions: 5,
		})
	})
	mux.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(SystemMetrics{
			NumCPU:         8,
			NumGoroutine:   42,
			AllocBytes:     1024 * 1024,
			AllocFormatted: "1.00 MB",
		})
	})
	mux.HandleFunc("/api/connections", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]ConnectionItem{
			{
				SessionID:     "deadbeef000000000000000000000001",
				TargetName:    "example.com:443",
				BufferedBytes: 2048,
				LastSeen:      time.Now(),
			},
		})
	})
	mux.HandleFunc("/api/connections/kill", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
	})
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"logs": []string{"[DEBUG] connection started", "[CHUNK] received data"},
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := NewAdminClient(ts.URL, "dummy.yaml", "dummy.json")

	// Status
	status, err := client.GetStatus()
	if err != nil || !status.Online || status.PID != 1234 {
		t.Fatalf("GetStatus mismatch: %+v (err=%v)", status, err)
	}

	// Metrics
	metrics, err := client.GetMetrics()
	if err != nil || metrics.NumCPU != 8 || metrics.NumGoroutine != 42 {
		t.Fatalf("GetMetrics mismatch: %+v (err=%v)", metrics, err)
	}

	// Connections
	conns, err := client.GetConnections()
	if err != nil || len(conns) != 1 || conns[0].TargetName != "example.com:443" {
		t.Fatalf("GetConnections mismatch: %+v (err=%v)", conns, err)
	}

	// Kill
	if err := client.KillConnection("deadbeef000000000000000000000001"); err != nil {
		t.Fatalf("KillConnection: %v", err)
	}

	// Logs
	logs, err := client.GetLogs(10)
	if err != nil || len(logs) != 2 {
		t.Fatalf("GetLogs mismatch: %+v (err=%v)", logs, err)
	}
}

func TestGenerateSecurePassword(t *testing.T) {
	p1, err := GenerateSecurePassword()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := GenerateSecurePassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != 24 || len(p2) != 24 {
		t.Fatalf("password lengths = %d, %d; want 24", len(p1), len(p2))
	}
	if p1 == p2 {
		t.Fatal("passwords are not unique")
	}
}
