package database

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"holdings/backend/internal/marketdata"
)

const (
	DividendActual   = "ACTUAL"
	DividendForecast = "FORECAST"
	// DividendStaleAfter is how old a successful sync may be before the tab queues it again.
	DividendStaleAfter = 7 * 24 * time.Hour
	forecastBasisDays  = 365 // actual events this recent are repeated every 12 months
	forecastMatchDays  = 20  // a projection near an actual event is dropped
	// Warnings for entries whose EUR values cannot be computed.
	WarningFXUnsupported = "FX_UNSUPPORTED_CURRENCY"
	WarningFXUnavailable = "FX_RATE_UNAVAILABLE"
)

// FXRates returns, for each date, the ECB publication used on it (latest on or
// before the date). It is called once per currency. Missing dates have no rate.
type FXRates func(currency string, dates []string) (map[string]*marketdata.FXRate, error)

type DividendEntry struct {
	InstrumentID int64   `json:"instrumentId"`
	Ticker       string  `json:"ticker"`
	ISIN         *string `json:"isin"`
	ShortName    string  `json:"shortName"`
	Kind         string  `json:"kind"`
	ExDate       string  `json:"exDate"`
	// PayDate is estimated: ex-date + PayDelayDays, moved to the next German
	// working day (PayDateShifted). Paid reports PayDate <= today.
	PayDate        string  `json:"payDate"`
	PayDateShifted bool    `json:"payDateShifted"`
	PayDelayDays   int     `json:"payDelayDays"`
	Paid           bool    `json:"paid"`
	BasisExDate    *string `json:"basisExDate"`
	PerUnitOrig    string  `json:"perUnitOrig"`
	Currency       string  `json:"currency"`
	PerUnitEUR     *string `json:"perUnitEur"`
	FXRate         *string `json:"fxRate"`
	FXRateDate     *string `json:"fxRateDate"`
	YieldPct       *string `json:"yieldPct"`
	// Close is the price the yield uses, in the listing currency: the close before
	// the ex-date, or for forecasts the latest close (dated CloseDate).
	Close     *string `json:"close"`
	CloseDate *string `json:"closeDate"`
	Units     string  `json:"units"`
	Total     *string `json:"total"`
	Held      bool    `json:"held"`
	Warning   *string `json:"warning"`
}

// DividendMonth holds the payments of a month (Entries, summed in Total and the
// year totals) and, as markers only, the dividends going ex in it (ExEntries,
// summed in ExTotal and nowhere else).
type DividendMonth struct {
	Month     int             `json:"month"`
	Total     string          `json:"total"`
	Entries   []DividendEntry `json:"entries"`
	ExTotal   string          `json:"exTotal"`
	ExEntries []DividendEntry `json:"exEntries"`
}

type DividendTotals struct {
	Received string `json:"received"`
	Expected string `json:"expected"`
	Total    string `json:"total"`
}

type DividendBounds struct {
	MinYear int `json:"minYear"`
	MaxYear int `json:"maxYear"`
}

type DividendSyncError struct {
	InstrumentID int64      `json:"instrumentId"`
	Label        string     `json:"label"`
	Message      string     `json:"message"`
	At           time.Time  `json:"at"`
	LastSyncedAt *time.Time `json:"lastSyncedAt"`
}

type DividendSyncInfo struct {
	Provider     string              `json:"provider"`
	LastSyncedAt *time.Time          `json:"lastSyncedAt"`
	Stale        []int64             `json:"stale"`
	Errors       []DividendSyncError `json:"errors"`
	JobID        *string             `json:"jobId"`
}

type DividendCalendar struct {
	Year   int              `json:"year"`
	Bounds DividendBounds   `json:"bounds"`
	Totals DividendTotals   `json:"totals"`
	Months []DividendMonth  `json:"months"`
	Sync   DividendSyncInfo `json:"sync"`
}

func yearOf(day string) int { y, _ := strconv.Atoi(day[:4]); return y }

func dayTime(day string) time.Time { t, _ := time.Parse("2006-01-02", day); return t }

// addYears moves a day n years forward on the same month and day; 29 February
// becomes 28 February in common years.
func addYears(day string, n int) string {
	t := dayTime(day)
	y, m, d := t.Date()
	if m == time.February && d == 29 {
		if leap := time.Date(y+n, time.March, 0, 0, 0, 0, 0, time.UTC).Day() == 29; !leap {
			d = 28
		}
	}
	return time.Date(y+n, m, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

func absDays(a, b string) int {
	d := int(dayTime(a).Sub(dayTime(b)).Hours() / 24)
	if d < 0 {
		return -d
	}
	return d
}

// Bounds are the selectable years: the first transaction year (or this year)
// through next year.
func (s DividendSnapshot) Bounds(today string) DividendBounds {
	current := yearOf(today)
	minYear := current
	if s.FirstTradeDate != "" {
		minYear = min(yearOf(s.FirstTradeDate), current)
	}
	return DividendBounds{MinYear: minYear, MaxYear: current + 1}
}

// NeededFrom is the first day the dividend cache must cover for this portfolio:
// 1 January of the year before the first transaction (or before this year).
func (s DividendSnapshot) NeededFrom(today string) string {
	return fmt.Sprintf("%04d-01-01", s.Bounds(today).MinYear-1)
}

// IsStale reports whether an instrument needs a sync: never synced, synced
// more than 7 days ago, or synced from a later date than this portfolio needs.
func (s DividendSnapshot) IsStale(id int64, now time.Time, today string) bool {
	st, ok := s.Sync[id]
	return !ok || st.LastSyncedAt.IsZero() || now.Sub(st.LastSyncedAt) > DividendStaleAfter || st.SyncedFrom > s.NeededFrom(today)
}

func (it DividendItem) label() string {
	if strings.TrimSpace(it.Ticker) != "" {
		return it.Ticker
	}
	return it.ISIN
}

// SyncInfo summarizes freshness and failures for the header and error banner.
func (s DividendSnapshot) SyncInfo(now time.Time, today string) DividendSyncInfo {
	out := DividendSyncInfo{Provider: marketdata.ProviderYahoo, Stale: []int64{}, Errors: []DividendSyncError{}}
	for _, it := range s.Items {
		if s.IsStale(it.InstrumentID, now, today) {
			out.Stale = append(out.Stale, it.InstrumentID)
		}
		st, ok := s.Sync[it.InstrumentID]
		if !ok {
			continue
		}
		var synced *time.Time
		if !st.LastSyncedAt.IsZero() {
			t := st.LastSyncedAt
			synced = &t
			if out.LastSyncedAt == nil || t.After(*out.LastSyncedAt) {
				out.LastSyncedAt = &t
			}
		}
		if st.LastError != "" {
			out.Errors = append(out.Errors, DividendSyncError{InstrumentID: it.InstrumentID, Label: it.label(), Message: st.LastError, At: st.LastErrorAt, LastSyncedAt: synced})
		}
	}
	return out
}

func (it DividendItem) unitsBefore(day string) int64 {
	var n int64
	for _, l := range it.lines {
		if l.date < day {
			n += l.qty
		}
	}
	return n
}

func (it DividendItem) currentUnits() int64 {
	var n int64
	for _, l := range it.lines {
		n += l.qty
	}
	return n
}

// unitsString drops trailing zeros: 120.500000 -> "120.5", 0 -> "0".
func unitsString(e6 int64) string {
	s := strings.TrimRight(decimalString(e6, 6), "0")
	return strings.TrimSuffix(s, ".")
}

func ptr[T any](v T) *T { return &v }

type plannedDividend struct {
	entry            DividendEntry
	amountE6, units  int64
	closeE4          int64
	fxDate, currency string // fxDate: publication date wanted; currency: ISO (minor units mapped)
	divisor          int64
}

// planDividends lists the actual events and the forecasts that go ex or are
// paid in the year. A payment can fall into the year after its ex-date, so
// ex-dates of the previous year are considered too.
func planDividends(snap DividendSnapshot, year int, today string) []plannedDividend {
	out := []plannedDividend{}
	basisFrom := dayTime(today).AddDate(0, 0, -forecastBasisDays).Format("2006-01-02")
	for _, it := range snap.Items {
		var isin *string
		if it.ISIN != "" {
			isin = ptr(it.ISIN)
		}
		delay := snap.PayDelays[it.InstrumentID]
		base := DividendEntry{InstrumentID: it.InstrumentID, Ticker: it.label(), ISIN: isin, ShortName: it.Name, PayDelayDays: delay}
		add := func(e DividendEntry, amountE6, units, closeE4 int64, currency, fxDate string) {
			e.PayDate, e.PayDateShifted = marketdata.PayDate(e.ExDate, delay)
			if yearOf(e.ExDate) != year && yearOf(e.PayDate) != year {
				return
			}
			e.Paid = e.PayDate <= today
			if e.Kind == DividendActual {
				fxDate = e.PayDate // converted when paid; min(today) below for future payments
			}
			e.PerUnitOrig, e.Currency, e.Units, e.Held = marketdata.Fixed(amountE6, 6), currency, unitsString(units), units > 0
			if closeE4 > 0 {
				e.Close = ptr(decimalString(closeE4, 4))
			}
			div := int64(1)
			if marketdata.MinorUnit(currency) {
				div = 100
			}
			out = append(out, plannedDividend{entry: e, amountE6: amountE6, units: units, closeE4: closeE4, fxDate: min(fxDate, today), currency: marketdata.MajorUnit(currency), divisor: div})
		}
		events := snap.Events[it.InstrumentID]
		for _, ev := range events {
			if y := yearOf(ev.ExDate); y != year && y != year-1 {
				continue
			}
			e := base
			e.Kind, e.ExDate = DividendActual, ev.ExDate
			// Buying on the ex-date does not qualify; future events use current units.
			add(e, ev.AmountE6, it.unitsBefore(ev.ExDate), ev.CloseBeforeE4, ev.Currency, ev.ExDate)
		}
		st := snap.Sync[it.InstrumentID]
		for _, basis := range events {
			if basis.ExDate < basisFrom || basis.ExDate > today {
				continue
			}
			for n := 1; ; n++ {
				day := addYears(basis.ExDate, n)
				if yearOf(day) > year {
					break
				}
				if yearOf(day) < year-1 || day <= today {
					continue
				}
				near := false
				for _, ev := range events {
					if absDays(ev.ExDate, day) <= forecastMatchDays {
						near = true
						break
					}
				}
				if near {
					continue
				}
				e := base
				e.Kind, e.ExDate, e.BasisExDate = DividendForecast, day, ptr(basis.ExDate)
				var closeE4 int64
				if st.Currency == basis.Currency && st.LatestCloseE4 > 0 {
					closeE4 = st.LatestCloseE4
					e.CloseDate = ptr(st.LatestCloseDate)
				}
				add(e, basis.AmountE6, it.currentUnits(), closeE4, basis.Currency, today)
			}
		}
	}
	return out
}

// BuildDividendCalendar computes the 12 months of a year: payments in the month
// of their estimated pay-date, ex-date markers in the month of the ex-date.
// Actual events use the units held before the ex-date and the ECB rate of the
// pay-date (latest rate while it is in the future); forecasts come from the last
// 365 days and use current units and the latest rate. Sync is not filled.
func BuildDividendCalendar(snap DividendSnapshot, year int, today string, rates FXRates) DividendCalendar {
	planned := planDividends(snap, year, today)
	// One rates call per currency, covering every date it needs.
	dates := map[string][]string{}
	for _, p := range planned {
		if p.currency != "EUR" {
			dates[p.currency] = append(dates[p.currency], p.fxDate)
		}
	}
	type result struct {
		rates map[string]*marketdata.FXRate
		err   error
	}
	loaded := map[string]result{}
	currencies := make([]string, 0, len(dates))
	for c := range dates {
		currencies = append(currencies, c)
	}
	sort.Strings(currencies)
	for _, c := range currencies {
		r, err := rates(c, dates[c])
		loaded[c] = result{r, err}
	}
	hundred := big.NewRat(100, 1)
	received, expected := new(big.Rat), new(big.Rat)
	cal := DividendCalendar{Year: year, Bounds: snap.Bounds(today), Months: make([]DividendMonth, 12)}
	monthTotals, exTotals := make([]*big.Rat, 12), make([]*big.Rat, 12)
	for i := range cal.Months {
		cal.Months[i] = DividendMonth{Month: i + 1, Entries: []DividendEntry{}, ExEntries: []DividendEntry{}}
		monthTotals[i], exTotals[i] = new(big.Rat), new(big.Rat)
	}
	for _, p := range planned {
		e := p.entry
		amount := new(big.Rat).SetFrac(big.NewInt(p.amountE6), big.NewInt(1000000))
		if p.closeE4 > 0 {
			ratio := new(big.Rat).Quo(amount, new(big.Rat).SetFrac(big.NewInt(p.closeE4), big.NewInt(10000)))
			e.YieldPct = ptr(money(ratio.Mul(ratio, hundred)))
		}
		perUnit := new(big.Rat).Quo(amount, new(big.Rat).SetInt64(p.divisor))
		ok := true
		if p.currency != "EUR" {
			r := loaded[p.currency]
			fx := r.rates[p.fxDate]
			switch {
			case errors.Is(r.err, marketdata.ErrFXUnsupported):
				e.Warning, ok = ptr(WarningFXUnsupported), false
			case fx == nil:
				e.Warning, ok = ptr(WarningFXUnavailable), false
			default:
				perUnit.Mul(perUnit, new(big.Rat).SetFrac(big.NewInt(fx.Rate), big.NewInt(100000000)))
				e.FXRate, e.FXRateDate = ptr(marketdata.Fixed(fx.Rate, 8)), ptr(fx.Date)
			}
		}
		total := new(big.Rat)
		switch {
		case ok:
			// The total uses the per-unit value as shown (4 decimals).
			e4 := halfUp(new(big.Rat).Mul(perUnit, big.NewRat(10000, 1)))
			e.PerUnitEUR = ptr(decimalString(e4.Int64(), 4))
			total.Mul(new(big.Rat).SetFrac(e4, big.NewInt(10000)), new(big.Rat).SetFrac(big.NewInt(p.units), big.NewInt(1000000)))
			e.Total = ptr(money(total))
			total.SetString(*e.Total)
		case !e.Held:
			e.Total = ptr("0.00") // nothing held: nothing received, whatever the rate
		}
		// The payment counts in its month and in Received (paid) or Expected.
		if yearOf(e.PayDate) == year {
			m := dayTime(e.PayDate).Month() - 1
			if e.Total != nil {
				monthTotals[m].Add(monthTotals[m], total)
				if e.Paid {
					received.Add(received, total)
				} else {
					expected.Add(expected, total)
				}
			}
			cal.Months[m].Entries = append(cal.Months[m].Entries, e)
		}
		// The ex-date is a marker only: it adds to no total but its month's ExTotal.
		if yearOf(e.ExDate) == year {
			m := dayTime(e.ExDate).Month() - 1
			if e.Total != nil {
				exTotals[m].Add(exTotals[m], total)
			}
			cal.Months[m].ExEntries = append(cal.Months[m].ExEntries, e)
		}
	}
	byDate := func(entries []DividendEntry, date func(DividendEntry) string) {
		sort.SliceStable(entries, func(a, b int) bool {
			x, y := entries[a], entries[b]
			if date(x) != date(y) {
				return date(x) < date(y)
			}
			if x.Ticker != y.Ticker {
				return x.Ticker < y.Ticker
			}
			return x.InstrumentID < y.InstrumentID
		})
	}
	for i := range cal.Months {
		byDate(cal.Months[i].Entries, func(e DividendEntry) string { return e.PayDate })
		byDate(cal.Months[i].ExEntries, func(e DividendEntry) string { return e.ExDate })
		cal.Months[i].Total, cal.Months[i].ExTotal = money(monthTotals[i]), money(exTotals[i])
	}
	cal.Totals = DividendTotals{Received: money(received), Expected: money(expected), Total: money(new(big.Rat).Add(received, expected))}
	return cal
}
