package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dragontcp/internal/protocol"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
)

const (
	defaultSSHInternalHost = "dragontcp-ssh.internal"
	defaultSSHListen       = "127.0.0.1:2222"
)

type sshUserRecord struct {
	Username       string    `json:"username"`
	PasswordHash   string    `json:"password_hash"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
	MaxConnections int       `json:"max_connections,omitempty"`
	Disabled       bool      `json:"disabled,omitempty"`
}

type sshUserFile struct {
	Version int             `json:"version"`
	Users   []sshUserRecord `json:"users"`
}

type sshUserStore struct {
	path    string
	mu      sync.RWMutex
	users   map[string]sshUserRecord
	modTime time.Time
}

func newSSHUserStore(path string) *sshUserStore {
	return &sshUserStore{path: path, users: make(map[string]sshUserRecord)}
}

func normalizeSSHUsername(v string) string {
	return strings.TrimSpace(v)
}

func validateSSHUsername(v string) error {
	v = normalizeSSHUsername(v)
	if v == "" {
		return errors.New("SSH username is required")
	}
	if len(v) > 64 {
		return errors.New("SSH username is too long")
	}
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			continue
		}
		return fmt.Errorf("SSH username contains unsupported character %q", r)
	}
	return nil
}

func (s *sshUserStore) loadLocked() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.users = make(map[string]sshUserRecord)
			s.modTime = time.Time{}
			return nil
		}
		return err
	}
	var file sshUserFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("parse %s: %w", s.path, err)
	}
	users := make(map[string]sshUserRecord, len(file.Users))
	for _, u := range file.Users {
		u.Username = normalizeSSHUsername(u.Username)
		if u.Username == "" || u.PasswordHash == "" {
			continue
		}
		users[u.Username] = u
	}
	s.users = users
	if st, err := os.Stat(s.path); err == nil {
		s.modTime = st.ModTime()
	}
	return nil
}

func (s *sshUserStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *sshUserStore) reloadIfChanged() error {
	st, err := os.Stat(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.mu.RLock()
			alreadyEmpty := len(s.users) == 0 && s.modTime.IsZero()
			s.mu.RUnlock()
			if alreadyEmpty {
				return nil
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			s.users = make(map[string]sshUserRecord)
			s.modTime = time.Time{}
			return nil
		}
		return err
	}
	s.mu.RLock()
	unchanged := st.ModTime().Equal(s.modTime)
	s.mu.RUnlock()
	if unchanged {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if st2, err := os.Stat(s.path); err == nil && st2.ModTime().Equal(s.modTime) {
		return nil
	}
	return s.loadLocked()
}

func (s *sshUserStore) snapshot() ([]sshUserRecord, error) {
	if err := s.reloadIfChanged(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	out := make([]sshUserRecord, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, u)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
}

func (s *sshUserStore) get(username string) (sshUserRecord, bool) {
	_ = s.reloadIfChanged()
	s.mu.RLock()
	u, ok := s.users[normalizeSSHUsername(username)]
	s.mu.RUnlock()
	return u, ok
}

func (s *sshUserStore) authenticate(username string, password []byte) (sshUserRecord, error) {
	if err := s.reloadIfChanged(); err != nil {
		return sshUserRecord{}, err
	}
	s.mu.RLock()
	u, ok := s.users[normalizeSSHUsername(username)]
	s.mu.RUnlock()
	if !ok || u.Disabled {
		return sshUserRecord{}, errors.New("authentication failed")
	}
	if !u.ExpiresAt.IsZero() && time.Now().After(u.ExpiresAt) {
		return sshUserRecord{}, errors.New("account expired")
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), password) != nil {
		return sshUserRecord{}, errors.New("authentication failed")
	}
	return u, nil
}

func (s *sshUserStore) writeRecords(records []sshUserRecord) error {
	sort.Slice(records, func(i, j int) bool { return records[i].Username < records[j].Username })
	file := sshUserFile{Version: 1, Users: records}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return s.Load()
}

func (s *sshUserStore) upsert(username, password string, days, maxConnections int) error {
	if err := validateSSHUsername(username); err != nil {
		return err
	}
	if password == "" {
		return errors.New("SSH password is required")
	}
	if days < 0 {
		return errors.New("account lifetime days cannot be negative")
	}
	if maxConnections < 0 {
		return errors.New("max connections cannot be negative")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	var expires time.Time
	if days > 0 {
		expires = time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
	}
	records, err := s.snapshot()
	if err != nil {
		return err
	}
	updated := false
	for i := range records {
		if records[i].Username == normalizeSSHUsername(username) {
			records[i].PasswordHash = string(hash)
			records[i].ExpiresAt = expires
			records[i].MaxConnections = maxConnections
			records[i].Disabled = false
			updated = true
			break
		}
	}
	if !updated {
		records = append(records, sshUserRecord{
			Username:       normalizeSSHUsername(username),
			PasswordHash:   string(hash),
			ExpiresAt:      expires,
			MaxConnections: maxConnections,
		})
	}
	return s.writeRecords(records)
}

func (s *sshUserStore) delete(username string) error {
	username = normalizeSSHUsername(username)
	records, err := s.snapshot()
	if err != nil {
		return err
	}
	out := records[:0]
	found := false
	for _, u := range records {
		if u.Username == username {
			found = true
			continue
		}
		out = append(out, u)
	}
	if !found {
		return fmt.Errorf("SSH user %q not found", username)
	}
	return s.writeRecords(out)
}

func (s *sshUserStore) setPassword(username, password string) error {
	username = normalizeSSHUsername(username)
	if err := validateSSHUsername(username); err != nil {
		return err
	}
	if password == "" {
		return errors.New("SSH password is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	records, err := s.snapshot()
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].Username == username {
			records[i].PasswordHash = string(hash)
			return s.writeRecords(records)
		}
	}
	return fmt.Errorf("SSH user %q not found", username)
}

func (s *sshUserStore) updateSettings(username string, days, maxConnections int) error {
	username = normalizeSSHUsername(username)
	if err := validateSSHUsername(username); err != nil {
		return err
	}
	if days < 0 {
		return errors.New("account lifetime days cannot be negative")
	}
	if maxConnections < 0 {
		return errors.New("max connections cannot be negative")
	}
	var expires time.Time
	if days > 0 {
		expires = time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
	}
	records, err := s.snapshot()
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].Username == username {
			records[i].ExpiresAt = expires
			records[i].MaxConnections = maxConnections
			return s.writeRecords(records)
		}
	}
	return fmt.Errorf("SSH user %q not found", username)
}

func (s *sshUserStore) setDisabled(username string, disabled bool) error {
	username = normalizeSSHUsername(username)
	records, err := s.snapshot()
	if err != nil {
		return err
	}
	for i := range records {
		if records[i].Username == username {
			records[i].Disabled = disabled
			return s.writeRecords(records)
		}
	}
	return fmt.Errorf("SSH user %q not found", username)
}

func generateSSHPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

type sshRuntime struct {
	store *sshUserStore
	mu    sync.Mutex
	conns map[string]int
}

func newSSHRuntime(store *sshUserStore) *sshRuntime {
	return &sshRuntime{store: store, conns: make(map[string]int)}
}

func (r *sshRuntime) acquire(username string) (sshUserRecord, error) {
	u, ok := r.store.get(username)
	if !ok || u.Disabled || (!u.ExpiresAt.IsZero() && time.Now().After(u.ExpiresAt)) {
		return sshUserRecord{}, errors.New("account unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if u.MaxConnections > 0 && r.conns[username] >= u.MaxConnections {
		return sshUserRecord{}, fmt.Errorf("max connections reached (%d)", u.MaxConnections)
	}
	r.conns[username]++
	return u, nil
}

func (r *sshRuntime) release(username string) {
	r.mu.Lock()
	if r.conns[username] <= 1 {
		delete(r.conns, username)
	} else {
		r.conns[username]--
	}
	r.mu.Unlock()
}

func ensureSSHHostSigner(path string) (ssh.Signer, error) {
	if data, err := os.ReadFile(path); err == nil {
		return ssh.ParsePrivateKey(data)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return nil, err
	}
	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	data := pem.EncodeToMemory(block)
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

type sshDirectTCPIPRequest struct {
	Host       string
	Port       uint32
	OriginHost string
	OriginPort uint32
}

const sshRelayBufferSize = 64 * 1024

var sshRelayBufferPool = sync.Pool{
	New: func() any {
		b := make([]byte, sshRelayBufferSize)
		return &b
	},
}

func handleSSHDirectTCPIP(newChan ssh.NewChannel, allowPrivate bool, cache *dnsCache, tcpBuffer int) {
	var req sshDirectTCPIPRequest
	if err := ssh.Unmarshal(newChan.ExtraData(), &req); err != nil || req.Host == "" || req.Port == 0 || req.Port > 65535 {
		_ = newChan.Reject(ssh.Prohibited, "bad direct-tcpip request")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var backend net.Conn
	var err error
	if internalAddr, ok := lookupSSHOnlyInternalTarget(req.Host, int(req.Port)); ok {
		d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		backend, err = d.DialContext(ctx, "tcp", internalAddr)
		if err == nil {
			protocol.TuneTCP(backend)
			protocol.TuneTCPBuffer(backend, tcpBuffer)
		}
	} else {
		backend, err = dialTarget(ctx, req.Host, int(req.Port), allowPrivate, cache, tcpBuffer)
	}
	if err != nil {
		_ = newChan.Reject(ssh.ConnectionFailed, "connect failed")
		return
	}
	ch, reqs, err := newChan.Accept()
	if err != nil {
		_ = backend.Close()
		return
	}
	go ssh.DiscardRequests(reqs)

	// Preserve TCP half-close semantics. A client may finish uploading while the
	// destination is still sending a large response, so do not close both sides
	// merely because one copy direction reached EOF.
	var relayWG sync.WaitGroup
	relayWG.Add(2)
	go func() {
		defer relayWG.Done()
		bufPtr := sshRelayBufferPool.Get().(*[]byte)
		defer sshRelayBufferPool.Put(bufPtr)
		_, _ = io.CopyBuffer(backend, ch, *bufPtr)
		if cw, ok := backend.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	go func() {
		defer relayWG.Done()
		bufPtr := sshRelayBufferPool.Get().(*[]byte)
		defer sshRelayBufferPool.Put(bufPtr)
		_, _ = io.CopyBuffer(ch, backend, *bufPtr)
		if cw, ok := ch.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	relayWG.Wait()
	_ = backend.Close()
	_ = ch.Close()
}

func handleSSHDummySession(newChan ssh.NewChannel) {
	ch, reqs, err := newChan.Accept()
	if err != nil {
		return
	}
	go func() {
		defer ch.Close()
		for req := range reqs {
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}()
}

func serveSSHConn(conn net.Conn, cfg *ssh.ServerConfig, runtime *sshRuntime, allowPrivate bool, cache *dnsCache, tcpBuffer int) {
	defer conn.Close()
	protocol.TuneTCP(conn)
	protocol.TuneTCPBuffer(conn, tcpBuffer)
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	sshConn, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	username := sshConn.User()
	user, err := runtime.acquire(username)
	if err != nil {
		log.Printf("fake-ssh rejected user=%q remote=%s: %v", username, sshConn.RemoteAddr(), err)
		_ = sshConn.Close()
		return
	}
	log.Printf("fake-ssh connected user=%q remote=%s mode=tunnel-only", username, sshConn.RemoteAddr())
	defer func() {
		runtime.release(username)
		log.Printf("fake-ssh disconnected user=%q remote=%s", username, sshConn.RemoteAddr())
	}()
	defer sshConn.Close()
	if !user.ExpiresAt.IsZero() {
		remaining := time.Until(user.ExpiresAt)
		if remaining <= 0 {
			return
		}
		expiryTimer := time.AfterFunc(remaining, func() { _ = sshConn.Close() })
		defer expiryTimer.Stop()
	}
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		switch newChan.ChannelType() {
		case "direct-tcpip":
			go handleSSHDirectTCPIP(newChan, allowPrivate, cache, tcpBuffer)
		case "session":
			go handleSSHDummySession(newChan)
		default:
			_ = newChan.Reject(ssh.UnknownChannelType, "unsupported channel type")
		}
	}
}

func startFakeSSH(listenAddr, hostKeyPath string, store *sshUserStore, allowPrivate bool, cache *dnsCache, tcpBuffer int) (net.Listener, string, error) {
	if err := store.Load(); err != nil {
		return nil, "", err
	}
	signer, err := ensureSSHHostSigner(hostKeyPath)
	if err != nil {
		return nil, "", err
	}
	runtime := newSSHRuntime(store)
	cfg := &ssh.ServerConfig{
		NoClientAuth: false,
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if _, err := store.authenticate(meta.User(), password); err != nil {
				log.Printf("fake-ssh auth failed user=%q remote=%s", meta.User(), meta.RemoteAddr())
				return nil, err
			}
			return nil, nil
		},
	}
	cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, "", err
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				log.Printf("fake-ssh accept: %v", err)
				continue
			}
			go serveSSHConn(conn, cfg, runtime, allowPrivate, cache, tcpBuffer)
		}
	}()
	return ln, ssh.FingerprintSHA256(signer.PublicKey()), nil
}

type sshCLIFlags struct {
	usersPath      *string
	addUser        *string
	deleteUser     *string
	password       *string
	passwordEnv    *string
	days           *int
	maxConnections *int
	listUsers      *bool
	menu           *bool
}

func registerSSHCLIFlags() sshCLIFlags {
	return sshCLIFlags{
		usersPath:      flag.String("ssh-users", "dragontcp-users.json", "fake SSH user database JSON path"),
		addUser:        flag.String("ssh-user-add", "", "create or update an SSH tunnel user, then exit"),
		deleteUser:     flag.String("ssh-user-delete", "", "delete an SSH tunnel user, then exit"),
		password:       flag.String("ssh-user-password", "", "password used with --ssh-user-add"),
		passwordEnv:    flag.String("ssh-user-password-env", "", "environment variable containing password for --ssh-user-add"),
		days:           flag.Int("ssh-user-days", 0, "account lifetime in days; 0 means no expiry"),
		maxConnections: flag.Int("ssh-user-max-connections", 1, "maximum simultaneous SSH connections for the account; 0 means unlimited"),
		listUsers:      flag.Bool("ssh-user-list", false, "list SSH tunnel users, then exit"),
		menu:           flag.Bool("ssh-menu", false, "interactive SSH tunnel user management menu, then exit"),
	}
}

func menuReadLine(reader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func menuReadInt(reader *bufio.Reader, prompt string, defaultValue, minValue int) (int, error) {
	for {
		line, err := menuReadLine(reader, prompt)
		if err != nil {
			return 0, err
		}
		if line == "" {
			return defaultValue, nil
		}
		v, err := strconv.Atoi(line)
		if err != nil || v < minValue {
			fmt.Printf("Enter a number >= %d.\n", minValue)
			continue
		}
		return v, nil
	}
}

func printSSHUserList(store *sshUserStore) error {
	records, err := store.snapshot()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		fmt.Println("No SSH tunnel users.")
		return nil
	}
	fmt.Printf("%-22s %-26s %-16s %s\n", "USERNAME", "EXPIRES", "MAX CONNECTIONS", "STATUS")
	fmt.Printf("%-22s %-26s %-16s %s\n", strings.Repeat("-", 8), strings.Repeat("-", 7), strings.Repeat("-", 15), strings.Repeat("-", 6))
	now := time.Now()
	for _, u := range records {
		expiry := "never"
		status := "active"
		if !u.ExpiresAt.IsZero() {
			expiry = u.ExpiresAt.Local().Format("2006-01-02 15:04 MST")
			if now.After(u.ExpiresAt) {
				status = "expired"
			}
		}
		if u.Disabled {
			status = "disabled"
		}
		max := "unlimited"
		if u.MaxConnections > 0 {
			max = strconv.Itoa(u.MaxConnections)
		}
		fmt.Printf("%-22s %-26s %-16s %s\n", u.Username, expiry, max, status)
	}
	return nil
}

func runSSHUserMenu(store *sshUserStore) error {
	if err := store.Load(); err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println()
		fmt.Println("========================================")
		fmt.Println(" DragonTCP SSH Tunnel User Manager")
		fmt.Println("========================================")
		fmt.Printf("User database: %s\n\n", store.path)
		fmt.Println("  1) Create user (automatic password)")
		fmt.Println("  2) Delete user")
		fmt.Println("  3) List users")
		fmt.Println("  4) Reset user password (automatic)")
		fmt.Println("  5) Renew/edit expiry and connection limit")
		fmt.Println("  0) Exit")
		choice, err := menuReadLine(reader, "\nSelect: ")
		if err != nil {
			return err
		}
		switch choice {
		case "0", "q", "quit", "exit":
			return nil
		case "1":
			username, err := menuReadLine(reader, "Username: ")
			if err != nil {
				return err
			}
			if err := validateSSHUsername(username); err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}
			if _, exists := store.get(username); exists {
				fmt.Printf("User %q already exists. Use option 4 or 5 to change it.\n", username)
				continue
			}
			days, err := menuReadInt(reader, "Days [30, 0 = never expires]: ", 30, 0)
			if err != nil {
				return err
			}
			maxConnections, err := menuReadInt(reader, "Max connections [1, 0 = unlimited]: ", 1, 0)
			if err != nil {
				return err
			}
			password, err := generateSSHPassword()
			if err != nil {
				fmt.Printf("Error generating password: %v\n", err)
				continue
			}
			if err := store.upsert(username, password, days, maxConnections); err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}
			u, _ := store.get(username)
			expiry := "never"
			if !u.ExpiresAt.IsZero() {
				expiry = u.ExpiresAt.Local().Format("2006-01-02 15:04 MST")
			}
			fmt.Println("\nUser created successfully.")
			fmt.Printf("Username:        %s\n", u.Username)
			fmt.Printf("Password:        %s\n", password)
			fmt.Printf("Expires:         %s\n", expiry)
			fmt.Printf("Max connections: %d\n", u.MaxConnections)
			fmt.Println("Save the password now. DragonTCP stores only its bcrypt hash and cannot display it later.")
		case "2":
			username, err := menuReadLine(reader, "Username to delete: ")
			if err != nil {
				return err
			}
			if _, exists := store.get(username); !exists {
				fmt.Printf("User %q not found.\n", normalizeSSHUsername(username))
				continue
			}
			confirm, err := menuReadLine(reader, fmt.Sprintf("Delete %q? [y/N]: ", normalizeSSHUsername(username)))
			if err != nil {
				return err
			}
			if !strings.EqualFold(confirm, "y") && !strings.EqualFold(confirm, "yes") {
				fmt.Println("Delete cancelled.")
				continue
			}
			if err := store.delete(username); err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}
			fmt.Printf("User %q deleted.\n", normalizeSSHUsername(username))
		case "3":
			if err := printSSHUserList(store); err != nil {
				fmt.Printf("Error: %v\n", err)
			}
		case "4":
			username, err := menuReadLine(reader, "Username: ")
			if err != nil {
				return err
			}
			if _, exists := store.get(username); !exists {
				fmt.Printf("User %q not found.\n", normalizeSSHUsername(username))
				continue
			}
			password, err := generateSSHPassword()
			if err != nil {
				fmt.Printf("Error generating password: %v\n", err)
				continue
			}
			if err := store.setPassword(username, password); err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}
			fmt.Printf("New password for %s: %s\n", normalizeSSHUsername(username), password)
			fmt.Println("Save it now; only the bcrypt hash is stored.")
		case "5":
			username, err := menuReadLine(reader, "Username: ")
			if err != nil {
				return err
			}
			u, exists := store.get(username)
			if !exists {
				fmt.Printf("User %q not found.\n", normalizeSSHUsername(username))
				continue
			}
			days, err := menuReadInt(reader, "New lifetime from now in days [30, 0 = never expires]: ", 30, 0)
			if err != nil {
				return err
			}
			maxDefault := u.MaxConnections
			maxConnections, err := menuReadInt(reader, fmt.Sprintf("Max connections [%d, 0 = unlimited]: ", maxDefault), maxDefault, 0)
			if err != nil {
				return err
			}
			if err := store.updateSettings(username, days, maxConnections); err != nil {
				fmt.Printf("Error: %v\n", err)
				continue
			}
			fmt.Printf("User %q updated. Password was not changed.\n", normalizeSSHUsername(username))
		default:
			fmt.Println("Invalid selection.")
		}
	}
}

func handleSSHCLI(flags sshCLIFlags) (bool, error) {
	store := newSSHUserStore(*flags.usersPath)
	actions := 0
	if strings.TrimSpace(*flags.addUser) != "" {
		actions++
	}
	if strings.TrimSpace(*flags.deleteUser) != "" {
		actions++
	}
	if *flags.listUsers {
		actions++
	}
	if *flags.menu {
		actions++
	}
	if actions == 0 {
		return false, nil
	}
	if actions > 1 {
		return true, errors.New("choose only one of --ssh-menu, --ssh-user-add, --ssh-user-delete, or --ssh-user-list")
	}
	if *flags.menu {
		return true, runSSHUserMenu(store)
	}
	if strings.TrimSpace(*flags.addUser) != "" {
		password := *flags.password
		if *flags.passwordEnv != "" {
			password = os.Getenv(*flags.passwordEnv)
		}
		if err := store.upsert(*flags.addUser, password, *flags.days, *flags.maxConnections); err != nil {
			return true, err
		}
		u, _ := store.get(*flags.addUser)
		expiry := "never"
		if !u.ExpiresAt.IsZero() {
			expiry = u.ExpiresAt.Format(time.RFC3339)
		}
		fmt.Printf("SSH user %s saved (expires=%s max_connections=%d)\n", u.Username, expiry, u.MaxConnections)
		return true, nil
	}
	if strings.TrimSpace(*flags.deleteUser) != "" {
		if err := store.delete(*flags.deleteUser); err != nil {
			return true, err
		}
		fmt.Printf("SSH user %s deleted\n", normalizeSSHUsername(*flags.deleteUser))
		return true, nil
	}
	records, err := store.snapshot()
	if err != nil {
		return true, err
	}
	for _, u := range records {
		expiry := "never"
		if !u.ExpiresAt.IsZero() {
			expiry = u.ExpiresAt.Format(time.RFC3339)
		}
		fmt.Printf("%s expires=%s max_connections=%d disabled=%t\n", u.Username, expiry, u.MaxConnections, u.Disabled)
	}
	return true, nil
}
