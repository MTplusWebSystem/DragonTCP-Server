//go:build !linux

package main

import (
	"fmt"
	"net"
)

// tunDevice is a stub for non-Linux platforms.
type tunDevice struct {
	name string
}

func (t *tunDevice) Close() error { return nil }

func findDefaultInterface() (string, error) {
	return "standard-nic", nil
}

// setupNativeUDPSocket applies best-effort buffer tuning on non-Linux platforms.
// Linux-specific options (SO_BINDTODEVICE, SO_BUSY_POLL, SO_RCVBUFFORCE) are skipped.
func setupNativeUDPSocket(conn *net.UDPConn, iface string, busyPollUS int) (string, error) {
	_ = conn.SetReadBuffer(4 * 1024 * 1024)
	_ = conn.SetWriteBuffer(4 * 1024 * 1024)
	if iface == "" || iface == "auto" {
		iface = "generic-nic"
	}
	return iface, nil
}

// openTunDevice is unsupported on non-Linux platforms.
func openTunDevice(name string) (*tunDevice, error) {
	return nil, fmt.Errorf("TUN device mode requires Linux ABI (/dev/net/tun)")
}

// readNativeUDP falls back to the standard read loop on non-Linux platforms.
func readNativeUDP(conn *net.UDPConn, mappings *udpMappings, writeCh chan<- []byte, done <-chan struct{}) {
	readStandardUDP(conn, mappings, writeCh, done)
}

// readStandardUDP is the portable UDP receive loop.
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

// readTunUDP is unsupported on non-Linux platforms (should never be called).
func readTunUDP(_ *tunDevice, _ *udpMappings, _ chan<- []byte, done <-chan struct{}) {
	<-done
}

// writeTunUDP is a no-op on non-Linux platforms.
func writeTunUDP(_ *tunDevice, _ [4]byte, _ uint16, _ [4]byte, _ uint16, _ []byte) {}
