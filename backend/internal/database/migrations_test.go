package database

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	md "holdings/backend/internal/marketdata"
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
	if version, err := provider.GetDBVersion(ctx); err != nil || version != 19 {
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

func TestDedupePriceQuotesMigration(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Dedupe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, "alice", p.ID, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2026-11-01", Lines: []TransactionLine{
		{InstrumentID: 1, Amount: "18", Price: "9", Quantity: "2"}, {InstrumentID: 2, Amount: "20", Price: "10", Quantity: "2"}}}); err != nil {
		t.Fatal(err)
	}
	provider, err := s.migrationProvider()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(ctx, 18); err != nil {
		t.Fatal(err)
	}
	insert := func(inst int64, date, fetched string, price int64, prov string, fx, fxSource, qtime, prev any) int64 {
		t.Helper()
		res, err := s.db.Exec(`INSERT INTO price_quotes(instrument_id,price_eur_e4,orig_price_e4,orig_currency,fx_rate_e8,fx_rate_date,quote_date,fetched_at,provider,quote_time,fx_source,prev_close_e4,prev_fx_rate_e8,prev_close_date) VALUES(?,?,?,'USD',?,?,?,?,?,?,?,?,?,?)`,
			inst, price, price, fx, "2026-11-02", date, fetched, prov, qtime, fxSource, prev, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	const y, e = "YAHOO", "EULERPOOL"
	var gone, kept []int64
	add := func(list *[]int64, id int64) { *list = append(*list, id) }
	// Instrument 1, quote date 11-03: A A A (older two go), B, A again (A,B,A is kept).
	add(&gone, insert(1, "2026-11-03", "2026-11-03T10:00:00Z", 90000, y, 90000000, y, "2026-11-03T09:00:00Z", 80000))
	add(&gone, insert(1, "2026-11-03", "2026-11-03T11:00:00Z", 90000, y, 90000000, y, "2026-11-03T09:00:00Z", 80000))
	add(&kept, insert(1, "2026-11-03", "2026-11-03T12:00:00Z", 90000, y, 90000000, y, "2026-11-03T09:00:00Z", 80000))
	add(&kept, insert(1, "2026-11-03", "2026-11-03T13:00:00Z", 91000, y, 90000000, y, "2026-11-03T09:00:00Z", 80000))
	add(&kept, insert(1, "2026-11-03", "2026-11-03T14:00:00Z", 90000, y, 90000000, y, "2026-11-03T09:00:00Z", 80000))
	// Same price but a different FX rate, FX source (NULL vs value) or provider: all kept.
	add(&kept, insert(1, "2026-11-03", "2026-11-03T15:00:00Z", 90000, y, 89000000, y, "2026-11-03T09:00:00Z", 80000))
	add(&kept, insert(1, "2026-11-03", "2026-11-03T16:00:00Z", 90000, y, 89000000, nil, "2026-11-03T09:00:00Z", 80000))
	add(&kept, insert(1, "2026-11-03", "2026-11-03T17:00:00Z", 90000, e, 89000000, nil, "2026-11-03T09:00:00Z", 80000))
	// Quote date 11-04: a run of three rows full of NULLs; the newest fetch stays.
	add(&gone, insert(1, "2026-11-04", "2026-11-04T10:00:00Z", 50000, e, nil, nil, nil, nil))
	add(&gone, insert(1, "2026-11-04", "2026-11-04T11:00:00Z", 50000, e, nil, nil, nil, nil))
	add(&kept, insert(1, "2026-11-04", "2026-11-04T12:00:00Z", 50000, e, nil, nil, nil, nil))
	// Instrument 2 is partitioned separately, even with values equal to instrument 1's.
	add(&gone, insert(2, "2026-11-03", "2026-11-03T10:00:00Z", 90000, y, 90000000, y, "2026-11-03T09:00:00Z", 80000))
	add(&kept, insert(2, "2026-11-03", "2026-11-03T11:00:00Z", 90000, y, 90000000, y, "2026-11-03T09:00:00Z", 80000))
	// Instrument 3 has one row.
	add(&kept, insert(3, "2026-11-03", "2026-11-03T10:00:00Z", 12300, y, nil, nil, nil, nil))

	snapshot := func() (map[int64]*md.Quote, Summaries) {
		t.Helper()
		latest, err := s.latestQuotes(ctx, []int64{1, 2, 3})
		if err != nil {
			t.Fatal(err)
		}
		sums, err := s.Summaries(ctx, "alice")
		if err != nil {
			t.Fatal(err)
		}
		return latest, sums
	}
	// Fetch times are compared as stored, so the newest row must be the very same row.
	latestBefore, sumsBefore := snapshot()
	if applied, err := provider.Up(ctx); err != nil || len(applied) != 1 || applied[0].Source.Version != 19 {
		t.Fatalf("applied=%v err=%v", applied, err)
	}
	rows, err := s.db.Query("SELECT id FROM price_quotes ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	have := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		have[id] = true
	}
	if len(have) != len(kept) {
		t.Fatalf("rows left %d, want %d", len(have), len(kept))
	}
	for _, id := range gone {
		if have[id] {
			t.Fatalf("duplicate %d survived", id)
		}
	}
	for _, id := range kept {
		if !have[id] {
			t.Fatalf("row %d was deleted", id)
		}
	}
	latestAfter, sumsAfter := snapshot()
	if !reflect.DeepEqual(latestBefore, latestAfter) || !reflect.DeepEqual(sumsBefore, sumsAfter) {
		t.Fatalf("latest quote or summaries changed:\n%+v\n%+v\n%+v\n%+v", latestBefore, latestAfter, sumsBefore, sumsAfter)
	}
	// Idempotent, and the Down is a documented no-op that leaves the data alone.
	if _, err := provider.DownTo(ctx, 18); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM price_quotes").Scan(&n); err != nil || n != len(kept) {
		t.Fatalf("down changed rows: %d %v", n, err)
	}
}
