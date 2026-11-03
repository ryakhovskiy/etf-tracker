package database

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAuditRefreshConcurrencyExpiryRollback(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	user, err := store.UpsertGoogleUser(ctx, "user", "user@example.com", "User", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRefreshToken(ctx, "initial", user.ID, HashToken("initial"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for _, id := range []string{"a", "b", "c", "d"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if _, err := store.RotateRefreshToken(ctx, HashToken("initial"), id, HashToken(id), time.Now().Add(time.Hour)); err == nil {
				successes.Add(1)
			}
		}(id)
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent successes=%d", successes.Load())
	}
	if err := store.CreateRefreshToken(ctx, "expired", user.ID, HashToken("expired"), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateRefreshToken(ctx, HashToken("expired"), "expired-new", HashToken("expired-new"), time.Now().Add(time.Hour)); err != ErrNotFound {
		t.Fatalf("expired refresh error=%v", err)
	}
	if err := store.CreateRefreshToken(ctx, "rollback", user.ID, HashToken("rollback"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RotateRefreshToken(ctx, HashToken("rollback"), "expired", HashToken("collision"), time.Now().Add(time.Hour)); err == nil {
		t.Fatal("duplicate insert succeeded")
	}
	if _, err := store.RotateRefreshToken(ctx, HashToken("rollback"), "retry", HashToken("retry"), time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("failed rotation consumed original: %v", err)
	}
	var rawCount int
	if err := store.db.QueryRow("SELECT count(*) FROM refresh_tokens WHERE token_hash IN ('initial','expired','rollback','retry')").Scan(&rawCount); err != nil || rawCount != 0 {
		t.Fatal("raw tokens persisted")
	}
}

func TestAuditDatabaseSidecarPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s mode=%o", filepath.Base(path+suffix), info.Mode().Perm())
		if info.Mode().Perm()&0077 != 0 {
			t.Errorf("SQLite file %s is accessible to group/other users", suffix)
		}
	}
}
