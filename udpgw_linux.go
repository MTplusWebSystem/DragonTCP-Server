//go:build linux

package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// findDefaultInterface finds the default physical network interface used for outbound traffic.
func findDefaultInterface() (string, error) {
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 53})
	if err != nil {
		return "", err
	}
	defer conn.Close()
	localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", fmt.Errorf("cannot get local UDP address")
	}
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.Equal(localAddr.IP) {
				return iface.Name, nil
			}
		}
	}
	// Fallback to first non-loopback up interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			return iface.Name, nil
		}
	}
	return "", fmt.Errorf("no matching default network interface")
}

// setupNativeUDPSocket applies Linux ABI socket options to discharge UDP datagrams
// directly onto the specified physical network interface card (NIC).
func setupNativeUDPSocket(conn *net.UDPConn, iface string, busyPollUS int) (string, error) {
	targetIface := strings.TrimSpace(iface)
	if targetIface == "" || targetIface == "auto" {
		if detected, err := findDefaultInterface(); err == nil && detected != "" {
			targetIface = detected
		}
	}

	rawConn, err := conn.SyscallConn()
	if err != nil {
		return targetIface, err
	}

	var sockErr error
	ctrlErr := rawConn.Control(func(fd uintptr) {
		fdInt := int(fd)

		// 1. SO_BINDTODEVICE: bind directly to the physical network card (NIC)!
		// Packets skip normal route table re-evaluation and are sent straight to the NIC driver queue.
		if targetIface != "" && targetIface != "auto" {
			if err := unix.BindToDevice(fdInt, targetIface); err != nil {
				// Non-fatal if lacking CAP_NET_RAW/root, but record error
				sockErr = fmt.Errorf("SO_BINDTODEVICE on %s: %w", targetIface, err)
			}
		}

		// 2. SO_BUSY_POLL: enable low-latency busy polling directly on the NIC queue (zero-interrupt latency).
		if busyPollUS > 0 {
			_ = unix.SetsockoptInt(fdInt, unix.SOL_SOCKET, unix.SO_BUSY_POLL, busyPollUS)
		}

		// 3. Force large ring buffers (4 MB) directly via ABI
		_ = unix.SetsockoptInt(fdInt, unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, 4*1024*1024)
		_ = unix.SetsockoptInt(fdInt, unix.SOL_SOCKET, unix.SO_SNDBUFFORCE, 4*1024*1024)
		_ = unix.SetsockoptInt(fdInt, unix.SOL_SOCKET, unix.SO_RCVBUF, 4*1024*1024)
		_ = unix.SetsockoptInt(fdInt, unix.SOL_SOCKET, unix.SO_SNDBUF, 4*1024*1024)

		// 4. Low-delay IP_TOS for gaming & VoIP
		_ = unix.SetsockoptInt(fdInt, unix.IPPROTO_IP, unix.IP_TOS, 0x10) // IPTOS_LOWDELAY

		// 5. IP_PMTUDISC_DONT: avoid PMTU blackholing for fast UDP datagrams
		_ = unix.SetsockoptInt(fdInt, unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DONT)
	})

	if ctrlErr != nil {
		return targetIface, ctrlErr
	}
	return targetIface, sockErr
}

// ── Receive loops ─────────────────────────────────────────────────────────────

// readNativeUDP is the hot receive loop for native ABI mode.
// Uses standard ReadFromUDP (compatible) with ABI socket tuning already applied above.
// For kernels 4.0+ the recvmmsg(2) batch path can be enabled via rawConn in future iterations.
func readNativeUDP(conn *net.UDPConn, mappings *udpMappings, writeCh chan<- []byte, done <-chan struct{}) {
	readStandardUDP(conn, mappings, writeCh, done)
}

// readStandardUDP is the base receive loop used by both standard and native modes.
func readStandardUDP(conn *net.UDPConn, mappings *udpMappings, writeCh chan<- []byte, done <-chan struct{}) {
	buf := make([]byte, 65535)
	for {
		select {
		case <-done:
			return
		default:
		}
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		ip4 := addr.IP.To4()
		if ip4 == nil || n <= 0 {
			continue
		}
		var ip [4]byte
		copy(ip[:], ip4)
		key := udpDestKey{ip: ip, port: uint16(addr.Port)}
		v, ok := mappings.get(key)
		if !ok {
			continue
		}
		frame := udpgwBuildFrame(v.connID, v.x, ip, uint16(addr.Port), buf[:n])
		select {
		case writeCh <- frame:
		default:
		}
	}
}

// readTunUDP reads raw IPv4/UDP packets from the TUN device and builds UDPGW frames.
func readTunUDP(tun *tunDevice, mappings *udpMappings, writeCh chan<- []byte, done <-chan struct{}) {
	pkt := make([]byte, 65535)
	for {
		select {
		case <-done:
			return
		default:
		}
		n, err := tun.file.Read(pkt)
		if err != nil || n < 28 { // IPv4(20) + UDP(8) minimum
			if err != nil {
				return
			}
			continue
		}
		// Parse minimal IPv4 header
		if pkt[0]>>4 != 4 { // not IPv4
			continue
		}
		proto := pkt[9]
		if proto != 17 { // not UDP
			continue
		}
		ihl := int(pkt[0]&0xf) * 4
		if n < ihl+8 {
			continue
		}
		var srcIP [4]byte
		copy(srcIP[:], pkt[12:16])
		srcPort := uint16(pkt[ihl])<<8 | uint16(pkt[ihl+1])
		udpPayload := pkt[ihl+8 : n]

		key := udpDestKey{ip: srcIP, port: srcPort}
		v, ok := mappings.get(key)
		if !ok {
			continue
		}
		frame := udpgwBuildFrame(v.connID, v.x, srcIP, srcPort, udpPayload)
		select {
		case writeCh <- frame:
		default:
		}
	}
}

// writeTunUDP injects a UDP datagram into the TUN device as a raw IPv4+UDP packet.
func writeTunUDP(tun *tunDevice, srcIP [4]byte, srcPort uint16, dstIP [4]byte, dstPort uint16, payload []byte) {
	totalLen := 20 + 8 + len(payload)
	pkt := make([]byte, totalLen)

	// IPv4 header
	pkt[0] = 0x45 // Version=4, IHL=5
	pkt[1] = 0x10 // DSCP IPTOS_LOWDELAY
	pkt[2] = byte(totalLen >> 8)
	pkt[3] = byte(totalLen)
	// ID, flags, frag offset = 0 (pkt[4:8])
	pkt[8] = 64 // TTL
	pkt[9] = 17 // Protocol: UDP
	// checksum at pkt[10:12] filled below
	copy(pkt[12:16], srcIP[:])
	copy(pkt[16:20], dstIP[:])

	// UDP header
	pkt[20] = byte(srcPort >> 8)
	pkt[21] = byte(srcPort)
	pkt[22] = byte(dstPort >> 8)
	pkt[23] = byte(dstPort)
	udpLen := uint16(8 + len(payload))
	pkt[24] = byte(udpLen >> 8)
	pkt[25] = byte(udpLen)
	// UDP checksum = 0 (optional for IPv4 as per RFC 768)
	copy(pkt[28:], payload)

	// IPv4 header checksum (one's complement)
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(pkt[i])<<8 | uint32(pkt[i+1])
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	chk := ^uint16(sum)
	pkt[10] = byte(chk >> 8)
	pkt[11] = byte(chk)

	_, _ = tun.file.Write(pkt)
}

// tunDevice represents a native Linux /dev/net/tun interface.
type tunDevice struct {
	file *os.File
	name string
}

func (t *tunDevice) Close() error {
	if t == nil || t.file == nil {
		return nil
	}
	return t.file.Close()
}

// openTunDevice opens and configures a native Linux TUN device (/dev/net/tun).
// It uses a raw TUNSETIFF ioctl with a manually assembled ifreq buffer because
// the golang.org/x/sys/unix.Ifreq API does not expose the raw Ifrn/Ifru union.
func openTunDevice(name string) (*tunDevice, error) {
	fd, err := unix.Open("/dev/net/tun", unix.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/net/tun: %w", err)
	}

	// ifreq layout: [IFNAMSIZ(16) bytes name] + [union 24 bytes flags…]
	// We only need the first 2 bytes of the union for IFF_TUN|IFF_NO_PI.
	var ifr [40]byte
	ifName := "dtun%d"
	if name != "" {
		ifName = name
	}
	copy(ifr[:unix.IFNAMSIZ], ifName)
	// flags at offset 16 (little-endian uint16)
	flags := uint16(unix.IFF_TUN | unix.IFF_NO_PI)
	ifr[unix.IFNAMSIZ] = byte(flags)
	ifr[unix.IFNAMSIZ+1] = byte(flags >> 8)

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(unix.TUNSETIFF),
		uintptr(unsafe.Pointer(&ifr[0])),
	)
	if errno != 0 {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("ioctl TUNSETIFF: %w", errno)
	}

	// Extract the assigned interface name (null-terminated)
	actualName := unix.ByteSliceToString(ifr[:unix.IFNAMSIZ])
	file := os.NewFile(uintptr(fd), "/dev/net/tun")
	return &tunDevice{file: file, name: actualName}, nil
}
