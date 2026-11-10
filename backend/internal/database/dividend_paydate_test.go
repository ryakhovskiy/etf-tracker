package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

// entriesOf lists the payments (pay=true) or ex-date markers of a month.
func entriesOf(cal DividendCalendar, month int, pay bool) []DividendEntry {
	if pay {
		return cal.Months[month-1].Entries
	}
	return cal.Months[month-1].ExEntries
}

func TestDividendPayDatesPlaceEntriesInThePaymentMonth(t *testing.T) {
	// calToday is 2026-11-07. 10 units held since 2025.
	snap := DividendSnapshot{
		Items: []DividendItem{divItem(1, "HMWO.L", units("2025-01-02", 10000000))},
		Events: map[int64][]StoredDividend{1: {
			divEvent("2025-12-15", "1.000000", "EUR", ""), // paid 2026-01-14
			divEvent("2026-10-02", "2.000000", "EUR", ""), // pay 2026-11-01 (Sunday) -> Monday 2026-11-02: paid
			divEvent("2026-10-20", "3.000000", "EUR", ""), // pay 2026-11-19: ex passed, not paid yet
		}},
		PayDelays: map[int64]int{1: 30},
	}
	cal := BuildDividendCalendar(snap, 2026, calToday, (&fxTable{}).lookup)

	jan := entriesOf(cal, 1, true)
	if len(jan) != 1 || jan[0].ExDate != "2025-12-15" || jan[0].PayDate != "2026-01-14" || !jan[0].Paid || jan[0].PayDelayDays != 30 {
		t.Fatalf("January payments %+v", jan)
	}
	// The 2025-12-15 event repeats as a forecast: ex 2026-12-15 (a marker only),
	// paid 2027-01-14 and so not counted in 2026.
	if dec := entriesOf(cal, 12, false); len(dec) != 1 || dec[0].Kind != DividendForecast || dec[0].PayDate != "2027-01-14" || len(entriesOf(cal, 12, true)) != 0 || cal.Months[11].Total != "0.00" {
		t.Fatalf("December %+v", cal.Months[11])
	}
	nov := entriesOf(cal, 11, true)
	if len(nov) != 2 || nov[0].PayDate != "2026-11-02" || !nov[0].PayDateShifted || !nov[0].Paid ||
		nov[1].PayDate != "2026-11-19" || nov[1].PayDateShifted || nov[1].Paid {
		t.Fatalf("November payments %+v", nov)
	}
	if len(entriesOf(cal, 10, true)) != 0 {
		t.Fatal("October has no payments")
	}
	oct := entriesOf(cal, 10, false)
	if len(oct) != 2 || oct[0].ExDate != "2026-10-02" || oct[1].ExDate != "2026-10-20" {
		t.Fatalf("October ex markers %+v", oct)
	}
	// Month totals count payments; markers add only to ExTotal.
	if cal.Months[0].Total != "10.00" || cal.Months[10].Total != "50.00" || cal.Months[9].Total != "0.00" || cal.Months[9].ExTotal != "50.00" || cal.Months[11].ExTotal != "10.00" {
		t.Fatalf("totals %+v", cal.Months)
	}
	// Received means paid; the unpaid October dividend is expected.
	if cal.Totals.Received != "30.00" || cal.Totals.Expected != "30.00" || cal.Totals.Total != "60.00" {
		t.Fatalf("year totals %+v", cal.Totals)
	}

	// 2025: the December ex-date is a marker, its payment is in 2026.
	prev := BuildDividendCalendar(snap, 2025, calToday, (&fxTable{}).lookup)
	if dec := entriesOf(prev, 12, false); len(dec) != 1 || dec[0].PayDate != "2026-01-14" || prev.Months[11].Total != "0.00" || prev.Months[11].ExTotal != "10.00" {
		t.Fatalf("2025 December %+v", prev.Months[11])
	}
	if prev.Totals.Total != "0.00" {
		t.Fatalf("2025 totals %+v", prev.Totals)
	}
}

func TestDividendPayDateDefaultsForecastsAndFX(t *testing.T) {
	snap := DividendSnapshot{
		Items: []DividendItem{divItem(1, "USD.L", units("2025-01-02", 1000000)), divItem(2, "EUR.DE", units("2025-01-02", 1000000))},
		Events: map[int64][]StoredDividend{
			1: {divEvent("2026-09-10", "1.000000", "USD", "")},
			2: {divEvent("2025-12-15", "1.000000", "EUR", "")},
		},
		PayDelays: map[int64]int{1: 20},
	}
	fx := &fxTable{rates: map[string]map[string]string{"USD": {"2026-09-30": "0.90000000@2026-09-30"}}}
	cal := BuildDividendCalendar(snap, 2026, calToday, fx.lookup)
	// Actual: converted at the rate of the pay-date (2026-09-30), not the ex-date.
	usd := entriesOf(cal, 9, true)
	if len(usd) != 1 || usd[0].PayDate != "2026-09-30" || str(usd[0].FXRateDate) != "2026-09-30" || str(usd[0].Total) != "0.90" {
		t.Fatalf("USD payment %+v (fx calls %v)", usd, fx.calls)
	}
	// No delay configured: paid on the ex-date (already a working day).
	eur := entriesOf(cal, 12, true)
	if len(eur) != 1 || eur[0].Kind != DividendForecast || eur[0].PayDate != "2026-12-15" || eur[0].PayDelayDays != 0 || eur[0].Paid {
		t.Fatalf("EUR forecast %+v", eur)
	}
	// A forecast going ex in December 2026 with a delay is paid, and counted, in 2027.
	snap.PayDelays[2] = 30
	next := BuildDividendCalendar(snap, 2027, calToday, (&fxTable{}).lookup)
	if jan := entriesOf(next, 1, true); len(jan) != 1 || jan[0].Kind != DividendForecast || jan[0].ExDate != "2026-12-15" || jan[0].PayDate != "2027-01-14" || next.Months[0].Total != "1.00" {
		t.Fatalf("2027 January %+v", next.Months[0])
	}
	if cur := BuildDividendCalendar(snap, 2026, calToday, (&fxTable{}).lookup); len(entriesOf(cur, 12, true)) != 0 || len(entriesOf(cur, 12, false)) != 1 || cur.Totals.Expected != "0.00" {
		t.Fatalf("2026 December %+v %+v", cur.Months[11], cur.Totals)
	}
}

func TestPayDelayStoreAndItems(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	id := dividendPortfolio(t, s, "alice", 20, 7)
	second, err := s.SavePortfolio(ctx, "alice", 0, "Second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, "alice", second.ID, []int64{20}); err != nil {
		t.Fatal(err)
	}
	other := second.ID
	items, err := s.PortfolioItems(ctx, "alice", id)
	if err != nil || len(items) != 2 || items[0].PayDelayDays != 0 || items[0].LatestQuote != nil {
		t.Fatalf("%+v %v", items, err)
	}
	if err = s.SetPayDelay(ctx, "alice", 20, 30); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPayDelay(ctx, "alice", 20, 31); err != nil { // updates the same row
		t.Fatal(err)
	}
	var validation *ValidationError
	for _, days := range []int{-1, 91} {
		if err = s.SetPayDelay(ctx, "alice", 20, days); !errors.As(err, &validation) {
			t.Fatal(days, err)
		}
	}
	// Bob holds neither instrument; nobody can set a delay for an unheld one.
	if err = s.SetPayDelay(ctx, "bob", 20, 5); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.PortfolioItems(ctx, "bob", id); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// One value per user and instrument, in every portfolio.
	if err = s.SaveQuote(ctx, &marketdata.Quote{InstrumentID: 20, Price: "98.4100", OrigPrice: "98.4100", Currency: "EUR", QuoteDate: "2026-11-06", FetchedAt: time.Now(), Provider: "YAHOO"}, ""); err != nil {
		t.Fatal(err)
	}
	for _, p := range []int64{id, other} {
		items, err = s.PortfolioItems(ctx, "alice", p)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.InstrumentID == 20 && (it.PayDelayDays != 31 || it.LatestQuote == nil || it.LatestQuote.PriceEUR != "98.4100" || it.LatestQuote.QuoteDate != "2026-11-06") {
				t.Fatalf("portfolio %d: %+v %+v", p, it, it.LatestQuote)
			}
		}
	}
	snap, err := s.DividendSnapshot(ctx, "alice", id)
	if err != nil || snap.PayDelays[20] != 31 {
		t.Fatalf("%+v %v", snap.PayDelays, err)
	}
	if snap, err = s.DividendSnapshot(ctx, "bob", dividendPortfolio(t, s, "bob", 20)); err != nil || snap.PayDelays[20] != 0 {
		t.Fatalf("bob sees alice's delay: %+v %v", snap.PayDelays, err)
	}
}
