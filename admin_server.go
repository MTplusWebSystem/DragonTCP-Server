package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"dragontcp/internal/wire"
)

type ServerStatusResponse struct {
	Online         bool      `json:"online"`
	PID            int       `json:"pid"`
	Uptime         string    `json:"uptime"`
	StartedAt      time.Time `json:"started_at"`
	Host           string    `json:"host"`
	Port           int       `json:"port"`
	PortAlt        int       `json:"port_alt"`
	HasToken       bool      `json:"has_token"`
	MaxConnections int       `json:"max_connections"`
	ActiveTunnels  int64     `json:"active_tunnels"`
	ActiveSessions int64     `json:"active_sessions"`
	SessionsOpened uint64    `json:"sessions_opened"`
	SessionsClosed uint64    `json:"sessions_closed"`
	BytesUp        uint64    `json:"bytes_up"`
	BytesDown      uint64    `json:"bytes_down"`
	PushRecords    uint64    `json:"push_records"`
	PullRequests   uint64    `json:"pull_requests"`
	DataRecords    uint64    `json:"data_records"`
	WaitRecords    uint64    `json:"wait_records"`
	Errors         uint64    `json:"errors"`
	SSHEnabled     bool      `json:"ssh_enabled"`
	UDPGWEnabled   bool      `json:"udpgw_enabled"`
	UDPGWMode      string    `json:"udpgw_mode,omitempty"`
	UDPGWInterface string    `json:"udpgw_interface,omitempty"`
}

type SystemMetricsResponse struct {
	NumCPU         int    `json:"num_cpu"`
	NumGoroutine   int    `json:"num_goroutine"`
	AllocBytes     uint64 `json:"alloc_bytes"`
	TotalAlloc     uint64 `json:"total_alloc_bytes"`
	SysBytes       uint64 `json:"sys_bytes"`
	NumGC          uint32 `json:"num_gc"`
	AllocFormatted string `json:"alloc_formatted"`
	SysFormatted   string `json:"sys_formatted"`
}

type adminServer struct {
	addr      string
	server    *http.Server
	manager   *streamManager
	sshStore  *sshUserStore
	debug     *serverDebug
	cfgTarget serverFlagTargets
	started   time.Time
}

func formatMemoryBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func startAdminServer(addr string, manager *streamManager, sshStore *sshUserStore, debug *serverDebug, targets serverFlagTargets) (*adminServer, error) {
	if strings.TrimSpace(addr) == "" {
		return nil, nil
	}

	as := &adminServer{
		addr:      addr,
		manager:   manager,
		sshStore:  sshStore,
		debug:     debug,
		cfgTarget: targets,
		started:   time.Now(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", as.handleStatus)
	mux.HandleFunc("/api/metrics", as.handleMetrics)
	mux.HandleFunc("/api/connections", as.handleConnections)
	mux.HandleFunc("/api/connections/kill", as.handleKillConnection)
	mux.HandleFunc("/api/logs", as.handleLogs)
	mux.HandleFunc("/api/users", as.handleUsers)
	mux.HandleFunc("/api/users/block", as.handleBlockUser)
	mux.HandleFunc("/api/restart", as.handleRestart)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("admin server listen on %s: %w", addr, err)
	}
	as.addr = ln.Addr().String()

	as.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		_ = as.server.Serve(ln)
	}()

	return as, nil
}

func (as *adminServer) Close() error {
	if as == nil || as.server == nil {
		return nil
	}
	return as.server.Close()
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (as *adminServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	host := "0.0.0.0"
	if as.cfgTarget.host != nil {
		host = *as.cfgTarget.host
	}
	port := 53
	if as.cfgTarget.port != nil {
		port = *as.cfgTarget.port
	}
	portAlt := 80
	if as.cfgTarget.portAlt != nil {
		portAlt = *as.cfgTarget.portAlt
	}
	maxConns := 20000
	if as.cfgTarget.maxConnections != nil {
		maxConns = *as.cfgTarget.maxConnections
	}
	hasToken := false
	if as.cfgTarget.token != nil && *as.cfgTarget.token != "" {
		hasToken = true
	}
	sshEn := false
	if as.cfgTarget.sshEnable != nil {
		sshEn = *as.cfgTarget.sshEnable
	}
	udpEn := false
	if as.cfgTarget.udpgwEnable != nil {
		udpEn = *as.cfgTarget.udpgwEnable
	}

	var sessOpened, sessClosed, bytesUp, bytesDown, pushRec, pullReq, dataRec, waitRec, errorsCount uint64
	activeSess := int64(0)
	if as.manager != nil {
		activeSess = int64(as.manager.count())
	}
	if as.debug != nil {
		sessOpened = as.debug.sessionsOpened.Load()
		sessClosed = as.debug.sessionsClosed.Load()
		bytesUp = as.debug.bytesUp.Load()
		bytesDown = as.debug.bytesDown.Load()
		pushRec = as.debug.pushRecords.Load()
		pullReq = as.debug.pullRequests.Load()
		dataRec = as.debug.dataRecords.Load()
		waitRec = as.debug.waitRecords.Load()
		errorsCount = as.debug.errors.Load()
		activeSess = as.debug.activeSessions.Load()
	}

	resp := ServerStatusResponse{
		Online:         true,
		PID:            os.Getpid(),
		Uptime:         time.Since(as.started).Round(time.Second).String(),
		StartedAt:      as.started,
		Host:           host,
		Port:           port,
		PortAlt:        portAlt,
		HasToken:       hasToken,
		MaxConnections: maxConns,
		ActiveTunnels:  atomic.LoadInt64(&active),
		ActiveSessions: activeSess,
		SessionsOpened: sessOpened,
		SessionsClosed: sessClosed,
		BytesUp:        bytesUp,
		BytesDown:      bytesDown,
		PushRecords:    pushRec,
		PullRequests:   pullReq,
		DataRecords:    dataRec,
		WaitRecords:    waitRec,
		Errors:         errorsCount,
		SSHEnabled:     sshEn,
		UDPGWEnabled:   udpEn,
		UDPGWMode: func() string {
			if as.cfgTarget.udpgwMode != nil && *as.cfgTarget.udpgwMode != "" {
				return *as.cfgTarget.udpgwMode
			}
			return "native"
		}(),
		UDPGWInterface: func() string {
			if as.cfgTarget.udpgwInterface != nil && *as.cfgTarget.udpgwInterface != "" {
				return *as.cfgTarget.udpgwInterface
			}
			return "auto"
		}(),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (as *adminServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	resp := SystemMetricsResponse{
		NumCPU:         runtime.NumCPU(),
		NumGoroutine:   runtime.NumGoroutine(),
		AllocBytes:     m.Alloc,
		TotalAlloc:     m.TotalAlloc,
		SysBytes:       m.Sys,
		NumGC:          m.NumGC,
		AllocFormatted: formatMemoryBytes(m.Alloc),
		SysFormatted:   formatMemoryBytes(m.Sys),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (as *adminServer) handleConnections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	conns := []ConnectionInfo{}
	if as.manager != nil {
		conns = as.manager.listConnections()
	}
	writeJSON(w, http.StatusOK, conns)
}

func (as *adminServer) handleKillConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sidStr := strings.TrimSpace(r.URL.Query().Get("sid"))
	if sidStr == "" {
		http.Error(w, "missing sid parameter", http.StatusBadRequest)
		return
	}

	if as.manager == nil {
		writeJSON(w, http.StatusOK, map[string]string{"result": "no manager"})
		return
	}

	if sidStr == "all" {
		conns := as.manager.listConnections()
		count := 0
		for _, c := range conns {
			var sid wire.SessionID
			if raw, err := hex.DecodeString(c.SessionID); err == nil && len(raw) == 16 {
				copy(sid[:], raw)
				as.manager.remove(sid)
				count++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": "ok", "killed": count})
		return
	}

	raw, err := hex.DecodeString(sidStr)
	if err != nil || len(raw) != 16 {
		http.Error(w, "invalid session id hex", http.StatusBadRequest)
		return
	}
	var sid wire.SessionID
	copy(sid[:], raw)
	as.manager.remove(sid)

	writeJSON(w, http.StatusOK, map[string]string{"result": "ok", "killed": sidStr})
}

func (as *adminServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	n := 100
	if nStr := r.URL.Query().Get("n"); nStr != "" {
		if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 {
			n = parsed
		}
	}

	lines := globalLogRing.get(n)
	writeJSON(w, http.StatusOK, map[string]any{"logs": lines})
}

func (as *adminServer) handleUsers(w http.ResponseWriter, r *http.Request) {
	if as.sshStore == nil {
		http.Error(w, "SSH user store not configured", http.StatusServiceUnavailable)
		return
	}

	switch r.Method {
	case http.MethodGet:
		records, err := as.sshStore.snapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, records)

	case http.MethodPost:
		var req struct {
			Username       string `json:"username"`
			Password       string `json:"password"`
			Days           int    `json:"days"`
			MaxConnections int    `json:"max_connections"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := as.sshStore.upsert(req.Username, req.Password, req.Days, req.MaxConnections); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		u, _ := as.sshStore.get(req.Username)
		writeJSON(w, http.StatusOK, u)

	case http.MethodDelete:
		username := strings.TrimSpace(r.URL.Query().Get("username"))
		if username == "" {
			http.Error(w, "missing username parameter", http.StatusBadRequest)
			return
		}
		if err := as.sshStore.delete(username); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"result": "deleted", "username": username})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (as *adminServer) handleBlockUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if as.sshStore == nil {
		http.Error(w, "SSH user store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Username string `json:"username"`
		Disabled bool   `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if err := as.sshStore.setDisabled(req.Username, req.Disabled); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": req.Username, "disabled": req.Disabled})
}

func (as *adminServer) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
	go func() {
		time.Sleep(500 * time.Millisecond)
		os.Exit(0)
	}()
}
