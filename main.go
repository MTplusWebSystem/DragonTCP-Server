package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dragontcp/internal/cover"
	"dragontcp/internal/protocol"
)

var active int64

// idleDeadline avoids a SetDeadline system call for every small protocol
// record. It refreshes halfway through the idle window, preserving idle-client
// cleanup while making persistent high-throughput lanes substantially cheaper.
type idleDeadline struct {
	conn    net.Conn
	timeout time.Duration
	next    time.Time
}

func newIdleDeadline(conn net.Conn, timeout time.Duration) *idleDeadline {
	return &idleDeadline{conn: conn, timeout: timeout}
}

func (d *idleDeadline) refresh() error {
	now := time.Now()
	if !d.next.IsZero() && now.Before(d.next.Add(-d.timeout/2)) {
		return nil
	}
	d.next = now.Add(d.timeout)
	return d.conn.SetDeadline(d.next)
}

type dnsEntry struct {
	ips     []netip.Addr
	expires time.Time
}

type dnsCache struct {
	mu      sync.RWMutex
	entries map[string]dnsEntry
	ttl     time.Duration
	max     int
}

func newDNSCache(ttl time.Duration, max int) *dnsCache {
	return &dnsCache{
		entries: make(map[string]dnsEntry),
		ttl:     ttl,
		max:     max,
	}
}

func (c *dnsCache) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}

	now := time.Now()
	c.mu.RLock()
	entry, ok := c.entries[host]
	c.mu.RUnlock()
	if ok && now.Before(entry.expires) {
		return entry.ips, nil
	}

	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	if len(c.entries) >= c.max {
		// Simple bounded reset keeps the hot cache cheap and prevents growth.
		c.entries = make(map[string]dnsEntry, c.max)
	}
	c.entries[host] = dnsEntry{ips: ips, expires: now.Add(c.ttl)}
	c.mu.Unlock()

	return ips, nil
}

func tokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

var blockedSpecial = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func addressAllowed(addr netip.Addr, allowPrivate bool) bool {
	if addr.IsUnspecified() || addr.IsMulticast() {
		return false
	}

	if allowPrivate {
		return true
	}

	if !addr.IsGlobalUnicast() ||
		addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() {
		return false
	}

	for _, prefix := range blockedSpecial {
		if prefix.Contains(addr) {
			return false
		}
	}

	return true
}

func dialTarget(ctx context.Context, host string, port int, allowPrivate bool, cache *dnsCache, tcpBuffer int) (net.Conn, error) {
	if internalAddr, ok := lookupInternalTarget(host, port); ok {
		d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", internalAddr)
		if err != nil {
			return nil, err
		}
		protocol.TuneTCP(conn)
		protocol.TuneTCPBuffer(conn, tcpBuffer)
		return conn, nil
	}

	ips, err := cache.resolve(ctx, host)
	if err != nil {
		return nil, err
	}

	var lastErr error
	var blocked []string

	d := net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	for _, ip := range ips {
		if !addressAllowed(ip, allowPrivate) {
			blocked = append(blocked, ip.String())
			continue
		}

		addr := net.JoinHostPort(ip.String(), strconv.Itoa(port))
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			protocol.TuneTCP(conn)
			protocol.TuneTCPBuffer(conn, tcpBuffer)
			return conn, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return nil, lastErr
	}
	if len(blocked) > 0 {
		return nil, fmt.Errorf("target resolves only to blocked addresses: %s", strings.Join(blocked, ","))
	}
	return nil, fmt.Errorf("no usable target address")
}

func handle(
	conn net.Conn,
	token string,
	allowPrivate bool,
	cache *dnsCache,
	tcpBuffer int,
	slots chan struct{},
	manager *streamManager,
	bhttpManager *bhttpSessionManager,
	xorManager *chunkManager,
	chunkMax int,
	bufferBytes int,
	xorBufferBytes int,
	chunkPollWait time.Duration,
	debug *serverDebug,
) {
	defer func() {
		<-slots
		atomic.AddInt64(&active, -1)
		_ = conn.Close()
	}()

	protocol.TuneTCP(conn)
	protocol.TuneTCPBuffer(conn, tcpBuffer)

	// One listener serves both wires and every startup-selected header profile.
	// sniffWire partitions the full first-byte space so B and X remain
	// unambiguous even when their legacy mode/UP bytes are masked.
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	conn, isXOR, headerMask, err := sniffWire(conn)
	if err != nil {
		return
	}
	if isXOR {
		if debug != nil && debug.enabled {
			debug.logf("WIRE peer=%v mode=xor header_mask=%02x", conn.RemoteAddr(), headerMask)
		}
		handleXOR(conn, headerMask, token, allowPrivate, cache, tcpBuffer, xorManager,
			chunkMax, xorBufferBytes, chunkPollWait, debug)
		return
	}
	clearPayload := false
	coverID := uint16(0)
	covered := false
	if profiled, ok := conn.(interface{ ClearPayload() bool }); ok {
		clearPayload = profiled.ClearPayload()
	}
	if profiled, ok := conn.(interface{ CoverProfile() cover.Profile }); ok {
		profile := profiled.CoverProfile()
		if profile.Enabled {
			covered = true
			coverID = profile.ID
		}
	}
	muxV2 := false
	if profiled, ok := conn.(interface{ MuxV2() bool }); ok {
		muxV2 = profiled.MuxV2()
	}

	if muxV2 {
		if debug != nil && debug.enabled {
			if covered {
				debug.logf("WIRE peer=%v mode=mux_v2 header_mask=%02x clear_payload=%t cover_id=%04x", conn.RemoteAddr(), headerMask, clearPayload, coverID)
			} else {
				debug.logf("WIRE peer=%v mode=mux_v2 header_mask=%02x clear_payload=%t cover_id=direct", conn.RemoteAddr(), headerMask, clearPayload)
			}
		}
		mc := newMuxServerConn(conn, headerMask, clearPayload)
		handleMuxConnection(mc, token, allowPrivate, cache, tcpBuffer, manager, chunkMax, bufferBytes, chunkPollWait, debug)
		return
	}

	if debug != nil && debug.enabled {
		if covered {
			debug.logf("WIRE peer=%v mode=binary header_mask=%02x clear_payload=%t cover_id=%04x", conn.RemoteAddr(), headerMask, clearPayload, coverID)
		} else {
			debug.logf("WIRE peer=%v mode=binary header_mask=%02x clear_payload=%t cover_id=direct", conn.RemoteAddr(), headerMask, clearPayload)
		}
	}

	handleBinary(conn, headerMask, clearPayload, token, allowPrivate, cache, tcpBuffer, manager,
		bhttpManager, chunkMax, bufferBytes, chunkPollWait, debug)
}

func acceptLoop(
	ln net.Listener,
	token string,
	allowPrivate bool,
	cache *dnsCache,
	tcpBuffer int,
	slots chan struct{},
	manager *streamManager,
	bhttpManager *bhttpSessionManager,
	xorManager *chunkManager,
	chunkMax int,
	bufferBytes int,
	xorBufferBytes int,
	chunkPollWait time.Duration,
	debug *serverDebug,
) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			fmt.Fprintln(os.Stderr, "accept:", err)
			continue
		}

		select {
		case slots <- struct{}{}:
			atomic.AddInt64(&active, 1)
			if debug.enabled {
				debug.logf("ACCEPT local=%v peer=%v active_connections=%d", conn.LocalAddr(), conn.RemoteAddr(), atomic.LoadInt64(&active))
			}
			go handle(conn, token, allowPrivate, cache, tcpBuffer, slots, manager,
				bhttpManager, xorManager, chunkMax, bufferBytes, xorBufferBytes,
				chunkPollWait, debug)
		default:
			if debug.enabled {
				debug.errorf("REJECT peer=%v reason=max-connections", conn.RemoteAddr())
			}
			_ = conn.Close()
		}
	}
}

func main() {
	sshCLI := registerSSHCLIFlags()
	var (
		configFile     = flag.String("config", "", "path to YAML configuration file")
		host           = flag.String("host", "0.0.0.0", "listen host")
		port           = flag.Int("port", 53, "listen port")
		portAlt        = flag.Int("port-alt", 80, "second simultaneous listen port; 0 disables")
		token          = flag.String("token", "", "optional shared token")
		maxConnections = flag.Int("max-connections", 20000, "max simultaneous tunnels")
		allowPrivate   = flag.Bool("allow-private", false, "allow private/loopback targets")
		dnsCacheTTL    = flag.Duration("dns-cache-ttl", 30*time.Second, "server DNS cache TTL")
		dnsCacheSize   = flag.Int("dns-cache-size", 4096, "maximum cached DNS hostnames")
		tcpBuffer      = flag.Int("tcp-buffer", 0, "optional TCP read/write buffer bytes; 0 keeps OS autotuning")
		chunkMax       = flag.Int("chunk-max", 1048576, "maximum adaptive chunk payload bytes (32 bytes to 1 MiB)")
		chunkBuffered  = flag.Int("chunk-buffered", 32, "per-session download buffer in 64 KiB units; 32 = about 2 MiB")
		chunkPollWait  = flag.Duration("chunk-poll-wait", 200*time.Millisecond, "server long-poll wait for chunk data")
		sessionTimeout = flag.Duration("chunk-session-timeout", 2*time.Minute, "idle chunk session timeout")
		debugEnabled   = flag.Bool("debug", false, "log session/connect/errors and periodic statistics")
		debugChunks    = flag.Bool("debug-chunks", false, "log every chunk protocol record; very verbose")
		debugStats     = flag.Duration("debug-stats-interval", 5*time.Second, "periodic debug statistics interval; 0 disables")

		sshEnable         = flag.Bool("ssh-enable", true, "enable the internal tunnel-only SSH service")
		sshListen         = flag.String("ssh-listen", defaultSSHListen, "internal fake SSH listen address")
		sshInternalHost   = flag.String("ssh-internal-host", defaultSSHInternalHost, "reserved DragonTCP target name used by clients for SSH")
		sshHostKey        = flag.String("ssh-host-key", "dragontcp_ssh_host_key", "SSH host private-key path; generated automatically if missing")
		udpgwEnable       = flag.Bool("udpgw-enable", true, "enable integrated BadVPN-compatible UDPGW")
		udpgwListen       = flag.String("udpgw-listen", "127.0.0.1:7400", "UDPGW listen address; loopback is recommended")
		udpgwInternalHost = flag.String("udpgw-internal-host", "dragontcp-udpgw.internal", "reserved SSH direct-tcpip target name for UDPGW")
		udpgwMaxClients   = flag.Int("udpgw-max-clients", 10000, "maximum concurrent UDPGW TCP clients")
		udpgwMode         = flag.String("udpgw-mode", "native", "UDP discharge mode: 'native' (Linux ABI direct NIC discharge), 'tun' (/dev/net/tun), or 'standard' (user-space udpgw)")
		udpgwInterface    = flag.String("udpgw-interface", "auto", "network interface for native ABI discharge (e.g. eth0, ens3, or 'auto')")
		udpgwBusyPoll     = flag.Int("udpgw-busy-poll", 50, "SO_BUSY_POLL in microseconds for low-latency native ABI (0 disables)")
		udpgwDebug        = flag.Bool("udpgw-debug", false, "verbose UDPGW errors")
		adminAddr         = flag.String("admin-addr", "127.0.0.1:53080", "local HTTP administration and CLI endpoint; empty disables")
	)
	flag.Parse()

	visited := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})

	if *configFile != "" {
		cfg, err := LoadConfigFile(*configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "config file error: %v\n", err)
			os.Exit(1)
		}
		applyConfig(cfg, visited, serverFlagTargets{
			host:              host,
			port:              port,
			portAlt:           portAlt,
			token:             token,
			maxConnections:    maxConnections,
			allowPrivate:      allowPrivate,
			dnsCacheTTL:       dnsCacheTTL,
			dnsCacheSize:      dnsCacheSize,
			tcpBuffer:         tcpBuffer,
			chunkMax:          chunkMax,
			chunkBuffered:     chunkBuffered,
			chunkPollWait:     chunkPollWait,
			sessionTimeout:    sessionTimeout,
			debugEnabled:      debugEnabled,
			debugChunks:       debugChunks,
			debugStats:        debugStats,
			adminAddr:         adminAddr,
			sshEnable:         sshEnable,
			sshListen:         sshListen,
			sshInternalHost:   sshInternalHost,
			sshHostKey:        sshHostKey,
			sshUsers:          sshCLI.usersPath,
			udpgwEnable:       udpgwEnable,
			udpgwListen:       udpgwListen,
			udpgwInternalHost: udpgwInternalHost,
			udpgwMaxClients:   udpgwMaxClients,
			udpgwMode:         udpgwMode,
			udpgwInterface:    udpgwInterface,
			udpgwBusyPoll:     udpgwBusyPoll,
			udpgwDebug:        udpgwDebug,
		})
		fmt.Printf("config=%s\n", *configFile)
	}

	if handled, err := handleSSHCLI(sshCLI); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}

	if *chunkMax < 32 || *chunkMax > protocol.MaxChunkPayload {
		fmt.Fprintf(os.Stderr, "--chunk-max must be between 32 and %d\n", protocol.MaxChunkPayload)
		os.Exit(2)
	}
	if *chunkBuffered < 8 {
		fmt.Fprintln(os.Stderr, "--chunk-buffered must be at least 8")
		os.Exit(2)
	}

	cache := newDNSCache(*dnsCacheTTL, *dnsCacheSize)

	var udpServer *udpgwServer
	if *udpgwEnable {
		var err error
		udpServer, err = startUDPGWServer(udpgwServerConfig{
			Listen:     *udpgwListen,
			MaxClients: *udpgwMaxClients,
			Mode:       *udpgwMode,
			Interface:  *udpgwInterface,
			BusyPollUS: *udpgwBusyPoll,
			Debug:      *udpgwDebug,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "UDPGW start failed: %v\n", err)
			os.Exit(1)
		}
		defer udpServer.Close()
		_, udpPortText, err := net.SplitHostPort(udpServer.ln.Addr().String())
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid UDPGW listener: %v\n", err)
			os.Exit(2)
		}
		udpPort, err := strconv.Atoi(udpPortText)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid UDPGW port: %v\n", err)
			os.Exit(2)
		}
		registerSSHOnlyInternalTarget(*udpgwInternalHost, udpPort, udpServer.ln.Addr().String())
		fmt.Printf("udpgw=true mode=%s interface=%s listen=%s internal_target=%s:%d max_clients=%d\n", udpServer.cfg.Mode, udpServer.cfg.Interface, udpServer.ln.Addr(), *udpgwInternalHost, udpPort, *udpgwMaxClients)
	}

	sshStore := newSSHUserStore(*sshCLI.usersPath)
	var sshListener net.Listener
	if *sshEnable {
		listener, fingerprint, err := startFakeSSH(*sshListen, *sshHostKey, sshStore, *allowPrivate, cache, *tcpBuffer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fake SSH start failed: %v\n", err)
			os.Exit(1)
		}
		sshListener = listener
		defer sshListener.Close()
		sshBoundAddr := sshListener.Addr().String()
		_, sshPortText, err := net.SplitHostPort(sshBoundAddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid fake SSH listener: %v\n", err)
			os.Exit(2)
		}
		sshPort, err := strconv.Atoi(sshPortText)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid fake SSH listener port: %v\n", err)
			os.Exit(2)
		}
		registerInternalTarget(*sshInternalHost, sshPort, sshBoundAddr)
		fmt.Printf("fake_ssh=true listen=%s internal_target=%s:%d hostkey=%s users=%s\n", sshBoundAddr, *sshInternalHost, sshPort, fingerprint, *sshCLI.usersPath)
	}

	listenAddr := net.JoinHostPort(*host, strconv.Itoa(*port))
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer ln.Close()
	listeners := []net.Listener{ln}
	fmt.Printf("DragonTCP Go server listening on %s\n", listenAddr)
	if *portAlt < 0 || *portAlt > 65535 {
		fmt.Fprintln(os.Stderr, "--port-alt must be between 0 and 65535")
		os.Exit(2)
	}
	if *portAlt != 0 && *portAlt != *port {
		altAddr := net.JoinHostPort(*host, strconv.Itoa(*portAlt))
		alt, altErr := net.Listen("tcp", altAddr)
		if altErr != nil {
			fmt.Fprintf(os.Stderr, "warning: secondary listener %s unavailable: %v\n", altAddr, altErr)
		} else {
			defer alt.Close()
			listeners = append(listeners, alt)
			fmt.Printf("DragonTCP Go server listening on %s\n", altAddr)
		}
	}
	fmt.Printf("max_connections=%d tcp_buffer=%d\n", *maxConnections, *tcpBuffer)

	slots := make(chan struct{}, *maxConnections)
	debug := newServerDebug(*debugEnabled, *debugChunks, *debugStats)
	bufferBytes := *chunkBuffered * 65536
	if bufferBytes < 1024*1024 {
		bufferBytes = 1024 * 1024
	}
	if bufferBytes > 64*1024*1024 {
		bufferBytes = 64 * 1024 * 1024
	}
	manager := newStreamManager(*sessionTimeout, debug)
	bhttpManager := newBHTTPSessionManager(*sessionTimeout, *maxConnections)
	xorManager := newChunkManager(*sessionTimeout, debug)

	targets := serverFlagTargets{
		host:              host,
		port:              port,
		portAlt:           portAlt,
		token:             token,
		maxConnections:    maxConnections,
		allowPrivate:      allowPrivate,
		dnsCacheTTL:       dnsCacheTTL,
		dnsCacheSize:      dnsCacheSize,
		tcpBuffer:         tcpBuffer,
		chunkMax:          chunkMax,
		chunkBuffered:     chunkBuffered,
		chunkPollWait:     chunkPollWait,
		sessionTimeout:    sessionTimeout,
		debugEnabled:      debugEnabled,
		debugChunks:       debugChunks,
		debugStats:        debugStats,
		adminAddr:         adminAddr,
		sshEnable:         sshEnable,
		sshListen:         sshListen,
		sshInternalHost:   sshInternalHost,
		sshHostKey:        sshHostKey,
		sshUsers:          sshCLI.usersPath,
		udpgwEnable:       udpgwEnable,
		udpgwListen:       udpgwListen,
		udpgwInternalHost: udpgwInternalHost,
		udpgwMaxClients:   udpgwMaxClients,
		udpgwDebug:        udpgwDebug,
	}
	if *adminAddr != "" {
		adminSrv, err := startAdminServer(*adminAddr, manager, sshStore, debug, targets)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: admin server start failed on %s: %v\n", *adminAddr, err)
		} else if adminSrv != nil {
			defer adminSrv.Close()
			fmt.Printf("admin_server=true listen=%s\n", *adminAddr)
		}
	}

	fmt.Printf("binary_transport=true bp_compat=true chunk_max=%d buffer_bytes=%d poll_wait=%s\n", *chunkMax, bufferBytes, chunkPollWait.String())
	if debug.enabled {
		fmt.Printf("debug=true debug_chunks=%t stats_interval=%s\n", debug.chunks, debug.statsEvery)
	}

	for _, listener := range listeners {
		go acceptLoop(listener, *token, *allowPrivate, cache, *tcpBuffer, slots,
			manager, bhttpManager, xorManager, *chunkMax, bufferBytes,
			bufferBytes, *chunkPollWait, debug)
	}
	select {}
}
