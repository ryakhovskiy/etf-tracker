package database

import (
	"strings"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

const calToday = "2026-11-07"

func units(day string, e6 int64) holding { return holding{date: day, qty: e6} }

func divItem(id int64, ticker string, lines ...holding) DividendItem {
	return DividendItem{InstrumentID: id, Ticker: ticker, ISIN: "XX000000000" + string(rune('0'+id%10)), Name: ticker + " Name", lines: lines}
}

func divEvent(day, amount, ccy, close string) StoredDividend {
	a, _ := marketdata.Scaled(amount, 6)
	var c int64
	if close != "" {
		c, _ = marketdata.Scaled(close, 4)
	}
	return StoredDividend{ExDate: day, AmountE6: a, Currency: ccy, CloseBeforeE4: c}
}

type fxTable struct {
	rates map[string]map[string]string // currency -> fx date -> "rate@publication"
	calls map[string][]string
	err   map[string]error
}

func (f *fxTable) lookup(currency string, dates []string) (map[string]*marketdata.FXRate, error) {
	if f.calls == nil {
		f.calls = map[string][]string{}
	}
	f.calls[currency] = append(f.calls[currency], strings.Join(dates, ","))
	if err := f.err[currency]; err != nil {
		return nil, err
	}
	out := map[string]*marketdata.FXRate{}
	for _, d := range dates {
		if v, ok := f.rates[currency][d]; ok {
			parts := strings.Split(v, "@")
			r, _ := marketdata.Scaled(parts[0], 8)
			out[d] = &marketdata.FXRate{Date: parts[1], Base: currency, Quote: "EUR", Rate: r}
		}
	}
	return out, nil
}

func find(t *testing.T, cal DividendCalendar, id int64, kind, exDate string) DividendEntry {
	t.Helper()
	for _, m := range cal.Months {
		for _, e := range m.Entries {
			if e.InstrumentID == id && e.Kind == kind && e.ExDate == exDate {
				return e
			}
		}
	}
	t.Fatalf("no %s entry for %d on %s in %+v", kind, id, exDate, cal.Months)
	return DividendEntry{}
}

func count(cal DividendCalendar, kind string) int {
	n := 0
	for _, m := range cal.Months {
		for _, e := range m.Entries {
			if kind == "" || e.Kind == kind {
				n++
			}
		}
	}
	return n
}

func str(p *string) string {
	if p == nil {
		return "–"
	}
	return *p
}

func TestDividendUnitsAtTheExDate(t *testing.T) {
	snap := DividendSnapshot{
		Items: []DividendItem{divItem(1, "AAA",
			units("2026-05-13", 10_000000), // the day before the ex-date: counts
			units("2026-05-14", 5_000000),  // on the ex-date: does not qualify
			units("2026-06-01", 2_500000))},
		Events: map[int64][]StoredDividend{1: {
			divEvent("2026-03-10", "1", "EUR", "50"),
			divEvent("2026-05-14", "1", "EUR", "50"),
			divEvent("2026-12-01", "1", "EUR", "50"), // announced, after today
		}},
	}
	cal := BuildDividendCalendar(snap, 2026, calToday, (&fxTable{}).lookup)
	if e := find(t, cal, 1, DividendActual, "2026-05-14"); e.Units != "10" || str(e.Total) != "10.00" || !e.Held {
		t.Fatalf("%+v", e)
	}
	// Nothing held yet: still shown, not held, total 0.
	if e := find(t, cal, 1, DividendActual, "2026-03-10"); e.Units != "0" || str(e.Total) != "0.00" || e.Held {
		t.Fatalf("%+v", e)
	}
	// A future event uses the current units.
	if e := find(t, cal, 1, DividendActual, "2026-12-01"); e.Units != "17.5" || str(e.Total) != "17.50" {
		t.Fatalf("%+v", e)
	}
	// So do forecasts (basis 2026-05-14 -> 2027-05-14).
	cal = BuildDividendCalendar(snap, 2027, calToday, (&fxTable{}).lookup)
	if e := find(t, cal, 1, DividendForecast, "2027-05-14"); e.Units != "17.5" || str(e.Total) != "17.50" || str(e.BasisExDate) != "2026-05-14" {
		t.Fatalf("%+v", e)
	}
}

func TestDividendValuesFXGBXAndRounding(t *testing.T) {
	snap := DividendSnapshot{
		Items: []DividendItem{
			divItem(1, "USD1", units("2025-01-02", 120_500000)),
			divItem(2, "GBX1", units("2025-01-02", 100_000000)),
			divItem(3, "XYZ1", units("2025-01-02", 10_000000)),
			divItem(4, "EUR1", units("2025-01-02", 1_000000)),
			divItem(5, "NOFX", units("2025-01-02", 1_000000)),
			divItem(6, "NOTHELD"),
		},
		Events: map[int64][]StoredDividend{
			1: {divEvent("2026-09-17", "0.519077", "USD", "93.625")},
			2: {divEvent("2026-08-06", "39.82", "GBp", "4774")},
			3: {divEvent("2026-08-06", "1", "XYZ", "10")},
			4: {divEvent("2026-02-02", "0.41235", "EUR", "30"), divEvent("2026-03-02", "0.025", "EUR", "80"), divEvent("2026-04-01", "1", "EUR", "80"), divEvent("2026-04-02", "0.01005", "EUR", "1"), divEvent("2026-04-03", "1", "EUR", "")},
			5: {divEvent("2026-03-05", "1", "CHF", "10")},
			6: {divEvent("2026-03-05", "1", "XYZ", "10")},
		},
	}
	fx := &fxTable{rates: map[string]map[string]string{
		"USD": {"2026-09-17": "0.9231@2026-09-17"},
		"GBP": {"2026-08-06": "1.1834@2026-08-06"},
		"CHF": {},
	}, err: map[string]error{"XYZ": marketdata.ErrFXUnsupported}}
	cal := BuildDividendCalendar(snap, 2026, calToday, fx.lookup)

	// 0.519077 USD x 0.9231 = 0.47916 -> 0.4792 EUR; x 120.5 = 57.7436 -> 57.74.
	e := find(t, cal, 1, DividendActual, "2026-09-17")
	if str(e.PerUnitEUR) != "0.4792" || str(e.Total) != "57.74" || str(e.FXRate) != "0.92310000" || str(e.FXRateDate) != "2026-09-17" || e.PerUnitOrig != "0.519077" || e.Currency != "USD" {
		t.Fatalf("%+v", e)
	}
	// Yield from the close before the ex-date: 0.519077 / 93.625 = 0.554 %.
	if str(e.YieldPct) != "0.55" || str(e.Close) != "93.6250" {
		t.Fatalf("%+v", e)
	}
	// Pence: 39.82 GBp = 0.3982 GBP x 1.1834 = 0.4712 EUR. Yield in pence: 39.82 / 4774 = 0.83 %.
	e = find(t, cal, 2, DividendActual, "2026-08-06")
	if str(e.PerUnitEUR) != "0.4712" || str(e.Total) != "47.12" || e.Currency != "GBp" || str(e.YieldPct) != "0.83" {
		t.Fatalf("%+v", e)
	}
	// A currency ECB does not publish: "–" with a warning, not counted.
	e = find(t, cal, 3, DividendActual, "2026-08-06")
	if e.PerUnitEUR != nil || e.Total != nil || str(e.Warning) != WarningFXUnsupported || str(e.YieldPct) != "10.00" {
		t.Fatalf("%+v", e)
	}
	// No publication for the date: "–" with its own warning.
	if e = find(t, cal, 5, DividendActual, "2026-03-05"); e.Total != nil || str(e.Warning) != WarningFXUnavailable {
		t.Fatalf("%+v", e)
	}
	// Not held: 0,00 € even without a rate.
	if e = find(t, cal, 6, DividendActual, "2026-03-05"); str(e.Total) != "0.00" || e.PerUnitEUR != nil || e.Held {
		t.Fatalf("%+v", e)
	}
	// Half-up rounding: per unit to 4 decimals, totals to cents, yield to 2 decimals.
	for _, tc := range []struct{ day, perUnit, total, yield string }{
		{"2026-02-02", "0.4124", "0.41", "1.37"}, // 0.41235 -> 0.4124; 0.41235/30 = 1.3745 %
		{"2026-03-02", "0.0250", "0.03", "0.03"}, // 0.025 x 1 = 2.5 cents -> 3; 0.03125 % -> 0.03
		{"2026-04-01", "1.0000", "1.00", "1.25"}, // exactly 1.25 %
		{"2026-04-02", "0.0101", "0.01", "1.01"}, // 0.01005 -> 0.0101; 1.005 % -> 1.01
		{"2026-04-03", "1.0000", "1.00", "–"},    // no close: no yield
	} {
		e = find(t, cal, 4, DividendActual, tc.day)
		if str(e.PerUnitEUR) != tc.perUnit || str(e.Total) != tc.total || str(e.YieldPct) != tc.yield || e.FXRate != nil {
			t.Fatalf("%s: %+v", tc.day, e)
		}
	}
	// One lookup per currency; EUR needs none.
	if len(fx.calls["USD"]) != 1 || len(fx.calls["GBP"]) != 1 || len(fx.calls["XYZ"]) != 1 || fx.calls["EUR"] != nil {
		t.Fatal(fx.calls)
	}
	// Totals: 57.74 + 47.12 + 0.41 + 0.03 + 1.00 + 0.01 + 1.00 = 107.31, all received.
	if cal.Totals != (DividendTotals{Received: "107.31", Expected: "0.00", Total: "107.31"}) {
		t.Fatalf("%+v", cal.Totals)
	}
}

func quarterly() DividendSnapshot {
	return DividendSnapshot{
		Items: []DividendItem{
			divItem(1, "QTR", units("2025-01-02", 100_000000)),
			divItem(2, "ACC", units("2025-01-02", 10_000000)),
			divItem(3, "NEW", units("2026-01-02", 1_000000)),
			divItem(4, "DEC", units("2025-01-02", 1_000000)),
			divItem(5, "EDGE", units("2025-01-02", 1_000000)),
			divItem(6, "REP", units("2025-01-02", 1_000000)),
		},
		Events: map[int64][]StoredDividend{
			// 2025-10-15 is older than 365 days: not a forecast basis.
			1: {divEvent("2025-10-15", "0.411158", "EUR", "32.45"), divEvent("2026-01-15", "0.251473", "EUR", "34.785"), divEvent("2026-04-15", "0.187044", "EUR", "37.04"), divEvent("2026-07-15", "0.674846", "EUR", "39.185"), divEvent("2026-10-15", "0.417165", "EUR", "39.965")},
			3: {divEvent("2026-06-01", "1", "EUR", "")},
			// The announced actual 2026-12-15 drops 2026-12-01 (14 days away) but
			// not 2026-11-24 (21 days away).
			4: {divEvent("2025-11-24", "1", "EUR", ""), divEvent("2025-12-01", "1", "EUR", ""), divEvent("2026-12-15", "1", "EUR", "")},
			// 2025-11-07 projects to today (dropped), 2025-11-08 to tomorrow (kept).
			5: {divEvent("2025-11-07", "1", "EUR", ""), divEvent("2025-11-08", "2", "EUR", "")},
			// Repeats in 2026 and again in 2027.
			6: {divEvent("2025-12-18", "1", "EUR", "")},
		},
		Sync: map[int64]DividendSync{1: {InstrumentID: 1, Currency: "EUR", LatestCloseE4: 400000, LatestCloseDate: "2026-11-06"}},
	}
}

func TestDividendForecastProjection(t *testing.T) {
	snap := quarterly()
	cal := BuildDividendCalendar(snap, 2026, calToday, (&fxTable{}).lookup)
	// The quarterly ETF's next payouts are in 2027; nothing is forecast for 2026.
	for _, m := range cal.Months {
		for _, e := range m.Entries {
			if e.InstrumentID == 1 && e.Kind == DividendForecast {
				t.Fatalf("%+v", e)
			}
		}
	}
	find(t, cal, 6, DividendForecast, "2026-12-18")
	find(t, cal, 4, DividendForecast, "2026-11-24")
	find(t, cal, 4, DividendActual, "2026-12-15")
	find(t, cal, 5, DividendForecast, "2026-11-08")
	if n := count(cal, DividendForecast); n != 3 {
		t.Fatal(n)
	}
	for _, m := range cal.Months {
		for _, e := range m.Entries {
			if e.Kind == DividendForecast && (e.ExDate == "2026-12-01" || e.ExDate <= calToday) {
				t.Fatalf("forecast must be after today and away from actual events: %+v", e)
			}
		}
	}

	cal = BuildDividendCalendar(snap, 2027, calToday, (&fxTable{}).lookup)
	// Quarterly events projected 12 months forward, valued at current units and the latest close.
	for basis, day := range map[string]string{"2026-01-15": "2027-01-15", "2026-04-15": "2027-04-15", "2026-07-15": "2027-07-15", "2026-10-15": "2027-10-15"} {
		e := find(t, cal, 1, DividendForecast, day)
		if str(e.BasisExDate) != basis || e.Units != "100" || str(e.CloseDate) != "2026-11-06" || str(e.Close) != "40.0000" {
			t.Fatalf("%+v", e)
		}
	}
	if e := find(t, cal, 1, DividendForecast, "2027-10-15"); e.PerUnitOrig != "0.417165" || str(e.PerUnitEUR) != "0.4172" || str(e.Total) != "41.72" || str(e.YieldPct) != "1.04" {
		t.Fatalf("%+v", e)
	}
	// Repeated projection: 2025-12-18 -> 2026-12-18 -> 2027-12-18.
	if e := find(t, cal, 6, DividendForecast, "2027-12-18"); str(e.BasisExDate) != "2025-12-18" {
		t.Fatalf("%+v", e)
	}
	// 2027-12-01 is far from any actual event: projected again.
	find(t, cal, 4, DividendForecast, "2027-12-01")
	find(t, cal, 5, DividendForecast, "2027-11-07")
	// Less than 12 months of history projects whatever exists; no close -> no yield.
	if e := find(t, cal, 3, DividendForecast, "2027-06-01"); e.YieldPct != nil || str(e.Total) != "1.00" {
		t.Fatalf("%+v", e)
	}
	// An instrument without events (accumulating ETF) gets no entries at all.
	for _, m := range cal.Months {
		for _, e := range m.Entries {
			if e.InstrumentID == 2 || e.Kind != DividendForecast {
				t.Fatalf("%+v", e)
			}
		}
	}
	// 4 quarterly + NEW + 2 DEC + 2 EDGE + REP.
	if n := count(cal, DividendForecast); n != 10 {
		t.Fatal(n)
	}
	if cal.Totals.Received != "0.00" || cal.Totals.Expected != cal.Totals.Total {
		t.Fatalf("%+v", cal.Totals)
	}
}

func TestDividendMonthsSortingAndTotals(t *testing.T) {
	snap := DividendSnapshot{
		Items: []DividendItem{
			{InstrumentID: 1, Ticker: "BBB", Name: "B fund", lines: []holding{units("2025-01-02", 2_000000)}},
			{InstrumentID: 2, Ticker: "", ISIN: "IE00B8GKDB10", Name: "Vanguard", lines: []holding{units("2025-01-02", 1_000000)}},
			{InstrumentID: 3, Ticker: "AAA", Name: "A fund", lines: []holding{units("2025-01-02", 1_000000)}},
		},
		Events: map[int64][]StoredDividend{
			1: {divEvent("2026-05-20", "1.5", "EUR", ""), divEvent("2025-12-01", "1", "EUR", "")},
			2: {divEvent("2026-05-14", "0.5", "EUR", "")},
			3: {divEvent("2026-05-20", "1", "EUR", "")},
		},
	}
	cal := BuildDividendCalendar(snap, 2026, calToday, (&fxTable{}).lookup)
	if len(cal.Months) != 12 || cal.Months[0].Month != 1 || cal.Months[11].Month != 12 || cal.Year != 2026 {
		t.Fatalf("%+v", cal.Months)
	}
	may := cal.Months[4]
	if len(may.Entries) != 3 || may.Total != "4.50" {
		t.Fatalf("%+v", may)
	}
	// By ex-date, then ticker; the ISIN stands in for a missing ticker.
	got := []string{}
	for _, e := range may.Entries {
		got = append(got, e.ExDate+" "+e.Ticker)
	}
	if strings.Join(got, ",") != "2026-05-14 IE00B8GKDB10,2026-05-20 AAA,2026-05-20 BBB" {
		t.Fatal(got)
	}
	if may.Entries[0].ISIN == nil || *may.Entries[0].ISIN != "IE00B8GKDB10" || may.Entries[0].ShortName != "Vanguard" {
		t.Fatalf("%+v", may.Entries[0])
	}
	// Forecast 2026-12-01 (from 2025-12-01) is expected; the rest received.
	if cal.Months[11].Total != "2.00" || cal.Totals != (DividendTotals{Received: "4.50", Expected: "2.00", Total: "6.50"}) {
		t.Fatalf("%+v %+v", cal.Months[11], cal.Totals)
	}
	if m := cal.Months[0]; len(m.Entries) != 0 || m.Total != "0.00" || m.Entries == nil {
		t.Fatalf("empty months need an empty list: %+v", m)
	}
}

func TestDividendBoundsAndStaleness(t *testing.T) {
	empty := DividendSnapshot{}
	if b := empty.Bounds(calToday); b != (DividendBounds{2026, 2027}) || empty.NeededFrom(calToday) != "2025-01-01" {
		t.Fatal(b)
	}
	snap := DividendSnapshot{FirstTradeDate: "2024-03-01", Items: []DividendItem{{InstrumentID: 1}, {InstrumentID: 2}, {InstrumentID: 3}, {InstrumentID: 4}, {InstrumentID: 5}}}
	if b := snap.Bounds(calToday); b != (DividendBounds{2024, 2027}) || snap.NeededFrom(calToday) != "2023-01-01" {
		t.Fatal(b)
	}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	snap.Sync = map[int64]DividendSync{
		2: {SyncedFrom: "2023-01-01", LastSyncedAt: now.Add(-6 * 24 * time.Hour)}, // fresh
		3: {SyncedFrom: "2023-01-01", LastSyncedAt: now.Add(-8 * 24 * time.Hour)}, // older than 7 days
		4: {SyncedFrom: "2025-01-01", LastSyncedAt: now.Add(-time.Hour)},          // starts later than needed
		5: {SyncedFrom: "2023-01-01", LastError: "down", LastErrorAt: now},        // never succeeded
	}
	info := snap.SyncInfo(now, calToday)
	if len(info.Stale) != 4 || info.Stale[0] != 1 || info.Stale[1] != 3 || info.Stale[2] != 4 || info.Stale[3] != 5 {
		t.Fatal(info.Stale)
	}
	if info.LastSyncedAt == nil || !info.LastSyncedAt.Equal(now.Add(-time.Hour)) || len(info.Errors) != 1 || info.Errors[0].InstrumentID != 5 || info.Errors[0].LastSyncedAt != nil || info.Provider != "YAHOO" {
		t.Fatalf("%+v", info)
	}
}
