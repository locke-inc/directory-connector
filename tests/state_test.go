package tests

import (
	"os"
	"testing"
	"time"

	"github.com/locke-inc/directory-connector/internal/state"
)

func TestStateStore(t *testing.T) {
	dbPath := t.TempDir() + "/test.db"
	defer os.Remove(dbPath)

	store, err := state.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()

	t.Run("upsert and get user", func(t *testing.T) {
		user := &state.SyncedUser{
			ObjectGUID: "test-guid-123",
			Username:   "jsmith",
			Email:      "jsmith@acme.com",
			FirstName:  "John",
			LastName:   "Smith",
			Disabled:   false,
			MemberOf:   `["CN=Engineering,DC=acme,DC=local"]`,
		}

		if err := store.UpsertUser(user); err != nil {
			t.Fatalf("upsert failed: %v", err)
		}

		got, err := store.GetUser("test-guid-123")
		if err != nil {
			t.Fatalf("get failed: %v", err)
		}
		if got == nil {
			t.Fatal("expected user, got nil")
		}
		if got.Username != "jsmith" {
			t.Errorf("username = %q, want %q", got.Username, "jsmith")
		}
		if got.Email != "jsmith@acme.com" {
			t.Errorf("email = %q, want %q", got.Email, "jsmith@acme.com")
		}
	})

	t.Run("get non-existent user returns nil", func(t *testing.T) {
		got, err := store.GetUser("non-existent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatal("expected nil, got user")
		}
	})

	t.Run("delete user", func(t *testing.T) {
		user := &state.SyncedUser{
			ObjectGUID: "delete-me",
			Username:   "deleteme",
			Email:      "del@acme.com",
		}
		store.UpsertUser(user)
		store.DeleteUser("delete-me")

		got, _ := store.GetUser("delete-me")
		if got != nil {
			t.Fatal("user should have been deleted")
		}
	})

	t.Run("high water mark", func(t *testing.T) {
		if err := store.SetHighWaterMark(12345); err != nil {
			t.Fatalf("set HWM failed: %v", err)
		}

		hwm, err := store.GetHighWaterMark()
		if err != nil {
			t.Fatalf("get HWM failed: %v", err)
		}
		if hwm != 12345 {
			t.Errorf("HWM = %d, want %d", hwm, 12345)
		}
	})

	t.Run("sync info", func(t *testing.T) {
		now := time.Now().Truncate(time.Second)
		store.SetLastSync(now)
		store.SetLastFullSync(now)
		store.SetLastError("")

		info, err := store.GetSyncInfo()
		if err != nil {
			t.Fatalf("get sync info failed: %v", err)
		}
		if info.UserCount < 1 {
			t.Error("expected at least 1 user in state")
		}
		if info.HighWaterMark != 12345 {
			t.Errorf("HWM in info = %d, want %d", info.HighWaterMark, 12345)
		}
	})

	t.Run("member of JSON helpers", func(t *testing.T) {
		groups := []string{"CN=Engineering,DC=acme,DC=local", "CN=VPN Users,DC=acme,DC=local"}
		j := state.MemberOfJSON(groups)
		parsed := state.ParseMemberOf(j)
		if len(parsed) != 2 {
			t.Fatalf("expected 2 groups, got %d", len(parsed))
		}
		if parsed[0] != groups[0] || parsed[1] != groups[1] {
			t.Error("round-trip mismatch")
		}
	})
}
