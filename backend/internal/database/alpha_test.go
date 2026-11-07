package database

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	md "holdings/backend/internal/marketdata"
)

func TestQuoteCommitDefaultsAndUnlink(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Quotes")
	if err != nil {
		t.Fatal(err)
	}
	s.AddItems(ctx, "alice", p.ID, []int64{1, 2})
	if err = s.LinkSymbol(ctx, "alice", 1, "AAPL", "USD", "US"); err != nil {
		t.Fatal(err)
	}
	q := &md.Quote{InstrumentID: 1, Price: "9.0000", OrigPrice: "10.0000", Currency: "USD", FXRate: "0.90000000", FXRateDate: "2026-10-30", QuoteDate: "2026-11-02", FetchedAt: time.Now()}
	if err = s.SaveQuote(ctx, q, "AAPL"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2026-11-01", Lines: []TransactionLine{{InstrumentID: 1, Amount: "18", Price: "9", Quantity: "2", QuoteID: q.ID}, {InstrumentID: 1, Amount: "20", Price: "10", Quantity: "2", QuoteID: q.ID}, {InstrumentID: 2, Amount: "18", Price: "9", Quantity: "2", QuoteID: q.ID}}}); err != nil {
		t.Fatal(err)
	}
	lines, err := s.Transactions(ctx, "alice", p.ID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if lines.Lines[0].OrigPrice != "10.0000" || lines.Lines[0].FXRate != "0.90000000" || lines.Lines[0].FXRateDate != "2026-10-30" {
		t.Fatal(lines.Lines)
	}
	for _, l := range lines.Lines[1:] {
		if l.OrigPrice != "" || l.FXRate != "" {
			t.Fatal("edited/foreign quote copied", l)
		}
	}
	plan, err := s.SavingPlan(ctx, "alice", p.ID)
	if err != nil || plan[0].QuoteID != q.ID || plan[0].DefaultPrice != "9.0000" || !strings.Contains(plan[0].PriceSource, "ECB 30.10.2026") {
		t.Fatalf("%+v %v", plan, err)
	}
	if err = s.LinkSymbol(ctx, "bob", 1, "OTHER", "EUR", ""); !errors.Is(err, ErrAlreadyLinked) {
		t.Fatal(err)
	}
	if err = s.LinkSymbol(ctx, "bob", 2, "AAPL", "USD", ""); !errors.Is(err, ErrSymbolInUse) {
		t.Fatal(err)
	}
	if err = s.UnlinkSymbol(ctx, 1); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.FreshQuote(ctx, 1, time.Now())
	if err != nil || fresh != nil {
		t.Fatal("unlink retained quotes", fresh, err)
	}
	lines, err = s.Transactions(ctx, "alice", p.ID, 1, 50)
	if err != nil || lines.Lines[0].FXRateDate != "2026-10-30" {
		t.Fatal("unlink changed committed provenance", lines, err)
	}
}
func TestAVLedgerPersistsCleanupAndQuoteDay(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "persistent.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpsertGoogleUser(ctx, "alice", "alice@example.com", "", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	limits := md.AVLimits{}.Defaults()
	if err = s.ReserveAV(ctx, "alice", "GLOBAL_QUOTE", now, limits); err != nil {
		t.Fatal(err)
	}
	if err = s.BlockAV(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := s.AVBudget(ctx, "alice", now, limits)
	if err != nil || b.UserRemaining != 9 || b.UserLimit != limits.User || b.BlockedUntil == nil {
		t.Fatal(b, err)
	}
	if err = s.CleanupAV(ctx, now.Add(49*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM provider_requests").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err = s.LinkSymbol(ctx, "alice", 1, "S1", "EUR", ""); err != nil {
		t.Fatal(err)
	}
	fetched := time.Date(2026, 11, 5, 22, 59, 0, 0, time.UTC)
	q := &md.Quote{InstrumentID: 1, Price: "10.0000", OrigPrice: "10.0000", Currency: "EUR", QuoteDate: "2026-11-05", FetchedAt: fetched}
	if err = s.SaveQuote(ctx, q, "S1"); err != nil {
		t.Fatal(err)
	}
	if q, e := s.FreshQuote(ctx, 1, fetched.Add(30*time.Second)); e != nil || q == nil {
		t.Fatal(q, e)
	}
	if q, e := s.FreshQuote(ctx, 1, fetched.Add(2*time.Minute)); e != nil || q != nil {
		t.Fatal("Berlin day crossed", q, e)
	}
}
