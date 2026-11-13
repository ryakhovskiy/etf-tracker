package database

import (
	"context"
	"math/big"
	"sort"
	"time"

	"holdings/backend/internal/marketdata"
)

// Reason codes of a partial summary figure.
const (
	ReasonNoQuote       = "NO_QUOTE"                // no stored quote: valued at the latest purchase price
	ReasonPurchaseNewer = "PURCHASE_NEWER"          // the quote is older than the latest purchase
	ReasonNoPrevClose   = "NO_PREVIOUS_CLOSE"       // units held before the session, but no stored previous close
	ReasonFXPrevMissing = "FX_PREVIOUS_UNAVAILABLE" // previous close converted at the quote's own FX rate
)

// SummaryAsOf describes the OLDEST quote used by a summary.
type SummaryAsOf struct {
	QuoteDate string     `json:"quoteDate"`
	QuoteTime *time.Time `json:"quoteTime"`
	FetchedAt time.Time  `json:"fetchedAt"`
	Providers []string   `json:"providers"`
	Realtime  bool       `json:"realtime"`
}

// SummaryFigure is a EUR amount with its percentage (null when the base is 0).
type SummaryFigure struct {
	Amount  string  `json:"amount"`
	Percent *string `json:"percent"`
}

// Summary is the list-page figure set of one portfolio, or of all of them.
type Summary struct {
	PositionCount int            `json:"positionCount"`
	Invested      string         `json:"invested"`
	CurrentValue  *string        `json:"currentValue"`
	Performance   *SummaryFigure `json:"performance"`
	Change        *SummaryFigure `json:"change"`
	Partial       bool           `json:"partial"`
	ChangePartial bool           `json:"changePartial"`
	Reasons       []string       `json:"reasons"`
	AsOf          *SummaryAsOf   `json:"asOf"`
}

type PortfolioSummary struct {
	PortfolioID int64 `json:"portfolioId"`
	Summary
}

type TotalSummary struct {
	PortfolioCount int `json:"portfolioCount"`
	Summary
}

type Summaries struct {
	Summaries []PortfolioSummary `json:"summaries"`
	Total     TotalSummary       `json:"total"`
}

// summaryAcc accumulates exact (unrounded) amounts so that portfolios can be
// combined before any rounding.
type summaryAcc struct {
	positions                 int
	invested, value, change   *big.Rat
	base                      *big.Rat // value at the previous close of the units held before the session
	hasQuote, hasChange       bool
	partial, changePartial    bool
	reasons                   map[string]bool
	used                      int
	oldestDate                string
	oldestFetched             time.Time
	oldestQuoteTime           time.Time
	allQuoteTime, allRealtime bool
	providers                 map[string]bool
}

func newSummaryAcc() *summaryAcc {
	return &summaryAcc{invested: new(big.Rat), value: new(big.Rat), change: new(big.Rat), base: new(big.Rat),
		reasons: map[string]bool{}, providers: map[string]bool{}, allQuoteTime: true, allRealtime: true}
}

func (a *summaryAcc) noteQuote(q *marketdata.Quote) {
	if a.used == 0 || q.QuoteDate < a.oldestDate {
		a.oldestDate = q.QuoteDate
	}
	if a.used == 0 || q.FetchedAt.Before(a.oldestFetched) {
		a.oldestFetched = q.FetchedAt
	}
	if q.QuoteTime.IsZero() {
		a.allQuoteTime = false
	} else if a.oldestQuoteTime.IsZero() || q.QuoteTime.Before(a.oldestQuoteTime) {
		a.oldestQuoteTime = q.QuoteTime
	}
	if q.Provider != marketdata.ProviderYahoo || q.QuoteTime.IsZero() {
		a.allRealtime = false
	}
	a.providers[q.Provider] = true
	a.used++
}

func (a *summaryAcc) add(b *summaryAcc) {
	a.positions += b.positions
	a.invested.Add(a.invested, b.invested)
	a.value.Add(a.value, b.value)
	a.change.Add(a.change, b.change)
	a.base.Add(a.base, b.base)
	a.hasQuote = a.hasQuote || b.hasQuote
	a.hasChange = a.hasChange || b.hasChange
	a.partial = a.partial || b.partial
	a.changePartial = a.changePartial || b.changePartial
	for r := range b.reasons {
		a.reasons[r] = true
	}
	if b.used > 0 {
		if a.used == 0 || b.oldestDate < a.oldestDate {
			a.oldestDate = b.oldestDate
		}
		if a.used == 0 || b.oldestFetched.Before(a.oldestFetched) {
			a.oldestFetched = b.oldestFetched
		}
		if !b.oldestQuoteTime.IsZero() && (a.oldestQuoteTime.IsZero() || b.oldestQuoteTime.Before(a.oldestQuoteTime)) {
			a.oldestQuoteTime = b.oldestQuoteTime
		}
		a.allQuoteTime = a.allQuoteTime && b.allQuoteTime
		a.allRealtime = a.allRealtime && b.allRealtime
		for p := range b.providers {
			a.providers[p] = true
		}
		a.used += b.used
	}
}

func pct(amount, base *big.Rat) *string {
	if base.Sign() == 0 {
		return nil
	}
	s := money(new(big.Rat).Mul(new(big.Rat).Quo(amount, base), big.NewRat(100, 1)))
	return &s
}

// result formats the accumulated figures. Rounding happens only here.
func (a *summaryAcc) result() Summary {
	out := Summary{PositionCount: a.positions, Invested: money(a.invested), Partial: a.partial, ChangePartial: a.changePartial, Reasons: []string{}}
	for r := range a.reasons {
		out.Reasons = append(out.Reasons, r)
	}
	sort.Strings(out.Reasons)
	if a.hasQuote {
		v := money(a.value)
		gain := new(big.Rat).Sub(a.value, a.invested)
		out.CurrentValue = &v
		out.Performance = &SummaryFigure{Amount: money(gain), Percent: pct(gain, a.invested)}
		asOf := &SummaryAsOf{QuoteDate: a.oldestDate, FetchedAt: a.oldestFetched.UTC(), Realtime: a.allRealtime, Providers: []string{}}
		if a.allQuoteTime && !a.oldestQuoteTime.IsZero() {
			t := a.oldestQuoteTime.UTC()
			asOf.QuoteTime = &t
		}
		for p := range a.providers {
			asOf.Providers = append(asOf.Providers, p)
		}
		sort.Strings(asOf.Providers)
		out.AsOf = asOf
	}
	if a.hasChange {
		out.Change = &SummaryFigure{Amount: money(a.change), Percent: pct(a.change, a.base)}
	}
	return out
}

// summarizeLines values the positions of one portfolio's transaction lines
// (ordered by trade date) with the latest stored quote per instrument.
//
// Current value: units held x the quote's EUR price, or the latest purchase
// price (flagged partial) when the instrument has no quote or the quote is older
// than the latest purchase.
//
// Change vs yesterday, per instrument with a usable quote, where the session is
// the quote's own date: units held before the session x (price - previous close
// in EUR) plus the units bought on or after the session date x (price - line
// price). The base for the percentage is the value of the units held before the
// session at the previous close. The previous close is converted with the
// previous close of the live FX pair (stored with the quote); without it the
// quote's own rate is used and FX_PREVIOUS_UNAVAILABLE is flagged.
func summarizeLines(lines []TransactionRow, quotes map[int64]*marketdata.Quote) *summaryAcc {
	acc := newSummaryAcc()
	p := purchasePerformance(lines)
	latest := map[int64]TransactionRow{}
	for _, l := range lines {
		latest[l.InstrumentID] = l
		acc.invested.Add(acc.invested, new(big.Rat).SetFrac64(l.a, 100))
	}
	million := big.NewInt(1000000)
	for _, series := range p.Series {
		id := series.InstrumentID
		held := p.held[id]
		if held.Sign() <= 0 {
			continue
		}
		acc.positions++
		purchase := latest[id]
		units := new(big.Rat).SetFrac(held, million)
		q := quotes[id]
		if q == nil || q.QuoteDate < purchase.Date {
			price, _ := new(big.Rat).SetString(purchase.Price)
			acc.value.Add(acc.value, new(big.Rat).Mul(units, price))
			acc.partial, acc.changePartial = true, true
			if q == nil {
				acc.reasons[ReasonNoQuote] = true
			} else {
				acc.reasons[ReasonPurchaseNewer] = true
			}
			continue
		}
		price, _ := new(big.Rat).SetString(q.Price)
		acc.value.Add(acc.value, new(big.Rat).Mul(units, price))
		acc.hasQuote = true
		acc.noteQuote(q)

		before := new(big.Int)
		for _, l := range lines {
			if l.InstrumentID != id {
				continue
			}
			if l.Date < q.QuoteDate {
				before.Add(before, big.NewInt(l.q))
				continue
			}
			// Bought in the session: gain since the purchase price.
			acc.hasChange = true
			delta := new(big.Rat).Sub(price, new(big.Rat).SetFrac64(l.p, 10000))
			acc.change.Add(acc.change, delta.Mul(delta, new(big.Rat).SetFrac64(l.q, 1000000)))
		}
		if before.Sign() == 0 {
			continue
		}
		prev, ok := previousClose(q)
		if !ok {
			acc.changePartial = true
			acc.reasons[ReasonNoPrevClose] = true
			continue
		}
		if prev.approximate {
			acc.changePartial = true
			acc.reasons[ReasonFXPrevMissing] = true
		}
		beforeUnits := new(big.Rat).SetFrac(before, million)
		acc.hasChange = true
		acc.change.Add(acc.change, new(big.Rat).Mul(beforeUnits, new(big.Rat).Sub(price, prev.eur)))
		acc.base.Add(acc.base, new(big.Rat).Mul(beforeUnits, prev.eur))
	}
	return acc
}

type prevClose struct {
	eur         *big.Rat // EUR price per unit at the previous close, rounded to 4 dp like a quote
	approximate bool     // converted at the quote's own FX rate
}

// previousClose converts the stored previous close of a quote to EUR.
func previousClose(q *marketdata.Quote) (prevClose, bool) {
	if q.PrevClose == "" {
		return prevClose{}, false
	}
	scaled, err := marketdata.Scaled(q.PrevClose, 4)
	if err != nil {
		return prevClose{}, false
	}
	if q.Currency == "EUR" {
		return prevClose{eur: new(big.Rat).SetFrac64(scaled, 10000)}, true
	}
	rate, approx := q.PrevFXRate, false
	if rate == "" {
		rate, approx = q.FXRate, true
	}
	if rate == "" {
		return prevClose{}, false
	}
	r, err := marketdata.Scaled(rate, 8)
	if err != nil {
		return prevClose{}, false
	}
	div := int64(1)
	if marketdata.MinorUnit(q.Currency) {
		div = 100
	}
	eur, err := marketdata.Convert(scaled, r, div)
	if err != nil {
		return prevClose{}, false
	}
	return prevClose{eur: new(big.Rat).SetFrac64(eur, 10000), approximate: approx}, true
}

// latestQuotes returns the newest stored quote of each instrument (by quote
// date, then fetch time): the same quote the dashboard's current value uses.
func (s *Store) latestQuotes(ctx context.Context, ids []int64) (map[int64]*marketdata.Quote, error) {
	out := make(map[int64]*marketdata.Quote, len(ids))
	for _, id := range ids {
		if _, done := out[id]; done {
			continue
		}
		q, err := scanQuote(s.db.QueryRowContext(ctx, "SELECT "+quoteColumns+" FROM price_quotes WHERE instrument_id=? ORDER BY quote_date DESC,fetched_at DESC,id DESC LIMIT 1", id))
		if err != nil {
			return nil, err
		}
		if q != nil {
			out[id] = q
		}
	}
	return out, nil
}

// Summaries returns the list-page summary of each of the user's portfolios and
// the combined total, which is aggregated before rounding.
func (s *Store) Summaries(ctx context.Context, user string) (Summaries, error) {
	portfolios, err := s.Portfolios(ctx, user)
	if err != nil {
		return Summaries{}, err
	}
	all := make([][]TransactionRow, len(portfolios))
	var ids []int64
	for i, p := range portfolios {
		if all[i], err = s.transactionRows(ctx, user, p.ID); err != nil {
			return Summaries{}, err
		}
		for _, l := range all[i] {
			ids = append(ids, l.InstrumentID)
		}
	}
	quotes, err := s.latestQuotes(ctx, ids)
	if err != nil {
		return Summaries{}, err
	}
	out := Summaries{Summaries: []PortfolioSummary{}}
	total := newSummaryAcc()
	for i, p := range portfolios {
		acc := summarizeLines(all[i], quotes)
		out.Summaries = append(out.Summaries, PortfolioSummary{PortfolioID: p.ID, Summary: acc.result()})
		total.add(acc)
	}
	out.Total = TotalSummary{PortfolioCount: len(portfolios), Summary: total.result()}
	return out, nil
}

// heldInstrumentIDs returns the de-duplicated instruments the user holds units
// of in any portfolio (instruments without transactions are excluded), by ID.
func (s *Store) heldInstrumentIDs(ctx context.Context, user string) ([]int64, error) {
	portfolios, err := s.Portfolios(ctx, user)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	ids := []int64{}
	for _, p := range portfolios {
		lines, err := s.transactionRows(ctx, user, p.ID)
		if err != nil {
			return nil, err
		}
		perf := purchasePerformance(lines)
		for _, series := range perf.Series {
			if perf.held[series.InstrumentID].Sign() > 0 && !seen[series.InstrumentID] {
				seen[series.InstrumentID] = true
				ids = append(ids, series.InstrumentID)
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// UserHeldInstruments lists the instruments to price for the user-level
// real-time job: the union of held instruments across all portfolios.
func (s *Store) UserHeldInstruments(ctx context.Context, user string) ([]SavingPlanItem, error) {
	ids, err := s.heldInstrumentIDs(ctx, user)
	if err != nil {
		return nil, err
	}
	out := make([]SavingPlanItem, 0, len(ids))
	for _, id := range ids {
		var v SavingPlanItem
		if err := s.db.QueryRowContext(ctx, "SELECT id,COALESCE(isin,''),type,ticker,name,COALESCE(av_symbol,''),COALESCE(yahoo_symbol,'') FROM instruments WHERE id=?", id).Scan(&v.InstrumentID, &v.ISIN, &v.Type, &v.Ticker, &v.Name, &v.AVSymbol, &v.YahooSymbol); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// NewestHeldFetchedAt is the fetch time of the newest latest-quote among the
// user's held instruments; false when none of them has a quote.
func (s *Store) NewestHeldFetchedAt(ctx context.Context, user string) (time.Time, bool, error) {
	ids, err := s.heldInstrumentIDs(ctx, user)
	if err != nil {
		return time.Time{}, false, err
	}
	quotes, err := s.latestQuotes(ctx, ids)
	if err != nil {
		return time.Time{}, false, err
	}
	var newest time.Time
	for _, q := range quotes {
		if q.FetchedAt.After(newest) {
			newest = q.FetchedAt
		}
	}
	return newest, !newest.IsZero(), nil
}
