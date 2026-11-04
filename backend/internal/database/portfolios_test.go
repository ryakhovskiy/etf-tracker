package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func featureStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, id := range []string{"alice", "bob"} {
		if _, err := s.UpsertGoogleUser(context.Background(), id, id+"@example.com", id, ""); err != nil {
			t.Fatal(err)
		}
	}
	return s
}
func requireLimit(t *testing.T, err error, code string, current, limit int) {
	t.Helper()
	var e *LimitError
	if !errors.As(err, &e) || e.Code != code || e.Current != current || e.Limit != limit {
		t.Fatalf("unexpected limit error: %#v", err)
	}
}
func TestPortfolioOwnershipAndItems(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Growth")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavePortfolio(ctx, "alice", 0, "growth"); err != ErrNameTaken {
		t.Fatal(err)
	}
	if _, err = s.SavePortfolio(ctx, "bob", 0, "GROWTH"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Portfolio(ctx, "bob", p.ID); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err = s.SavePortfolio(ctx, "bob", p.ID, "stolen"); err != ErrNotFound {
		t.Fatal(err)
	}
	if err = s.DeletePortfolio(ctx, "bob", p.ID); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, "bob", p.ID, []int64{1}); err != ErrNotFound {
		t.Fatal(err)
	}
	if err = s.RemoveItem(ctx, "bob", p.ID, 1); err != ErrNotFound {
		t.Fatal(err)
	}
	result, err := s.AddItems(ctx, "alice", p.ID, []int64{1, 1, 2})
	if err != nil || len(result.Added) != 2 {
		t.Fatalf("%v %v", result, err)
	}
	result, err = s.AddItems(ctx, "alice", p.ID, []int64{1, 2})
	if err != nil || len(result.Added) != 0 || len(result.Skipped) != 2 {
		t.Fatalf("%v %v", result, err)
	}
	if _, err = s.AddItems(ctx, "alice", p.ID, []int64{3, 99999}); err != ErrInvalid {
		t.Fatal(err)
	}
	detail, err := s.Portfolio(ctx, "alice", p.ID)
	if err != nil || len(detail.InstrumentIDs) != 2 {
		t.Fatalf("%v %v", detail, err)
	}
	if err = s.RemoveItem(ctx, "alice", p.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveItem(ctx, "alice", p.ID, 2); err != ErrNotFound {
		t.Fatal(err)
	}
	if err = s.DeletePortfolio(ctx, "alice", p.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM portfolio_items WHERE portfolio_id=?", p.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade: %d %v", count, err)
	}
}
func TestItemLimitAtomic(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Growth")
	if err != nil {
		t.Fatal(err)
	}
	for i := 22; i <= 33; i++ {
		if _, err = s.db.Exec("INSERT INTO instruments(id,type,name,ticker,isin) VALUES (?,'STOCK',?,?,?)", i, fmt.Sprint("Stock ", i), fmt.Sprint("T", i), fmt.Sprintf("TEST%08d", i)); err != nil {
			t.Fatal(err)
		}
	}
	ids := []int64{}
	for i := int64(1); i <= 28; i++ {
		ids = append(ids, i)
	}
	if _, err = s.AddItems(ctx, "alice", p.ID, ids); err != nil {
		t.Fatal(err)
	}
	_, err = s.AddItems(ctx, "alice", p.ID, []int64{29, 30, 31, 32, 33})
	requireLimit(t, err, "ITEM_LIMIT_REACHED", 28, 30)
	detail, err := s.Portfolio(ctx, "alice", p.ID)
	if err != nil || len(detail.InstrumentIDs) != 28 {
		t.Fatalf("partial insert: %v %v", detail, err)
	}
	result, err := s.AddItems(ctx, "alice", p.ID, []int64{1, 2, 29, 30})
	if err != nil || len(result.Added) != 2 || len(result.Skipped) != 2 {
		t.Fatalf("existing counted: %v %v", result, err)
	}
}
func TestConcurrentLimitsAcrossConnections(t *testing.T) {
	t.Setenv("MAX_USERS", "2")
	t.Setenv("MAX_PORTFOLIOS_PER_USER", "2")
	t.Setenv("MAX_ITEMS_PER_PORTFOLIO", "1")
	path := filepath.Join(t.TempDir(), "limits.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	ctx := context.Background()
	if _, err = a.UpsertGoogleUser(ctx, "alice", "a@example.com", "", ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			_, err := s.SavePortfolio(ctx, "alice", 0, fmt.Sprint(i))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			requireLimit(t, err, "PORTFOLIO_LIMIT_REACHED", 2, 2)
		}
	}
	if successes != 2 {
		t.Fatalf("created %d", successes)
	}
	ps, err := a.Portfolios(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	errs = make(chan error, 10)
	for i := int64(1); i <= 10; i++ {
		wg.Add(1)
		go func(i int64) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			_, err := s.AddItems(ctx, "alice", ps[0].ID, []int64{i})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	successes = 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			requireLimit(t, err, "ITEM_LIMIT_REACHED", 1, 1)
		}
	}
	if successes != 1 {
		t.Fatalf("added %d", successes)
	}
	errs = make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := a
			if i%2 == 0 {
				s = b
			}
			_, err := s.UpsertGoogleUser(ctx, fmt.Sprint("new", i), "new@example.com", "", "")
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	successes = 0
	for err := range errs {
		if err == nil {
			successes++
		} else {
			requireLimit(t, err, "USER_LIMIT_REACHED", 2, 2)
		}
	}
	if successes != 1 {
		t.Fatalf("signed up %d", successes)
	}
	if _, err = a.UpsertGoogleUser(ctx, "alice", "updated@example.com", "", ""); err != nil {
		t.Fatal("existing login blocked", err)
	}
}
func TestDefaultLimits(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		if _, err := s.SavePortfolio(ctx, "alice", 0, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.SavePortfolio(ctx, "alice", 0, "eleventh")
	requireLimit(t, err, "PORTFOLIO_LIMIT_REACHED", 10, 10)
	for i := 0; i < 8; i++ {
		if _, err := s.UpsertGoogleUser(ctx, fmt.Sprint(i), "new@example.com", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.UpsertGoogleUser(ctx, "eleventh", "new@example.com", "", "")
	requireLimit(t, err, "USER_LIMIT_REACHED", 10, 10)
}
func TestLimitConfiguration(t *testing.T) {
	for _, key := range []string{"MAX_USERS", "MAX_PORTFOLIOS_PER_USER", "MAX_ITEMS_PER_PORTFOLIO"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "invalid")
			if s, err := Open(":memory:"); err == nil {
				s.Close()
				t.Fatal("accepted invalid limit")
			}
		})
	}
}
func TestInstrumentSearch(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Growth")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, "alice", p.ID, []int64{1}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"aapl", "US0378331005", "865985", "apple"} {
		rows, err := s.Search(ctx, "alice", q, p.ID)
		if err != nil || len(rows) != 1 || rows[0].ID != 1 || !rows[0].InPortfolio {
			t.Fatalf("%s: %v %v", q, rows, err)
		}
	}
	if _, err = s.Search(ctx, "bob", "apple", p.ID); err != ErrNotFound {
		t.Fatal(err)
	}
	for _, q := range []string{"%%%%", "____", `\\\\`} {
		rows, err := s.Search(ctx, "alice", q, 0)
		if err != nil || len(rows) != 0 {
			t.Fatalf("wildcards %s: %v %v", q, rows, err)
		}
	}
	for i := 0; i < 14; i++ {
		ticker := "OTHER"
		name := fmt.Sprintf("A contains RANK %02d", i)
		if i == 12 {
			ticker = "RANKplus"
			name = "Z prefix"
		}
		if i == 13 {
			ticker = "RANK"
			name = "Z exact"
		}
		if _, err = s.db.Exec("INSERT INTO instruments(type,name,ticker,isin) VALUES('STOCK',?,?,?)", name, ticker, fmt.Sprintf("TEST%08d", i)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Search(ctx, "alice", "rank", 0)
	if err != nil || len(rows) != 10 || rows[0].Name != "Z exact" || rows[1].Name != "Z prefix" || rows[2].Name != "A contains RANK 00" {
		t.Fatalf("ranking: %v %v", rows, err)
	}
	if _, err = s.db.Exec(`INSERT INTO instruments(type,name,ticker,isin) VALUES('STOCK','Literal %_\ name','WILD','TEST00000099')`); err != nil {
		t.Fatal(err)
	}
	rows, err = s.Search(ctx, "alice", `%_\ name`, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("literal escape: %v %v", rows, err)
	}
}

func TestForeignKeysOnEveryConnection(t *testing.T) {
	s := featureStore(t)
	s.db.SetMaxOpenConns(2)
	ctx := context.Background()
	first, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, conn := range []*sql.Conn{first, second} {
		var enabled int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
			t.Fatalf("foreign keys: %d %v", enabled, err)
		}
	}
}
