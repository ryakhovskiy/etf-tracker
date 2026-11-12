package database

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

func TestTransactionPerformanceQuotesCoverageAndPagination(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Performance")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddItems(ctx, "alice", p.ID, []int64{1, 2, 3, 4, 5}); err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2025-01-03", Lines: []TransactionLine{
		{InstrumentID: 1, Price: "10", Quantity: "2", Amount: "20"},
		{InstrumentID: 2, Price: "20", Quantity: "1", Amount: "20"},
		{InstrumentID: 3, Price: "5", Quantity: "3", Amount: "15"},
		{InstrumentID: 4, Price: "10", Quantity: "1", Amount: "10"},
		{InstrumentID: 5, Price: "10", Quantity: "1", Amount: "10"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.Transactions(ctx, "alice", p.ID, 1, 50)
	if err != nil || before.Groups[first].TransactionChange != nil || before.Groups[first].PricedLines != 0 || before.Groups[first].TotalLines != 5 {
		t.Fatal(before, err)
	}
	for _, line := range before.Lines {
		if line.Performance != nil {
			t.Fatal("purchase prices must not stand in for missing quotes", line)
		}
	}
	// A newer purchase must not hide the quote for an earlier purchase of the same item.
	second, err := s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2025-01-05", Lines: []TransactionLine{{InstrumentID: 1, Price: "20", Quantity: "1", Amount: "20"}}})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC)
	for i, q := range []marketdata.Quote{
		{InstrumentID: 1, Price: "11.0000", OrigPrice: "11.0000", Currency: "EUR", QuoteDate: "2025-01-04"},
		{InstrumentID: 1, Price: "12.0000", OrigPrice: "24.0000", Currency: "USD", FXRate: "0.50000000", FXRateDate: "2025-01-03", QuoteDate: "2025-01-04"},
		{InstrumentID: 1, Price: "99.0000", OrigPrice: "99.0000", Currency: "EUR", QuoteDate: "2025-01-03"},
		{InstrumentID: 2, Price: "17.0000", OrigPrice: "17.0000", Currency: "EUR", QuoteDate: "2025-01-04"},
		{InstrumentID: 3, Price: "5.0000", OrigPrice: "5.0000", Currency: "EUR", QuoteDate: "2025-01-03"},
		{InstrumentID: 5, Price: "20.0000", OrigPrice: "20.0000", Currency: "EUR", QuoteDate: "2025-01-02"},
	} {
		q.FetchedAt = at.Add(time.Duration(i) * time.Minute)
		if err := s.SaveQuote(ctx, &q, ""); err != nil {
			t.Fatal(err)
		}
	}
	full, err := s.Transactions(ctx, "alice", p.ID, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	wantPct := "1.82"
	want := TransactionGroupPerformance{TransactionChange: &TransactionChange{Change: "1.00", ChangePct: &wantPct}, PricedLines: 3, TotalLines: 5}
	if !reflect.DeepEqual(full.Groups[first], want) || full.Groups[second].TransactionChange != nil || full.Lines[0].Performance != nil {
		t.Fatalf("unexpected group coverage: %+v", full)
	}
	for i, tc := range []struct{ current, delta, pct, change string }{
		{"12.0000", "2.0000", "20.00", "4.00"},
		{"17.0000", "-3.0000", "-15.00", "-3.00"},
		{"5.0000", "0.0000", "0.00", "0.00"},
	} {
		got := full.Lines[i+1].Performance
		if got == nil || got.CurrentPrice != tc.current || got.PriceChange != tc.delta || got.PriceChangePct == nil || *got.PriceChangePct != tc.pct || got.Change != tc.change {
			t.Fatalf("line %d: %+v", i, got)
		}
	}
	for _, line := range full.Lines[4:] {
		if line.Performance != nil {
			t.Fatal("missing and pre-purchase quotes should remain unavailable", line)
		}
	}
	for page := 1; page <= 3; page++ {
		part, err := s.Transactions(ctx, "alice", p.ID, page, 2)
		if err != nil || len(part.Lines) != 2 || !reflect.DeepEqual(part.Groups[first], want) {
			t.Fatalf("page %d lost the full transaction total: %+v, %v", page, part, err)
		}
	}
	if _, err := s.Transactions(ctx, "bob", p.ID, 1, 50); !errors.Is(err, ErrNotFound) {
		t.Fatal("transaction performance must remain user-scoped", err)
	}
}

func TestTransactionPerformanceRoundsAfterSummingAndKeepsPricePrecision(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Rounding")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddItems(ctx, "alice", p.ID, []int64{1}); err != nil {
		t.Fatal(err)
	}
	line := TransactionLine{InstrumentID: 1, Price: "1", Quantity: "0.5", Amount: "0.50"}
	id, err := s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2025-01-01", Lines: []TransactionLine{line, line}})
	if err != nil {
		t.Fatal(err)
	}
	q := &marketdata.Quote{InstrumentID: 1, Price: "1.0100", OrigPrice: "1.0100", Currency: "EUR", QuoteDate: "2025-01-02", FetchedAt: time.Now()}
	if err := s.SaveQuote(ctx, q, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.Transactions(ctx, "alice", p.ID, 1, 50)
	if err != nil || got.Groups[id].TransactionChange == nil || got.Groups[id].Change != "0.01" || got.Lines[0].Performance.Change != "0.01" {
		t.Fatal("totals must sum exact differences before rounding", got, err)
	}
	q.Price, q.OrigPrice, q.FetchedAt = "1.0001", "1.0001", time.Now().Add(time.Minute)
	if err := s.SaveQuote(ctx, q, ""); err != nil {
		t.Fatal(err)
	}
	got, err = s.Transactions(ctx, "alice", p.ID, 1, 50)
	if err != nil || got.Lines[0].Performance.PriceChange != "0.0001" || got.Groups[id].Change != "0.00" {
		t.Fatal("per-unit changes retain four decimal places", got, err)
	}
}
