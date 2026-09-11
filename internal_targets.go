package main

import (
	"net"
	"strconv"
	"strings"
	"sync"
)

type internalTargetRegistry struct {
	sync.RWMutex
	m map[string]string
}

var dragonTCPInternalTargets = internalTargetRegistry{m: make(map[string]string)}
var sshOnlyInternalTargets = internalTargetRegistry{m: make(map[string]string)}

func internalTargetKey(host string, port int) string {
	return strings.ToLower(strings.TrimSpace(host)) + ":" + strconv.Itoa(port)
}

func registerInternalTarget(host string, port int, dialAddr string) {
	registerTarget(&dragonTCPInternalTargets, host, port, dialAddr)
}

func lookupInternalTarget(host string, port int) (string, bool) {
	return lookupTarget(&dragonTCPInternalTargets, host, port)
}

// registerSSHOnlyInternalTarget creates a destination that is reachable only
// after SSH authentication. It is deliberately not exposed to raw DragonTCP
// clients, which prevents direct access to services such as the UDP gateway.
func registerSSHOnlyInternalTarget(host string, port int, dialAddr string) {
	registerTarget(&sshOnlyInternalTargets, host, port, dialAddr)
}

func lookupSSHOnlyInternalTarget(host string, port int) (string, bool) {
	return lookupTarget(&sshOnlyInternalTargets, host, port)
}

func registerTarget(registry *internalTargetRegistry, host string, port int, dialAddr string) {
	if strings.TrimSpace(host) == "" || port < 1 || port > 65535 || strings.TrimSpace(dialAddr) == "" {
		return
	}
	registry.Lock()
	registry.m[internalTargetKey(host, port)] = dialAddr
	registry.Unlock()
}

func lookupTarget(registry *internalTargetRegistry, host string, port int) (string, bool) {
	registry.RLock()
	addr, ok := registry.m[internalTargetKey(host, port)]
	registry.RUnlock()
	return addr, ok
}

func dialInternalTarget(dialer *net.Dialer, network, addr string) (net.Conn, error) {
	return dialer.Dial(network, addr)
}
