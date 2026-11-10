package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLegacyDatabaseMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	initial, err := migrations.ReadFile("migrations/00001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Seed the pre-Goose schema without creating a migration version table.
	if _, err = db.ExecContext(ctx, strings.Split(string(initial), "-- +goose Down")[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO users VALUES ('user','google-user','user@example.com','User','',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO refresh_tokens VALUES ('legacy','user',?, ?,1,NULL)`, HashToken("legacy-secret"), time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if user, err := store.UserByID(ctx, "user"); err != nil || user.Email != "user@example.com" {
		t.Fatalf("legacy user lost: %v %v", user, err)
	}
	if _, err := store.RotateRefreshToken(ctx, HashToken("legacy-secret"), "new", HashToken("new"), time.Now().Add(time.Hour)); err != ErrNotFound {
		t.Fatalf("legacy token accepted: %v", err)
	}
	provider, err := store.migrationProvider()
	if err != nil {
		t.Fatal(err)
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 14 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if applied, err := provider.Up(ctx); err != nil || len(applied) != 0 {
		t.Fatalf("migrations reapplied: %v %v", applied, err)
	}
	if err := store.CreateRefreshToken(ctx, "fresh", "user", HashToken("fresh"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.CheckSession(ctx, "fresh", "user"); err != nil {
		t.Fatalf("reopening lost session: %v", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("unsafe migrated file %s: %v %v", suffix, info, err)
		}
	}
}

func TestMigrationDownAndUp(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	provider, err := store.migrationProvider()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 1 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertGoogleUser(ctx, "fresh-user", "fresh@example.com", "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestSessionExpiryAndSubjectBinding(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	user, err := store.UpsertGoogleUser(ctx, "user", "user@example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	if err := store.CreateRefreshToken(ctx, "initial", user.ID, HashToken("initial"), expires); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckSession(ctx, "initial", "other-user"); err != ErrNotFound {
		t.Fatal("session accepted for another user")
	}
	if _, err := store.RotateRefreshToken(ctx, HashToken("initial"), "next", HashToken("next"), time.Now().Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var actual int64
	if err := store.db.QueryRow(`SELECT expires_at FROM refresh_tokens WHERE id='next'`).Scan(&actual); err != nil || actual != expires.Unix() {
		t.Fatalf("rotation extended absolute session expiry: %d %v", actual, err)
	}
	if _, err := store.db.Exec(`UPDATE sessions SET expires_at=?`, time.Now().Add(-time.Second).Unix()); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckSession(ctx, "next", user.ID); err != ErrNotFound {
		t.Fatal("expired session accepted")
	}
}
