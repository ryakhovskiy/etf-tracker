package database

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

func TestPerformanceInstrumentSelectionUsesCompleteHistoryAndExactTotals(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Selection")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddItems(ctx, "alice", p.ID, []int64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	for _, in := range []TransactionInput{
		{Date: "2025-01-01", Lines: []TransactionLine{
			{InstrumentID: 1, Amount: "0.50", Price: "1", Quantity: "0.5"},
			{InstrumentID: 2, Amount: "0.50", Price: "1", Quantity: "0.5"},
			{InstrumentID: 3, Amount: "20", Price: "10", Quantity: "2"},
		}},
		{Date: "2025-01-02", Lines: []TransactionLine{{InstrumentID: 1, Amount: "1", Price: "2", Quantity: "0.5"}}},
	} {
		if _, err := s.CreateTransaction(ctx, "alice", p.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2025, 1, 6, 12, 0, 0, 0, time.UTC)
	for i, q := range []marketdata.Quote{
		{InstrumentID: 1, Price: "2.0050", OrigPrice: "2.0050", Currency: "EUR", QuoteDate: "2025-01-03"},
		{InstrumentID: 2, Price: "1.0100", OrigPrice: "1.0100", Currency: "EUR", QuoteDate: "2025-01-04"},
	} {
		q.FetchedAt = at.Add(time.Duration(i) * time.Hour)
		if err := s.SaveQuote(ctx, &q, ""); err != nil {
			t.Fatal(err)
		}
	}
	full, err := s.Performance(ctx, "alice", p.ID)
	if err != nil || full.Invested != "22.00" || full.Current == nil || full.Current.Value != "22.51" {
		t.Fatal(full, err)
	}
	selected, err := s.PerformanceForInstruments(ctx, "alice", p.ID, []int64{1, 2, 2})
	if err != nil || selected.Invested != "2.00" || selected.AtPurchase.Value != "2.50" || selected.AtPurchase.Performance != "0.50" || *selected.AtPurchase.PerformancePct != "25.00" || len(selected.Series) != 2 {
		t.Fatal(selected, err)
	}
	c := selected.Current
	// Sum unrounded instrument values: 2.005 + 0.505 = 2.51, not 2.01 + 0.51.
	if c == nil || c.Value != "2.51" || c.Performance != "0.51" || *c.PerformancePct != "25.50" || c.Partial || !c.ChartPoint || c.AsOfFrom != "2025-01-03" || c.AsOfTo != "2025-01-04" {
		t.Fatal(c)
	}
	partial, err := s.PerformanceForInstruments(ctx, "alice", p.ID, []int64{1, 3})
	if err != nil || partial.Invested != "21.50" || partial.Current == nil || !partial.Current.Partial || partial.Current.Value != "22.01" || partial.Current.AsOfFrom != "2025-01-03" || partial.Current.AsOfTo != "2025-01-03" || !partial.Current.LastFetchedAt.Equal(at) {
		t.Fatal(partial, err)
	}
	noQuote, err := s.PerformanceForInstruments(ctx, "alice", p.ID, []int64{3})
	if err != nil || noQuote.Current != nil || noQuote.Invested != "20.00" || noQuote.AtPurchase.Value != "20.00" {
		t.Fatal(noQuote, err)
	}
	for _, ids := range [][]int64{{}, {99}} {
		empty, err := s.PerformanceForInstruments(ctx, "alice", p.ID, ids)
		if err != nil || empty.Invested != "0.00" || empty.AtPurchase.Value != "0.00" || empty.AtPurchase.Performance != "0.00" || empty.AtPurchase.PerformancePct != nil || empty.Current != nil || len(empty.Series) != 0 {
			t.Fatal(empty, err)
		}
	}
	again, err := s.PerformanceForInstruments(ctx, "alice", p.ID, nil)
	if err != nil || !reflect.DeepEqual(full, again) {
		t.Fatal("filtering must not change saved portfolio values", again, err)
	}
	for _, ids := range [][]int64{nil, {}, {1}} {
		if _, err := s.PerformanceForInstruments(ctx, "bob", p.ID, ids); !errors.Is(err, ErrNotFound) {
			t.Fatal("empty and nonempty selections must remain user-scoped", ids, err)
		}
	}
}

func TestValuationSignsZeroAndHalfUp(t *testing.T) {
	for _, tc := range []struct{ value, invested, gain, pct string }{
		{"45446.26", "36553.92", "8892.34", "24.33"},
		{"80.00", "100.00", "-20.00", "-20.00"},
		{"100.00", "100.00", "0.00", "0.00"},
		{"1.00", "32.00", "-31.00", "-96.88"},
		{"10.00", "0.00", "10.00", ""},
		{"0.005", "0.01", "-0.01", "-50.00"},
		{"100.004", "1.00", "99.00", "9900.40"},
	} {
		value, _ := new(big.Rat).SetString(tc.value)
		invested, _ := new(big.Rat).SetString(tc.invested)
		got := valuation(value, invested)
		if got.Performance != tc.gain || (tc.pct == "" && got.PerformancePct != nil) || (tc.pct != "" && (got.PerformancePct == nil || *got.PerformancePct != tc.pct)) {
			t.Fatalf("%+v: %+v", tc, got)
		}
	}
	for _, tc := range []struct{ raw, want string }{{"-1.005", "-1.01"}, {"-0.005", "-0.01"}, {"-0.0049", "0.00"}, {"0.005", "0.01"}} {
		r, _ := new(big.Rat).SetString(tc.raw)
		if got := money(r); got != tc.want {
			t.Fatalf("%s: %s", tc.raw, got)
		}
	}
}

func TestPerformanceCurrentSelectionSharedQuotesAndPersistence(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Performance")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, "alice", p.ID, []int64{1, 2, 3, 4, 5, 6}); err != nil {
		t.Fatal(err)
	}
	// Symbols and quotes linked by another user are shared. Item 2 has no symbol,
	// item 3 has no quote, item 4 has a purchase newer than its quote, item 6 is unheld.
	for _, id := range []int64{1, 3, 4, 5, 6} {
		if err = s.LinkSymbol(ctx, "bob", id, "S"+big.NewInt(id).String(), "EUR", ""); err != nil {
			t.Fatal(err)
		}
	}
	lines := []TransactionLine{}
	for id := int64(1); id <= 5; id++ {
		lines = append(lines, TransactionLine{InstrumentID: id, Amount: "20", Price: "10", Quantity: "2"})
	}
	if _, err = s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2025-01-03", Lines: lines}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Performance(ctx, "alice", p.ID)
	if err != nil || before.Current != nil || before.AtPurchase.Value != "100.00" || before.AtPurchase.Performance != "0.00" {
		t.Fatal(before, err)
	}
	fetched := time.Date(2025, 1, 5, 13, 32, 0, 0, time.UTC)
	save := func(id int64, date, price string, at time.Time) {
		t.Helper()
		q := &marketdata.Quote{InstrumentID: id, Price: price, OrigPrice: price, Currency: "EUR", QuoteDate: date, FetchedAt: at}
		if id == 5 {
			q.Currency = "USD"
			q.OrigPrice = "20.0000"
			q.FXRate = "0.90000000"
			q.FXRateDate = "2025-01-03"
		}
		if err := s.SaveQuote(ctx, q, "S"+big.NewInt(id).String()); err != nil {
			t.Fatal(err)
		}
	}
	save(1, "2025-01-04", "11.0000", fetched.Add(-time.Hour))
	save(1, "2025-01-04", "12.0000", fetched)                   // fetched_at tie-breaker within newest date
	save(1, "2025-01-03", "99.0000", fetched.Add(24*time.Hour)) // fetch time must not outrank quote date
	save(4, "2025-01-02", "99.0000", fetched.Add(2*time.Hour))
	save(5, "2025-01-05", "18.0000", fetched.Add(-time.Minute))
	save(6, "2025-01-06", "99.0000", fetched.Add(2*time.Hour))
	perf, err := s.Performance(ctx, "alice", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	c := perf.Current
	if c == nil || !c.Partial || c.Value != "120.00" || c.Performance != "20.00" || c.PerformancePct == nil || *c.PerformancePct != "20.00" || !c.ChartPoint {
		t.Fatalf("%+v", c)
	}
	if c.AsOfFrom != "2025-01-04" || c.AsOfTo != "2025-01-05" || !c.LastFetchedAt.Equal(fetched) || len(c.Items) != 5 {
		t.Fatalf("%+v", c)
	}
	for i, want := range []struct{ source, reason, price string }{{"QUOTE", "", "12.0000"}, {"PURCHASE", "NO_SYMBOL", "10.0000"}, {"PURCHASE", "NO_QUOTE", "10.0000"}, {"PURCHASE", "PURCHASE_NEWER", "10.0000"}, {"QUOTE", "", "18.0000"}} {
		got := c.Items[i]
		if got.Source != want.source || got.Reason != want.reason || got.PriceEUR != want.price || got.Units != "2.000000" {
			t.Fatalf("%d: %+v", i, got)
		}
	}
	if c.Items[4].FXRate == nil || *c.Items[4].FXRate != "0.90000000" || *c.Items[4].FXRateDate != "2025-01-03" || c.Items[4].Value != "36.00" {
		t.Fatal(c.Items[4])
	}
	// Every dashboard request reconstructs this from SQLite, without a live job.
	again, err := s.Performance(ctx, "alice", p.ID)
	if err != nil || again.Current == nil || again.Current.Value != c.Value {
		t.Fatal(again, err)
	}
	if _, err = s.Performance(ctx, "bob", p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// A later transaction hides the chart extension even when some items still use quotes.
	if _, err = s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2025-01-05", Lines: []TransactionLine{{InstrumentID: 3, Amount: "30", Price: "15", Quantity: "2"}}}); err != nil {
		t.Fatal(err)
	}
	perf, err = s.Performance(ctx, "alice", p.ID)
	if err != nil || perf.Current.ChartPoint {
		t.Fatal(perf.Current, err)
	}
	// Zero units are excluded independently of the quote's existence.
	perf.held[1] = new(big.Int)
	stored, _ := s.transactionRows(ctx, "alice", p.ID)
	c, err = s.currentValuation(ctx, stored, perf)
	if err != nil || len(c.Items) != 4 || c.AsOfFrom != "2025-01-05" {
		t.Fatal(c, err)
	}
}

func TestPurchasePerformancePositiveNegativeAndEmpty(t *testing.T) {
	for _, tc := range []struct {
		price     int64
		gain, pct string
	}{{200000, "10.00", "33.33"}, {50000, "-5.00", "-33.33"}, {100000, "0.00", "0.00"}} {
		// First purchase: one unit at 10. Second purchase: one unit at the new price.
		lines := []TransactionRow{{TransactionLine: TransactionLine{InstrumentID: 1}, Date: "2025-01-01", a: 1000, q: 1000000, p: 100000}, {TransactionLine: TransactionLine{InstrumentID: 1}, Date: "2025-01-02", a: tc.price / 100, q: 1000000, p: tc.price}}
		got := purchasePerformance(lines).AtPurchase
		if got.Performance != tc.gain || got.PerformancePct == nil || *got.PerformancePct != tc.pct {
			t.Fatal(got)
		}
	}
	empty := purchasePerformance(nil)
	if empty.AtPurchase.Value != "0.00" || empty.AtPurchase.Performance != "0.00" || empty.AtPurchase.PerformancePct != nil {
		t.Fatal(empty)
	}
}

func TestCurrentFallbackUsesNewestPurchaseRatherThanDailyWeightedPrice(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Latest purchase")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, "alice", p.ID, []int64{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2025-01-03", Lines: []TransactionLine{
		{InstrumentID: 1, Amount: "10", Price: "10", Quantity: "1"},
		{InstrumentID: 1, Amount: "60", Price: "20", Quantity: "3"},
		{InstrumentID: 2, Amount: "10", Price: "10", Quantity: "1"},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		if err = s.LinkSymbol(ctx, "alice", id, "S"+big.NewInt(id).String(), "EUR", ""); err != nil {
			t.Fatal(err)
		}
	}
	save := func(id int64, date string) {
		t.Helper()
		if err := s.SaveQuote(ctx, &marketdata.Quote{InstrumentID: id, Price: "10.0000", OrigPrice: "10.0000", Currency: "EUR", QuoteDate: date, FetchedAt: time.Now()}, "S"+big.NewInt(id).String()); err != nil {
			t.Fatal(err)
		}
	}
	save(1, "2025-01-02")
	perf, err := s.Performance(ctx, "alice", p.ID)
	if err != nil || perf.Current != nil {
		t.Fatal("no usable quote must give null", perf.Current, err)
	}
	save(2, "2025-01-03") // Same-day quote wins; newest quote equals the last trade date, no chart point.
	perf, err = s.Performance(ctx, "alice", p.ID)
	if err != nil || perf.Current == nil || perf.Current.Value != "90.00" || perf.Current.Items[0].PriceEUR != "20.0000" || perf.Current.ChartPoint {
		t.Fatal(perf.Current, err)
	}
	if perf.AtPurchase.Value != "80.00" {
		t.Fatal("historical weighted-price chart changed", perf.AtPurchase)
	}
}
