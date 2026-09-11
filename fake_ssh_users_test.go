package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestGeneratedSSHPassword(t *testing.T) {
	p1, err := generateSSHPassword()
	if err != nil {
		t.Fatal(err)
	}
	p2, err := generateSSHPassword()
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != 24 || len(p2) != 24 {
		t.Fatalf("generated password lengths = %d, %d; want 24", len(p1), len(p2))
	}
	if p1 == p2 {
		t.Fatal("two generated passwords were identical")
	}
}

func TestSSHUserMenuOperationsPreservePasswordWhenEditingSettings(t *testing.T) {
	store := newSSHUserStore(filepath.Join(t.TempDir(), "users.json"))
	if err := store.upsert("alice", "initial-password", 7, 2); err != nil {
		t.Fatal(err)
	}
	before, ok := store.get("alice")
	if !ok {
		t.Fatal("user missing after create")
	}

	if err := store.updateSettings("alice", 30, 5); err != nil {
		t.Fatal(err)
	}
	after, ok := store.get("alice")
	if !ok {
		t.Fatal("user missing after settings update")
	}
	if after.PasswordHash != before.PasswordHash {
		t.Fatal("editing expiry/connection limit changed password hash")
	}
	if after.MaxConnections != 5 {
		t.Fatalf("max connections = %d; want 5", after.MaxConnections)
	}
	if after.ExpiresAt.Before(time.Now().UTC().Add(29 * 24 * time.Hour)) {
		t.Fatalf("expiry was not renewed: %v", after.ExpiresAt)
	}

	if err := store.setPassword("alice", "replacement-password"); err != nil {
		t.Fatal(err)
	}
	reset, ok := store.get("alice")
	if !ok {
		t.Fatal("user missing after password reset")
	}
	if reset.PasswordHash == after.PasswordHash {
		t.Fatal("password reset did not change password hash")
	}
	if reset.MaxConnections != after.MaxConnections || !reset.ExpiresAt.Equal(after.ExpiresAt) {
		t.Fatal("password reset changed account limits")
	}

	if err := store.delete("alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.get("alice"); ok {
		t.Fatal("user still present after delete")
	}
}
