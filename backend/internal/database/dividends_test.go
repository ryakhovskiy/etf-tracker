package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

func dividendPortfolio(t *testing.T, s *Store, user string, ids ...int64) int64 {
	t.Helper()
	p, err := s.SavePortfolio(context.Background(), user, 0, "Dividends "+user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(context.Background(), user, p.ID, ids); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func TestDividendStoreUpsertDeletesAndErrors(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	id := dividendPortfolio(t, s, "alice", 20, 7)
	if _, err := s.CreateTransaction(ctx, "alice", id, TransactionInput{Date: "2025-03-03", Lines: []TransactionLine{{InstrumentID: 20, Amount: "100.00", Price: "10.0000", Quantity: "10.000000"}}}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	first := &marketdata.DividendHistory{Currency: "EUR", Events: []marketdata.DividendEvent{{ExDate: "2025-07-15", AmountPerShare: "0.575119"}, {ExDate: "2026-01-15", AmountPerShare: "0.250000"}, {ExDate: "2026-04-15", AmountPerShare: "0.187044"}},
		CloseBefore: map[string]string{"2026-01-15": "34.7850"}, LatestClose: "39.9950", LatestCloseDate: "2026-10-30"}
	if err := s.SaveDividends(ctx, 20, first, "YAHOO", "2024-01-01", "2026-11-01", at); err != nil {
		t.Fatal(err)
	}
	snap, err := s.DividendSnapshot(ctx, "alice", id)
	if err != nil || len(snap.Events[20]) != 3 || snap.FirstTradeDate != "2025-03-03" || len(snap.Items) != 2 {
		t.Fatalf("%+v %v", snap, err)
	}
	st := snap.Sync[20]
	if st.SyncedFrom != "2024-01-01" || !st.LastSyncedAt.Equal(at) || st.LatestCloseE4 != 399950 || st.LatestCloseDate != "2026-10-30" || st.Currency != "EUR" || st.LastError != "" {
		t.Fatalf("%+v", st)
	}
	if e := snap.Events[20][1]; e.ExDate != "2026-01-15" || e.AmountE6 != 250000 || e.CloseBeforeE4 != 347850 || e.Currency != "EUR" {
		t.Fatalf("%+v", e)
	}

	// A later sync from 2025-09-01: the provider corrected 2026-01-15 and no
	// longer has 2026-04-15. 2025-07-15 lies outside the range and stays.
	second := &marketdata.DividendHistory{Currency: "EUR", Events: []marketdata.DividendEvent{{ExDate: "2026-01-15", AmountPerShare: "0.251473"}, {ExDate: "2026-10-15", AmountPerShare: "0.417165"}}, CloseBefore: map[string]string{}}
	if err = s.SaveDividends(ctx, 20, second, "YAHOO", "2025-09-01", "2026-11-07", at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.DividendSnapshot(ctx, "alice", id)
	got := snap.Events[20]
	if len(got) != 3 || got[0].ExDate != "2025-07-15" || got[1].AmountE6 != 251473 || got[2].ExDate != "2026-10-15" {
		t.Fatalf("%+v", got)
	}
	// The close before is kept when a response lacks it.
	if got[1].CloseBeforeE4 != 347850 || got[2].CloseBeforeE4 != 0 {
		t.Fatalf("%+v", got)
	}
	// synced_from keeps the earliest covered date; the latest close survives a response without closes.
	if st = snap.Sync[20]; st.SyncedFrom != "2024-01-01" || st.LatestCloseE4 != 399950 {
		t.Fatalf("%+v", st)
	}

	// A failure keeps the data and records the error.
	if err = s.RecordDividendError(ctx, 20, "2025-09-01", "Yahoo Finance is unavailable.", at.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.DividendSnapshot(ctx, "alice", id)
	if st = snap.Sync[20]; len(snap.Events[20]) != 3 || st.LastError != "Yahoo Finance is unavailable." || !st.LastErrorAt.Equal(at.Add(2*time.Hour)) || !st.LastSyncedAt.Equal(at.Add(time.Hour)) || st.SyncedFrom != "2024-01-01" {
		t.Fatalf("%+v", st)
	}
	// A failure before any success creates a row that is still stale.
	if err = s.RecordDividendError(ctx, 7, "2024-01-01", "Yahoo Finance has no listing for this instrument.", at); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.DividendSnapshot(ctx, "alice", id)
	if st = snap.Sync[7]; !st.LastSyncedAt.IsZero() || !snap.IsStale(7, at, "2026-11-01") || snap.IsStale(20, at.Add(2*time.Hour), "2026-11-01") {
		t.Fatalf("%+v", st)
	}
	// The first success replaces the placeholder range; success clears the error.
	if err = s.SaveDividends(ctx, 7, &marketdata.DividendHistory{Currency: "EUR", CloseBefore: map[string]string{}}, "YAHOO", "2024-06-01", "2026-11-07", at); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.DividendSyncState(ctx, 7); st == nil || st.SyncedFrom != "2024-06-01" || st.LastError != "" || st.LastErrorAt.IsZero() == false {
		t.Fatalf("%+v", st)
	}
	if st, _ := s.DividendSyncState(ctx, 1); st != nil {
		t.Fatal(st)
	}

	// Ownership: another user's portfolio does not exist.
	if _, err = s.DividendSnapshot(ctx, "bob", id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// Removed items are not shown: their data stays shared but leaves the snapshot.
	if err = s.RemoveItem(ctx, "alice", id, 7); err != nil {
		t.Fatal(err)
	}
	snap, _ = s.DividendSnapshot(ctx, "alice", id)
	if len(snap.Items) != 1 || snap.Sync[7].InstrumentID != 0 {
		t.Fatalf("%+v", snap)
	}
}

func TestRelinkingYahooSymbolDropsDividends(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	at := time.Now()
	h := &marketdata.DividendHistory{Currency: "USD", Events: []marketdata.DividendEvent{{ExDate: "2026-09-17", AmountPerShare: "0.519077"}}, CloseBefore: map[string]string{}}
	if err := s.LinkYahooSymbol(ctx, 18, "VHYD.L"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDividends(ctx, 18, h, "YAHOO", "2025-01-01", "2026-11-07", at); err != nil {
		t.Fatal(err)
	}
	// The same symbol again keeps the data.
	if err := s.LinkYahooSymbol(ctx, 18, "vhyd.l"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.DividendSyncState(ctx, 18); st == nil {
		t.Fatal("data dropped for an unchanged symbol")
	}
	// Another listing (other currency) drops events and sync state.
	if err := s.LinkYahooSymbol(ctx, 18, "VHYL.AS"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM dividend_events WHERE instrument_id=18").Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	if st, _ := s.DividendSyncState(ctx, 18); st != nil {
		t.Fatalf("%+v", st)
	}
}
