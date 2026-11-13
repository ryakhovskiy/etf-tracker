package database

import (
	"context"
	"reflect"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

func summaryTestLine(t *testing.T, id int64, date, amount, price, quantity string) TransactionRow {
	t.Helper()
	values := []string{amount, price, quantity}
	scales := []int{2, 4, 6}
	var scaled [3]int64
	for i := range values {
		var err error
		if scaled[i], err = parseDecimal(values[i], scales[i]); err != nil {
			t.Fatal(err)
		}
	}
	return TransactionRow{TransactionLine: TransactionLine{InstrumentID: id, Amount: amount, Price: price, Quantity: quantity}, Date: date, a: scaled[0], p: scaled[1], q: scaled[2]}
}

func summaryTestQuote(id int64, price, previous string) *marketdata.Quote {
	at := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	previousDate := ""
	if previous != "" {
		previousDate = "2026-11-12"
	}
	return &marketdata.Quote{InstrumentID: id, Price: price, OrigPrice: price, PrevClose: previous, PrevCloseDate: previousDate, Currency: "EUR", QuoteDate: "2026-11-13", QuoteTime: at, FetchedAt: at.Add(time.Minute), Provider: "YAHOO"}
}

func TestPortfolioSummarySumsUnroundedPositionsBeforeFormatting(t *testing.T) {
	lines := []TransactionRow{summaryTestLine(t, 1, "2026-11-02", "0.50", "1", "0.5"), summaryTestLine(t, 2, "2026-11-02", "0.50", "1", "0.5")}
	for _, tc := range []struct{ first, second, value, delta, pct string }{{"1.01", "1.01", "1.01", "0.01", "1.00"}, {"0.99", "0.99", "0.99", "-0.01", "-1.00"}, {"1.01", "0.99", "1.00", "0.00", "0.00"}} {
		got := summarizeLines(lines, map[int64]*marketdata.Quote{1: summaryTestQuote(1, tc.first, "1"), 2: summaryTestQuote(2, tc.second, "1")}).result()
		checkSummaryPointer(t, "total position value", got.CurrentValue, tc.value)
		checkSummaryFigure(t, "total day delta", got.Change, tc.delta, tc.pct)
	}
}

func checkSummaryPointer(t *testing.T, label string, got *string, want string) {
	t.Helper()
	if want == "" {
		if got != nil {
			t.Fatalf("%s = %q, want null", label, *got)
		}
		return
	}
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %s", label, got, want)
	}
}

func checkSummaryFigure(t *testing.T, label string, got *SummaryFigure, amount, percent string) {
	t.Helper()
	if amount == "" {
		if got != nil {
			t.Fatalf("%s = %+v, want null", label, got)
		}
		return
	}
	if got == nil || got.Amount != amount {
		t.Fatalf("%s = %+v, want amount %s", label, got, amount)
	}
	checkSummaryPointer(t, label+" percent", got.Percent, percent)
}

func TestPortfolioSummarySessionMathAndCoverage(t *testing.T) {
	old := func() TransactionRow { return summaryTestLine(t, 1, "2026-11-02", "900", "90", "10") }
	buy := func() TransactionRow { return summaryTestLine(t, 1, "2026-11-13", "216", "108", "2") }
	weekend := summaryTestQuote(1, "110", "100")
	weekend.FetchedAt = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	older := summaryTestQuote(1, "99", "98")
	older.QuoteDate = "2026-11-01"
	for _, tc := range []struct {
		name                                              string
		lines                                             []TransactionRow
		quotes                                            map[int64]*marketdata.Quote
		invested, value, gain, gainPct, change, changePct string
		partial, changePartial                            bool
		reasons                                           []string
	}{
		{name: "held before plus session purchases", lines: []TransactionRow{old(), buy()}, quotes: map[int64]*marketdata.Quote{1: summaryTestQuote(1, "110", "100")}, invested: "1116.00", value: "1320.00", gain: "204.00", gainPct: "18.28", change: "104.00", changePct: "10.40"},
		{name: "Saturday uses Friday session purchases against Thursday close", lines: []TransactionRow{old(), buy()}, quotes: map[int64]*marketdata.Quote{1: weekend}, invested: "1116.00", value: "1320.00", gain: "204.00", gainPct: "18.28", change: "104.00", changePct: "10.40"},
		{name: "new position has no previous-close percentage base", lines: []TransactionRow{buy()}, quotes: map[int64]*marketdata.Quote{1: summaryTestQuote(1, "110", "100")}, invested: "216.00", value: "220.00", gain: "4.00", gainPct: "1.85", change: "4.00"},
		{name: "zero change on zero previous-close base stays computable", lines: []TransactionRow{buy()}, quotes: map[int64]*marketdata.Quote{1: summaryTestQuote(1, "108", "")}, invested: "216.00", value: "216.00", gain: "0.00", gainPct: "0.00", change: "0.00"},
		{name: "missing previous close omits only held-before contribution", lines: []TransactionRow{old(), buy()}, quotes: map[int64]*marketdata.Quote{1: summaryTestQuote(1, "110", "")}, invested: "1116.00", value: "1320.00", gain: "204.00", gainPct: "18.28", change: "4.00", changePartial: true, reasons: []string{"NO_PREVIOUS_CLOSE"}},
		{name: "missing previous close alone produces unavailable change", lines: []TransactionRow{old()}, quotes: map[int64]*marketdata.Quote{1: summaryTestQuote(1, "110", "")}, invested: "900.00", value: "1100.00", gain: "200.00", gainPct: "22.22", changePartial: true, reasons: []string{"NO_PREVIOUS_CLOSE"}},
		{name: "no quotes never pretends the value is zero", lines: []TransactionRow{old()}, invested: "900.00", partial: true, changePartial: true, reasons: []string{"NO_QUOTE"}},
		{name: "purchase newer than quote is partial", lines: []TransactionRow{old()}, quotes: map[int64]*marketdata.Quote{1: older}, invested: "900.00", partial: true, changePartial: true, reasons: []string{"PURCHASE_NEWER"}},
		{name: "positive half cent rounds away from zero", lines: []TransactionRow{summaryTestLine(t, 1, "2026-11-02", "0.50", "1", "0.5")}, quotes: map[int64]*marketdata.Quote{1: summaryTestQuote(1, "1.01", "1")}, invested: "0.50", value: "0.51", gain: "0.01", gainPct: "1.00", change: "0.01", changePct: "1.00"},
		{name: "negative half cent rounds away from zero", lines: []TransactionRow{summaryTestLine(t, 1, "2026-11-02", "0.50", "1", "0.5")}, quotes: map[int64]*marketdata.Quote{1: summaryTestQuote(1, "0.99", "1")}, invested: "0.50", value: "0.50", gain: "-0.01", gainPct: "-1.00", change: "-0.01", changePct: "-1.00"},
		{name: "empty positions have no invented figures", invested: "0.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeLines(tc.lines, tc.quotes).result()
			if got.Invested != tc.invested || got.Partial != tc.partial || got.ChangePartial != tc.changePartial {
				t.Fatalf("summary %+v", got)
			}
			checkSummaryPointer(t, "current value", got.CurrentValue, tc.value)
			checkSummaryFigure(t, "performance", got.Performance, tc.gain, tc.gainPct)
			checkSummaryFigure(t, "change", got.Change, tc.change, tc.changePct)
			wantReasons := tc.reasons
			if wantReasons == nil {
				wantReasons = []string{}
			}
			if !reflect.DeepEqual(got.Reasons, wantReasons) {
				t.Fatalf("reasons %v, want %v", got.Reasons, wantReasons)
			}
			if tc.value == "" && got.AsOf != nil {
				t.Fatal("unavailable value must not advertise an as-of quote", got.AsOf)
			}
		})
	}
}

func TestPortfolioSummaryPreviousCloseFXAndMinorUnits(t *testing.T) {
	for _, tc := range []struct {
		name, currency, price, original, previous, rate, previousRate, quantity, cost, buyPrice, value, gain, gainPct, change, changePct string
		approximate                                                                                                                      bool
	}{
		{"different current and previous USD rates", "USD", "108.0000", "120.0000", "100.0000", "0.90000000", "0.80000000", "3", "300", "100", "324.00", "24.00", "8.00", "84.00", "35.00", false},
		{"missing previous USD FX is explicit approximation", "USD", "108.0000", "120.0000", "100.0000", "0.90000000", "", "3", "300", "100", "324.00", "24.00", "8.00", "54.00", "20.00", true},
		{"pence converts through major GBP exactly once", "GBp", "58.9086", "4930.0000", "4900.0000", "1.19490000", "1.19700000", "10", "500", "50", "589.09", "89.09", "17.82", "2.56", "0.44", false},
		{"South African cents convert through major ZAR", "ZAc", "0.5000", "1000.0000", "900.0000", "0.05000000", "0.04000000", "10", "4", "0.4", "5.00", "1.00", "25.00", "1.40", "38.89", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := summaryTestQuote(1, tc.price, tc.previous)
			q.Currency = tc.currency
			q.OrigPrice = tc.original
			q.FXRate = tc.rate
			q.PrevFXRate = tc.previousRate
			got := summarizeLines([]TransactionRow{summaryTestLine(t, 1, "2026-11-02", tc.cost, tc.buyPrice, tc.quantity)}, map[int64]*marketdata.Quote{1: q}).result()
			checkSummaryPointer(t, "value", got.CurrentValue, tc.value)
			checkSummaryFigure(t, "performance", got.Performance, tc.gain, tc.gainPct)
			checkSummaryFigure(t, "change", got.Change, tc.change, tc.changePct)
			if got.Partial || got.ChangePartial != tc.approximate {
				t.Fatalf("coverage %+v", got)
			}
			if tc.approximate && !reflect.DeepEqual(got.Reasons, []string{"FX_PREVIOUS_UNAVAILABLE"}) {
				t.Fatal(got.Reasons)
			}
		})
	}
}

func TestPortfolioSummaryMixedFallbacksAndOldestQuote(t *testing.T) {
	lines := []TransactionRow{summaryTestLine(t, 1, "2026-11-02", "10", "10", "1"), summaryTestLine(t, 2, "2026-11-02", "10", "10", "1"), summaryTestLine(t, 2, "2026-11-03", "40", "20", "2")}
	q := summaryTestQuote(1, "12", "11")
	got := summarizeLines(lines, map[int64]*marketdata.Quote{1: q}).result()
	checkSummaryPointer(t, "latest purchase fallback, not weighted purchase", got.CurrentValue, "72.00")
	checkSummaryFigure(t, "performance", got.Performance, "12.00", "20.00")
	checkSummaryFigure(t, "known change subset", got.Change, "1.00", "9.09")
	if !got.Partial || !got.ChangePartial || !reflect.DeepEqual(got.Reasons, []string{"NO_QUOTE"}) {
		t.Fatalf("%+v", got)
	}
	// A computed zero is still available when another position has no quote.
	q.Price = "11"
	q.PrevClose = "11"
	checkSummaryFigure(t, "computed zero subset", summarizeLines(lines, map[int64]*marketdata.Quote{1: q}).result().Change, "0.00", "0.00")
	older := summaryTestQuote(2, "20", "19")
	older.QuoteDate = "2026-11-12"
	older.QuoteTime = q.QuoteTime.Add(-24 * time.Hour)
	older.FetchedAt = q.FetchedAt.Add(-24 * time.Hour)
	got = summarizeLines(lines, map[int64]*marketdata.Quote{1: q, 2: older}).result()
	if got.AsOf == nil || got.AsOf.QuoteDate != "2026-11-12" || got.AsOf.QuoteTime == nil || !got.AsOf.QuoteTime.Equal(older.QuoteTime) || !got.AsOf.FetchedAt.Equal(older.FetchedAt) || !got.AsOf.Realtime {
		t.Fatalf("as-of %+v", got.AsOf)
	}
}

func TestPortfolioSummaryUserScopeExactTotalsAndRefreshUnion(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	makePortfolio := func(user, name string, held int64, quantity, amount string) int64 {
		t.Helper()
		p, err := s.SavePortfolio(ctx, user, 0, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.AddItems(ctx, user, p.ID, []int64{held, 4}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateTransaction(ctx, user, p.ID, TransactionInput{Date: "2026-11-02", Lines: []TransactionLine{{InstrumentID: held, Quantity: quantity, Price: "1", Amount: amount}}}); err != nil {
			t.Fatal(err)
		}
		return p.ID
	}
	a := makePortfolio("alice", "One", 1, "0.5", "0.50")
	b := makePortfolio("alice", "Two", 1, "0.5", "0.50")
	foreign := makePortfolio("bob", "Private", 3, "100", "100")
	q := summaryTestQuote(1, "1.01", "1")
	if err := s.SaveQuote(ctx, q, ""); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{3, 4} {
		q := summaryTestQuote(id, "500", "499")
		q.FetchedAt = q.FetchedAt.Add(time.Hour)
		if err := s.SaveQuote(ctx, q, ""); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Summaries(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Summaries) != 2 || got.Total.PortfolioCount != 2 || got.Total.PositionCount != 2 {
		t.Fatalf("user scope %+v", got)
	}
	for _, p := range got.Summaries {
		if p.PortfolioID == foreign || (p.PortfolioID != a && p.PortfolioID != b) {
			t.Fatalf("foreign portfolio %+v", p)
		}
		checkSummaryPointer(t, "individual rounded value", p.CurrentValue, "0.51")
	}
	checkSummaryPointer(t, "total rounds after aggregation", got.Total.CurrentValue, "1.01")
	checkSummaryFigure(t, "total change", got.Total.Change, "0.01", "1.00")
	checkSummaryFigure(t, "total performance", got.Total.Performance, "0.01", "1.00")
	held, err := s.UserHeldInstruments(ctx, "alice")
	if err != nil || len(held) != 1 || held[0].InstrumentID != 1 {
		t.Fatalf("union %+v %v", held, err)
	}
	newest, ok, err := s.NewestHeldFetchedAt(ctx, "alice")
	if err != nil || !ok || !newest.Equal(q.FetchedAt) {
		t.Fatalf("foreign/unheld quote affected freshness: %v %v %v", newest, ok, err)
	}
	oldHistory := summaryTestQuote(1, "99", "98")
	oldHistory.QuoteDate = "2026-11-12"
	oldHistory.FetchedAt = q.FetchedAt.Add(2 * time.Hour)
	if err := s.SaveQuote(ctx, oldHistory, ""); err != nil {
		t.Fatal(err)
	}
	newest, _, err = s.NewestHeldFetchedAt(ctx, "alice")
	if err != nil || !newest.Equal(q.FetchedAt) {
		t.Fatalf("newly fetched old history outranked latest market date: %v %v", newest, err)
	}
	empty, err := s.Summaries(ctx, "nobody")
	if err != nil || len(empty.Summaries) != 0 || empty.Total.PortfolioCount != 0 {
		t.Fatalf("unknown user leaked data %+v %v", empty, err)
	}
}
