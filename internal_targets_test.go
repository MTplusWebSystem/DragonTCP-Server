package main

import "testing"

func TestSSHOnlyInternalTargetIsNotPublicDragonTCPTarget(t *testing.T) {
	const host = "test-udpgw.internal"
	const port = 17400
	registerSSHOnlyInternalTarget(host, port, "127.0.0.1:17400")

	if _, ok := lookupInternalTarget(host, port); ok {
		t.Fatal("SSH-only target leaked into raw DragonTCP internal target registry")
	}
	if got, ok := lookupSSHOnlyInternalTarget(host, port); !ok || got != "127.0.0.1:17400" {
		t.Fatalf("SSH-only target lookup = %q, %v", got, ok)
	}
}
