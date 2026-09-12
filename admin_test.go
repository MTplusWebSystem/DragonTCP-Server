package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"dragontcp/internal/wire"
)

func TestAdminServerEndpoints(t *testing.T) {
	manager := newStreamManager(time.Minute, nil)
	usersPath := filepath.Join(t.TempDir(), "users.json")
	sshStore := newSSHUserStore(usersPath)
	debug := newServerDebug(true, false, 0)
	debug.logf("server initialized for test")

	var host = "127.0.0.1"
	var port = 5353
	var portAlt = 8080
	var token = "admin-secret"
	var maxConns = 1000
	var sshEn = true
	var udpgwEn = false

	targets := serverFlagTargets{
		host:           &host,
		port:           &port,
		portAlt:        &portAlt,
		token:          &token,
		maxConnections: &maxConns,
		sshEnable:      &sshEn,
		udpgwEnable:    &udpgwEn,
	}

	admin, err := startAdminServer("127.0.0.1:0", manager, sshStore, debug, targets)
	if err != nil {
		t.Fatalf("startAdminServer: %v", err)
	}
	defer admin.Close()

	baseURL := "http://" + admin.addr

	// 1. GET /api/status
	resp, err := http.Get(baseURL + "/api/status")
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/status status: %d", resp.StatusCode)
	}
	var status ServerStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Online || status.Host != "127.0.0.1" || status.Port != 5353 || !status.HasToken {
		t.Fatalf("unexpected status: %+v", status)
	}

	// 2. GET /api/metrics
	resp, err = http.Get(baseURL + "/api/metrics")
	if err != nil {
		t.Fatalf("GET /api/metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/metrics status: %d", resp.StatusCode)
	}
	var metrics SystemMetricsResponse
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if metrics.NumCPU <= 0 || metrics.AllocBytes == 0 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}

	// 3. GET /api/logs
	resp, err = http.Get(baseURL + "/api/logs?n=10")
	if err != nil {
		t.Fatalf("GET /api/logs: %v", err)
	}
	defer resp.Body.Close()
	var logsResp struct {
		Logs []string `json:"logs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&logsResp); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if len(logsResp.Logs) == 0 {
		t.Fatal("expected at least 1 log entry in logs response")
	}

	// 4. POST /api/users (Create user)
	newUserReq := map[string]any{
		"username":        "bob",
		"password":        "secret123",
		"days":            15,
		"max_connections": 3,
	}
	bodyData, _ := json.Marshal(newUserReq)
	resp, err = http.Post(baseURL+"/api/users", "application/json", bytes.NewReader(bodyData))
	if err != nil {
		t.Fatalf("POST /api/users: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/users status: %d", resp.StatusCode)
	}

	// 5. GET /api/users (List users)
	resp, err = http.Get(baseURL + "/api/users")
	if err != nil {
		t.Fatalf("GET /api/users: %v", err)
	}
	defer resp.Body.Close()
	var users []sshUserRecord
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	if len(users) != 1 || users[0].Username != "bob" || users[0].Disabled {
		t.Fatalf("unexpected user list: %+v", users)
	}

	// 6. POST /api/users/block (Disable user)
	blockReq := map[string]any{
		"username": "bob",
		"disabled": true,
	}
	bodyData, _ = json.Marshal(blockReq)
	resp, err = http.Post(baseURL+"/api/users/block", "application/json", bytes.NewReader(bodyData))
	if err != nil {
		t.Fatalf("POST /api/users/block: %v", err)
	}
	defer resp.Body.Close()
	u, exists := sshStore.get("bob")
	if !exists || !u.Disabled {
		t.Fatalf("expected user bob to be disabled: %+v", u)
	}

	// 7. DELETE /api/users (Delete user)
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/users?username=bob", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/users: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/users status: %d", resp.StatusCode)
	}
	if _, exists := sshStore.get("bob"); exists {
		t.Fatal("expected user bob to be deleted")
	}

	// 8. GET /api/connections
	resp, err = http.Get(baseURL + "/api/connections")
	if err != nil {
		t.Fatalf("GET /api/connections: %v", err)
	}
	defer resp.Body.Close()
	var conns []ConnectionInfo
	if err := json.NewDecoder(resp.Body).Decode(&conns); err != nil {
		t.Fatalf("decode connections: %v", err)
	}
	if len(conns) != 0 {
		t.Fatalf("expected 0 connections initially, got %d", len(conns))
	}

	// 9. Kill connection test
	var dummySID wire.SessionID
	dummySID[0] = 0x11
	dummySID[15] = 0x99
	req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/connections/kill?sid=11000000000000000000000000000099", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/connections/kill: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("kill status: %d", resp.StatusCode)
	}
	killBody, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(killBody, []byte("ok")) {
		t.Fatalf("unexpected kill body: %s", killBody)
	}
}
