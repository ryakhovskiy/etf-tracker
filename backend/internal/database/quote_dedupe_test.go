package database

import (
	"context"
	"errors"
	"testing"
	"time"

	md "holdings/backend/internal/marketdata"
)

func baseQuote(at time.Time) md.Quote {
	return md.Quote{InstrumentID: 1, Price: "9.0000", OrigPrice: "10.0000", Currency: "USD", FXRate: "0.90000000", FXRateDate: "2026-11-02", FXSource: md.FXSourceYahoo,
		QuoteDate: "2026-11-03", QuoteTime: time.Date(2026, 11, 3, 15, 0, 0, 0, time.UTC), FetchedAt: at, Provider: md.ProviderYahoo,
		PrevClose: "8.0000", PrevCloseDate: "2026-11-02", PrevFXRate: "0.91000000"}
}

func quoteRows(t *testing.T, s *Store, id int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM price_quotes WHERE instrument_id=?", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSaveQuoteSkipsIdenticalQuoteAndBumpsFetchTime(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	first := baseQuote(t0)
	if err := s.SaveQuote(ctx, &first, ""); err != nil {
		t.Fatal(err)
	}
	second := baseQuote(t0.Add(20 * time.Minute))
	if err := s.SaveQuote(ctx, &second, ""); err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || quoteRows(t, s, 1) != 1 {
		t.Fatalf("ids %d/%d rows %d", first.ID, second.ID, quoteRows(t, s, 1))
	}
	got, err := s.latestQuotes(ctx, []int64{1})
	if err != nil || !got[1].FetchedAt.Equal(t0.Add(20*time.Minute)) {
		t.Fatalf("fetched_at not bumped: %+v %v", got[1], err)
	}
	if got[1].FXSource != md.FXSourceYahoo || got[1].PrevClose != "8.0000" || got[1].PrevFXRate != "0.91000000" || got[1].QuoteTime.IsZero() {
		t.Fatalf("stored fields changed: %+v", got[1])
	}
	// Another instrument is never compared.
	other := baseQuote(t0)
	other.InstrumentID = 2
	if err := s.SaveQuote(ctx, &other, ""); err != nil || other.ID == first.ID || quoteRows(t, s, 2) != 1 {
		t.Fatalf("%+v %v", other, err)
	}
}

func TestSaveQuoteStoresAnyDifferingField(t *testing.T) {
	t0 := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	for name, change := range map[string]func(*md.Quote){
		"price":         func(q *md.Quote) { q.Price = "9.0100"; q.OrigPrice = "10.0111" },
		"quote time":    func(q *md.Quote) { q.QuoteTime = q.QuoteTime.Add(time.Minute) },
		"quote date":    func(q *md.Quote) { q.QuoteDate = "2026-11-04" },
		"previous":      func(q *md.Quote) { q.PrevClose = "8.0100" },
		"previous date": func(q *md.Quote) { q.PrevCloseDate = "2026-11-01" },
		"previous fx":   func(q *md.Quote) { q.PrevFXRate = "0.92000000" },
		"live fx rate":  func(q *md.Quote) { q.FXRate = "0.89000000"; q.Price = "8.9000" },
		"fx rate only":  func(q *md.Quote) { q.FXRate = "0.89000000" },
		"fx date":       func(q *md.Quote) { q.FXRateDate = "2026-11-03" },
		"fx source":     func(q *md.Quote) { q.FXSource = "" },
		"provider":      func(q *md.Quote) { q.Provider = md.ProviderEulerpool },
		"currency":      func(q *md.Quote) { q.Currency = "GBP" },
	} {
		t.Run(name, func(t *testing.T) {
			s := featureStore(t)
			ctx := context.Background()
			first := baseQuote(t0)
			if err := s.SaveQuote(ctx, &first, ""); err != nil {
				t.Fatal(err)
			}
			next := baseQuote(t0.Add(time.Minute))
			change(&next)
			if err := s.SaveQuote(ctx, &next, ""); err != nil {
				t.Fatal(err)
			}
			if next.ID == first.ID || quoteRows(t, s, 1) != 2 {
				t.Fatalf("not stored: %d/%d rows %d", first.ID, next.ID, quoteRows(t, s, 1))
			}
			old, err := scanQuote(s.db.QueryRow("SELECT "+quoteColumns+" FROM price_quotes WHERE id=?", first.ID))
			if err != nil || old == nil || !old.FetchedAt.Equal(t0) {
				t.Fatalf("old row touched: %+v %v", old, err)
			}
		})
	}
}

func TestSaveQuoteComparesOnlyTheNewestRow(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	a := baseQuote(t0)
	b := baseQuote(t0.Add(time.Minute))
	b.Provider = md.ProviderEulerpool
	b.Price, b.OrigPrice = "9.5000", "9.5000"
	a2 := baseQuote(t0.Add(2 * time.Minute))
	for _, q := range []*md.Quote{&a, &b, &a2} {
		if err := s.SaveQuote(ctx, q, ""); err != nil {
			t.Fatal(err)
		}
	}
	if a.ID == b.ID || a2.ID == a.ID || a2.ID == b.ID || quoteRows(t, s, 1) != 3 {
		t.Fatalf("A,B,A must keep three rows: %d %d %d rows %d", a.ID, b.ID, a2.ID, quoteRows(t, s, 1))
	}
}

func TestSaveQuoteDedupesNullFields(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 3, 16, 0, 0, 0, time.UTC)
	// A daily EUR quote: no FX, no quote time, no previous close.
	mk := func(at time.Time) *md.Quote {
		return &md.Quote{InstrumentID: 3, Price: "50.0000", OrigPrice: "50.0000", Currency: "EUR", QuoteDate: "2026-11-03", FetchedAt: at, Provider: md.ProviderEulerpool}
	}
	first, second := mk(t0), mk(t0.Add(time.Hour))
	for _, q := range []*md.Quote{first, second} {
		if err := s.SaveQuote(ctx, q, ""); err != nil {
			t.Fatal(err)
		}
	}
	if second.ID != first.ID || quoteRows(t, s, 3) != 1 {
		t.Fatalf("NULL fields did not dedupe: %d/%d rows %d", first.ID, second.ID, quoteRows(t, s, 3))
	}
	// NULL vs a value is a difference.
	withPrev := mk(t0.Add(2 * time.Hour))
	withPrev.PrevClose, withPrev.PrevCloseDate = "49.0000", "2026-11-02"
	if err := s.SaveQuote(ctx, withPrev, ""); err != nil || withPrev.ID == first.ID || quoteRows(t, s, 3) != 2 {
		t.Fatalf("%+v %v", withPrev, err)
	}
}

func TestCreateTransactionRejectsMissingQuoteID(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Quotes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, "alice", p.ID, []int64{1}); err != nil {
		t.Fatal(err)
	}
	q := baseQuote(time.Now())
	if err = s.SaveQuote(ctx, &q, ""); err != nil {
		t.Fatal(err)
	}
	line := func(id int64) TransactionLine {
		return TransactionLine{InstrumentID: 1, Amount: "18", Price: "9", Quantity: "2", QuoteID: id}
	}
	// A valid first line must be rolled back with the failing second one.
	_, err = s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2026-11-01", Lines: []TransactionLine{line(q.ID), line(q.ID + 1000)}})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Fields["lines.1.quoteId"] == "" {
		t.Fatalf("expected quoteId field error, got %v", err)
	}
	var n int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial write: %d %v", n, err)
	}
	// No quote id, and a quote of another instrument, keep their behaviour.
	if _, err = s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2026-11-01", Lines: []TransactionLine{line(0)}}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2026-11-02", Lines: []TransactionLine{line(q.ID)}}); err != nil {
		t.Fatal(err)
	}
}
