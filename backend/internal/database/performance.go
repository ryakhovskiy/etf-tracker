package database

import (
	"context"
	"math/big"
	"time"
)

// Valuation contains cent-rounded EUR figures. A zero investment has no percentage.
type Valuation struct {
	Value          string  `json:"value"`
	Performance    string  `json:"performance"`
	PerformancePct *string `json:"performancePct"`
}
type CurrentItem struct {
	InstrumentID int64   `json:"instrumentId"`
	Units        string  `json:"units"`
	PriceEUR     string  `json:"priceEur"`
	Value        string  `json:"value"`
	Source       string  `json:"source"`
	Reason       string  `json:"reason,omitempty"`
	QuoteDate    string  `json:"quoteDate,omitempty"`
	Provider     string  `json:"provider,omitempty"`
	FXRate       *string `json:"fxRate"`
	FXRateDate   *string `json:"fxRateDate"`
}
type CurrentValuation struct {
	Valuation
	AsOfFrom      string        `json:"asOfFrom"`
	AsOfTo        string        `json:"asOfTo"`
	LastFetchedAt time.Time     `json:"lastFetchedAt"`
	Partial       bool          `json:"partial"`
	ChartPoint    bool          `json:"chartPoint"`
	Items         []CurrentItem `json:"items"`
}

func valuation(value, cost *big.Rat) Valuation {
	gain := new(big.Rat).Sub(value, cost)
	out := Valuation{Value: money(value), Performance: money(gain)}
	if cost.Sign() != 0 {
		pct := money(new(big.Rat).Mul(new(big.Rat).Quo(gain, cost), big.NewRat(100, 1)))
		out.PerformancePct = &pct
	}
	return out
}

// HoldingsValues returns only held items, valued at the chart's purchase prices.
// It deliberately does not use quote defaults when ordering a limited fetch job.
func (s *Store) HoldingsValues(ctx context.Context, user string, id int64) (map[int64]*big.Rat, error) {
	lines, err := s.transactionRows(ctx, user, id)
	if err != nil {
		return nil, err
	}
	p := purchasePerformance(lines)
	out := map[int64]*big.Rat{}
	for _, series := range p.Series {
		if p.held[series.InstrumentID].Sign() > 0 {
			out[series.InstrumentID] = p.heldValues[series.InstrumentID]
		}
	}
	return out, nil
}

func (s *Store) currentValuation(ctx context.Context, lines []TransactionRow, p Performance) (*CurrentValuation, error) {
	latest := map[int64]TransactionRow{}
	// transactionRows is sorted by trade date, creation time, transaction ID and line ID.
	for _, l := range lines {
		latest[l.InstrumentID] = l
	}
	out := &CurrentValuation{Items: []CurrentItem{}}
	total := new(big.Rat)
	hasQuote := false
	for _, series := range p.Series {
		id := series.InstrumentID
		held := p.held[id]
		if held.Sign() <= 0 {
			continue
		}
		purchase := latest[id]
		q, err := scanQuote(s.db.QueryRowContext(ctx, "SELECT "+quoteColumns+" FROM price_quotes WHERE instrument_id=? ORDER BY quote_date DESC,fetched_at DESC,id DESC LIMIT 1", id))
		if err != nil {
			return nil, err
		}
		units := new(big.Rat).SetFrac(held, big.NewInt(1000000))
		item := CurrentItem{InstrumentID: id, Units: units.FloatString(6), PriceEUR: purchase.Price, Source: "PURCHASE"}
		if q != nil && q.QuoteDate >= purchase.Date {
			item.Source, item.PriceEUR, item.QuoteDate, item.Provider = "QUOTE", q.Price, q.QuoteDate, q.Provider
			if q.FXRate != "" {
				rate, date := q.FXRate, q.FXRateDate
				item.FXRate, item.FXRateDate = &rate, &date
			}
			hasQuote = true
			if out.AsOfFrom == "" || q.QuoteDate < out.AsOfFrom {
				out.AsOfFrom = q.QuoteDate
			}
			if q.QuoteDate > out.AsOfTo {
				out.AsOfTo = q.QuoteDate
			}
			if q.FetchedAt.After(out.LastFetchedAt) {
				out.LastFetchedAt = q.FetchedAt
			}
		} else {
			out.Partial = true
			if q != nil {
				item.Reason = "PURCHASE_NEWER"
			} else {
				var symbol string
				if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(av_symbol,'') FROM instruments WHERE id=?", id).Scan(&symbol); err != nil {
					return nil, err
				}
				item.Reason = "NO_QUOTE"
				if symbol == "" {
					item.Reason = "NO_SYMBOL"
				}
			}
		}
		price, _ := new(big.Rat).SetString(item.PriceEUR)
		value := new(big.Rat).Mul(units, price)
		item.Value = money(value)
		total.Add(total, value)
		out.Items = append(out.Items, item)
	}
	if !hasQuote {
		return nil, nil
	}
	cost, _ := new(big.Rat).SetString(p.Invested)
	out.Valuation = valuation(total, cost)
	out.ChartPoint = len(p.Dates) > 0 && out.AsOfTo > p.Dates[len(p.Dates)-1]
	return out, nil
}

type PerformanceSeries struct {
	InstrumentID int64    `json:"instrumentId"`
	Name         string   `json:"name"`
	Values       []string `json:"values"`
}
type Performance struct {
	Dates      []string            `json:"dates"`
	Series     []PerformanceSeries `json:"series"`
	Totals     []string            `json:"totals"`
	Invested   string              `json:"invested"`
	AtPurchase Valuation           `json:"atPurchase"`
	Current    *CurrentValuation   `json:"current"`
	held       map[int64]*big.Int
	heldValues map[int64]*big.Rat
}

func (s *Store) Performance(ctx context.Context, user string, id int64) (Performance, error) {
	lines, err := s.transactionRows(ctx, user, id)
	result := Performance{Dates: []string{}, Series: []PerformanceSeries{}, Totals: []string{}, Invested: "0.00"}
	if err != nil {
		return result, err
	}
	result = purchasePerformance(lines)
	result.Current, err = s.currentValuation(ctx, lines, result)
	return result, err
}

func purchasePerformance(lines []TransactionRow) Performance {
	result := Performance{Dates: []string{}, Series: []PerformanceSeries{}, Totals: []string{}, Invested: "0.00"}
	indices := map[int64]int{}
	held := map[int64]*big.Int{}
	prices := map[int64]*big.Rat{}
	values := map[int64]*big.Rat{}
	invested := new(big.Int)
	lastValue := new(big.Rat)
	for _, l := range lines {
		if _, ok := indices[l.InstrumentID]; !ok {
			indices[l.InstrumentID] = len(result.Series)
			result.Series = append(result.Series, PerformanceSeries{l.InstrumentID, l.Name, []string{}})
			held[l.InstrumentID] = new(big.Int)
			prices[l.InstrumentID] = new(big.Rat)
		}
	}
	for start := 0; start < len(lines); {
		end := start
		weighted := map[int64]*big.Int{}
		quantities := map[int64]*big.Int{}
		for end < len(lines) && lines[end].Date == lines[start].Date {
			l := lines[end]
			if weighted[l.InstrumentID] == nil {
				weighted[l.InstrumentID] = new(big.Int)
				quantities[l.InstrumentID] = new(big.Int)
			}
			weighted[l.InstrumentID].Add(weighted[l.InstrumentID], new(big.Int).Mul(big.NewInt(l.q), big.NewInt(l.p)))
			quantities[l.InstrumentID].Add(quantities[l.InstrumentID], big.NewInt(l.q))
			held[l.InstrumentID].Add(held[l.InstrumentID], big.NewInt(l.q))
			invested.Add(invested, big.NewInt(l.a))
			end++
		}
		for item, w := range weighted {
			prices[item] = new(big.Rat).SetFrac(w, new(big.Int).Mul(quantities[item], big.NewInt(10000)))
		}
		result.Dates = append(result.Dates, lines[start].Date)
		total := new(big.Rat)
		for i := range result.Series {
			item := result.Series[i].InstrumentID
			value := new(big.Rat).Mul(new(big.Rat).SetFrac(held[item], big.NewInt(1000000)), prices[item])
			values[item] = value
			result.Series[i].Values = append(result.Series[i].Values, money(value))
			total.Add(total, value)
		}
		result.Totals = append(result.Totals, money(total))
		lastValue.Set(total)
		start = end
	}
	result.Invested = money(new(big.Rat).SetFrac(invested, big.NewInt(100)))
	result.AtPurchase = valuation(lastValue, new(big.Rat).SetFrac(invested, big.NewInt(100)))
	result.held = held
	result.heldValues = values
	return result
}
