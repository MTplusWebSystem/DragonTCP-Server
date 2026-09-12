package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// Shared API & Client Model Structures

type ServerStatus struct {
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

type SystemMetrics struct {
	NumCPU         int    `json:"num_cpu"`
	NumGoroutine   int    `json:"num_goroutine"`
	AllocBytes     uint64 `json:"alloc_bytes"`
	TotalAlloc     uint64 `json:"total_alloc_bytes"`
	SysBytes       uint64 `json:"sys_bytes"`
	NumGC          uint32 `json:"num_gc"`
	AllocFormatted string `json:"alloc_formatted"`
	SysFormatted   string `json:"sys_formatted"`
}

type ConnectionItem struct {
	SessionID     string    `json:"session_id"`
	TargetName    string    `json:"target_name"`
	BaseOffset    uint64    `json:"base_offset"`
	BufferedBytes int       `json:"buffered_bytes"`
	LastSeen      time.Time `json:"last_seen"`
	Closed        bool      `json:"closed"`
	EOF           bool      `json:"eof"`
	AgeSeconds    int64     `json:"age_seconds"`
}

type UserItem struct {
	Username       string    `json:"username"`
	PasswordHash   string    `json:"password_hash,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	MaxConnections int       `json:"max_connections,omitempty"`
	Disabled       bool      `json:"disabled,omitempty"`
}

type UserStoreFile struct {
	Version int        `json:"version"`
	Users   []UserItem `json:"users"`
}

// AdminClient communicates with the DragonTCP HTTP administration server,
// with graceful local file fallback for offline user & config management.
type AdminClient struct {
	BaseURL    string
	ConfigPath string
	UsersPath  string
	httpClient *http.Client
}

func NewAdminClient(baseURL, configPath, usersPath string) *AdminClient {
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &AdminClient{
		BaseURL:    baseURL,
		ConfigPath: configPath,
		UsersPath:  usersPath,
		httpClient: &http.Client{Timeout: 3 * time.Second},
	}
}

func (c *AdminClient) GetStatus() (ServerStatus, error) {
	resp, err := c.httpClient.Get(c.BaseURL + "/api/status")
	if err != nil {
		return ServerStatus{Online: false}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ServerStatus{Online: false}, fmt.Errorf("status HTTP %d", resp.StatusCode)
	}
	var status ServerStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return ServerStatus{Online: false}, err
	}
	status.Online = true
	return status, nil
}

func (c *AdminClient) GetMetrics() (SystemMetrics, error) {
	resp, err := c.httpClient.Get(c.BaseURL + "/api/metrics")
	if err != nil {
		// Fallback to local process stats
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return SystemMetrics{
			NumCPU:         runtime.NumCPU(),
			NumGoroutine:   runtime.NumGoroutine(),
			AllocBytes:     m.Alloc,
			TotalAlloc:     m.TotalAlloc,
			SysBytes:       m.Sys,
			NumGC:          m.NumGC,
			AllocFormatted: formatBytesHuman(m.Alloc),
			SysFormatted:   formatBytesHuman(m.Sys),
		}, nil
	}
	defer resp.Body.Close()
	var metrics SystemMetrics
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		return SystemMetrics{}, err
	}
	return metrics, nil
}

func (c *AdminClient) GetConnections() ([]ConnectionItem, error) {
	resp, err := c.httpClient.Get(c.BaseURL + "/api/connections")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var conns []ConnectionItem
	if err := json.NewDecoder(resp.Body).Decode(&conns); err != nil {
		return nil, err
	}
	return conns, nil
}

func (c *AdminClient) KillConnection(sid string) error {
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/api/connections/kill?sid="+sid, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("kill failed: %s", string(body))
	}
	return nil
}

func (c *AdminClient) GetLogs(n int) ([]string, error) {
	resp, err := c.httpClient.Get(fmt.Sprintf("%s/api/logs?n=%d", c.BaseURL, n))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var res struct {
		Logs []string `json:"logs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}
	return res.Logs, nil
}

func (c *AdminClient) RestartServer() error {
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+"/api/restart", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// User Management Methods (API first, File Fallback second)

func (c *AdminClient) GetUsers() ([]UserItem, error) {
	// Try online API first
	resp, err := c.httpClient.Get(c.BaseURL + "/api/users")
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var users []UserItem
		if err := json.NewDecoder(resp.Body).Decode(&users); err == nil {
			return users, nil
		}
	}
	// Fallback to reading file directly
	return c.loadUsersFile()
}

func (c *AdminClient) CreateUser(username, password string, days, maxConns int) error {
	// Try online API first
	payload := map[string]any{
		"username":        username,
		"password":        password,
		"days":            days,
		"max_connections": maxConns,
	}
	bodyData, _ := json.Marshal(payload)
	resp, err := c.httpClient.Post(c.BaseURL+"/api/users", "application/json", bytes.NewReader(bodyData))
	if err == nil && resp.StatusCode == http.StatusOK {
		_ = resp.Body.Close()
		return nil
	}

	// Offline file fallback
	return c.createUserFile(username, password, days, maxConns)
}

func (c *AdminClient) BlockUser(username string, disabled bool) error {
	// Try online API first
	payload := map[string]any{
		"username": username,
		"disabled": disabled,
	}
	bodyData, _ := json.Marshal(payload)
	resp, err := c.httpClient.Post(c.BaseURL+"/api/users/block", "application/json", bytes.NewReader(bodyData))
	if err == nil && resp.StatusCode == http.StatusOK {
		_ = resp.Body.Close()
		return nil
	}

	// Offline file fallback
	return c.blockUserFile(username, disabled)
}

func (c *AdminClient) DeleteUser(username string) error {
	req, err := http.NewRequest(http.MethodDelete, c.BaseURL+"/api/users?username="+username, nil)
	if err == nil {
		resp, reqErr := c.httpClient.Do(req)
		if reqErr == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return nil
		}
	}

	// Offline file fallback
	return c.deleteUserFile(username)
}

// Local File Operations for Users (Offline mode)

func (c *AdminClient) loadUsersFile() ([]UserItem, error) {
	data, err := os.ReadFile(c.UsersPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []UserItem{}, nil
		}
		return nil, err
	}
	var f UserStoreFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	sort.Slice(f.Users, func(i, j int) bool { return f.Users[i].Username < f.Users[j].Username })
	return f.Users, nil
}

func (c *AdminClient) saveUsersFile(users []UserItem) error {
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	f := UserStoreFile{Version: 1, Users: users}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(c.UsersPath)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0700)
	}
	return os.WriteFile(c.UsersPath, data, 0600)
}

func (c *AdminClient) createUserFile(username, password string, days, maxConns int) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("nome de usuário é obrigatório")
	}
	if password == "" {
		return errors.New("senha é obrigatória")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	users, err := c.loadUsersFile()
	if err != nil {
		return err
	}
	var expires time.Time
	if days > 0 {
		expires = time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
	}
	found := false
	for i := range users {
		if users[i].Username == username {
			users[i].PasswordHash = string(hash)
			users[i].ExpiresAt = expires
			users[i].MaxConnections = maxConns
			users[i].Disabled = false
			found = true
			break
		}
	}
	if !found {
		users = append(users, UserItem{
			Username:       username,
			PasswordHash:   string(hash),
			ExpiresAt:      expires,
			MaxConnections: maxConns,
			Disabled:       false,
		})
	}
	return c.saveUsersFile(users)
}

func (c *AdminClient) blockUserFile(username string, disabled bool) error {
	users, err := c.loadUsersFile()
	if err != nil {
		return err
	}
	found := false
	for i := range users {
		if users[i].Username == username {
			users[i].Disabled = disabled
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("usuário %q não encontrado", username)
	}
	return c.saveUsersFile(users)
}

func (c *AdminClient) deleteUserFile(username string) error {
	users, err := c.loadUsersFile()
	if err != nil {
		return err
	}
	out := make([]UserItem, 0, len(users))
	found := false
	for _, u := range users {
		if u.Username == username {
			found = true
			continue
		}
		out = append(out, u)
	}
	if !found {
		return fmt.Errorf("usuário %q não encontrado", username)
	}
	return c.saveUsersFile(out)
}

func GenerateSecurePassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func formatBytesHuman(b uint64) string {
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

// Config File Model for YAML viewing and editing

type YAMLSSHConfig struct {
	Enable       bool   `yaml:"enable"`
	Listen       string `yaml:"listen"`
	InternalHost string `yaml:"internal_host"`
	HostKey      string `yaml:"host_key"`
	Users        string `yaml:"users"`
}

type YAMLUDPGWConfig struct {
	Enable       bool   `yaml:"enable"`
	Listen       string `yaml:"listen"`
	InternalHost string `yaml:"internal_host"`
	MaxClients   int    `yaml:"max_clients"`
	Mode         string `yaml:"mode"`
	Interface    string `yaml:"interface"`
	BusyPollUS   int    `yaml:"busy_poll_us"`
	Debug        bool   `yaml:"debug"`
}

type YAMLConfig struct {
	Host               string          `yaml:"host"`
	Port               int             `yaml:"port"`
	PortAlt            int             `yaml:"port_alt"`
	Token              string          `yaml:"token"`
	MaxConnections     int             `yaml:"max_connections"`
	AllowPrivate       bool            `yaml:"allow_private"`
	DNSCacheTTL        string          `yaml:"dns_cache_ttl"`
	DNSCacheSize       int             `yaml:"dns_cache_size"`
	TCPBuffer          int             `yaml:"tcp_buffer"`
	ChunkMax           int             `yaml:"chunk_max"`
	ChunkBuffered      int             `yaml:"chunk_buffered"`
	ChunkPollWait      string          `yaml:"chunk_poll_wait"`
	ChunkSessionTime   string          `yaml:"chunk_session_timeout"`
	Debug              bool            `yaml:"debug"`
	DebugChunks        bool            `yaml:"debug_chunks"`
	DebugStatsInterval string          `yaml:"debug_stats_interval"`
	AdminAddr          string          `yaml:"admin_addr"`
	SSH                YAMLSSHConfig   `yaml:"ssh"`
	UDPGW              YAMLUDPGWConfig `yaml:"udpgw"`
}

func DefaultYAMLConfig() *YAMLConfig {
	return &YAMLConfig{
		Host:               "0.0.0.0",
		Port:               53,
		PortAlt:            80,
		Token:              "",
		MaxConnections:     20000,
		AllowPrivate:       false,
		DNSCacheTTL:        "30s",
		DNSCacheSize:       4096,
		TCPBuffer:          0,
		ChunkMax:           1048576,
		ChunkBuffered:      32,
		ChunkPollWait:      "200ms",
		ChunkSessionTime:   "2m",
		Debug:              false,
		DebugChunks:        false,
		DebugStatsInterval: "5s",
		AdminAddr:          "127.0.0.1:53080",
		SSH: YAMLSSHConfig{
			Enable:       true,
			Listen:       "127.0.0.1:2222",
			InternalHost: "dragontcp-ssh.internal",
			HostKey:      "dragontcp_ssh_host_key",
			Users:        "dragontcp-users.json",
		},
		UDPGW: YAMLUDPGWConfig{
			Enable:       true,
			Listen:       "127.0.0.1:7400",
			InternalHost: "dragontcp-udpgw.internal",
			MaxClients:   10000,
			Mode:         "native",
			Interface:    "auto",
			BusyPollUS:   50,
			Debug:        false,
		},
	}
}

func (c *AdminClient) LoadConfigYAML() (*YAMLConfig, error) {
	data, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultYAMLConfig(), nil
		}
		return nil, err
	}
	cfg := DefaultYAMLConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *AdminClient) SaveConfigYAML(cfg *YAMLConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	dir := filepath.Dir(c.ConfigPath)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}
	return os.WriteFile(c.ConfigPath, data, 0644)
}
