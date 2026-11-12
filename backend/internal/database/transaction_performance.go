package database

import (
	"context"
	"math/big"

	"holdings/backend/internal/marketdata"
)

type TransactionChange struct {
	Change    string  `json:"change"`
	ChangePct *string `json:"changePct"`
}

type TransactionLinePerformance struct {
	TransactionChange
	CurrentPrice   string  `json:"currentPrice"`
	QuoteDate      string  `json:"quoteDate"`
	PriceChange    string  `json:"priceChange"`
	PriceChangePct *string `json:"priceChangePct"`
}

type TransactionGroupPerformance struct {
	*TransactionChange
	PricedLines int `json:"pricedLines"`
	TotalLines  int `json:"totalLines"`
}

// Compute complete transaction totals before pagination. Only fetched quotes on
// or after a purchase date can value that line; a purchase price is no substitute.
func (s *Store) transactionPerformance(ctx context.Context, lines []TransactionRow) (map[int64]TransactionGroupPerformance, error) {
	quotes := map[int64]*marketdata.Quote{}
	type totals struct {
		value, cost *big.Rat
		priced, all int
	}
	groups := map[int64]*totals{}
	for i := range lines {
		line := &lines[i]
		if _, ok := quotes[line.InstrumentID]; !ok {
			q, err := scanQuote(s.db.QueryRowContext(ctx, "SELECT "+quoteColumns+" FROM price_quotes WHERE instrument_id=? ORDER BY quote_date DESC,fetched_at DESC,id DESC LIMIT 1", line.InstrumentID))
			if err != nil {
				return nil, err
			}
			quotes[line.InstrumentID] = q
		}
		group := groups[line.TransactionID]
		if group == nil {
			group = &totals{value: new(big.Rat), cost: new(big.Rat)}
			groups[line.TransactionID] = group
		}
		group.all++
		q := quotes[line.InstrumentID]
		if q == nil || q.QuoteDate < line.Date {
			continue
		}
		current, _ := new(big.Rat).SetString(q.Price)
		purchase := big.NewRat(line.p, 10000)
		quantity := big.NewRat(line.q, 1000000)
		value := new(big.Rat).Mul(current, quantity)
		cost := new(big.Rat).Mul(purchase, quantity)
		unit := valuation(current, purchase)
		change := valuation(value, cost)
		line.Performance = &TransactionLinePerformance{
			TransactionChange: TransactionChange{Change: change.Performance, ChangePct: change.PerformancePct},
			CurrentPrice:      q.Price, QuoteDate: q.QuoteDate,
			PriceChange: new(big.Rat).Sub(current, purchase).FloatString(4), PriceChangePct: unit.PerformancePct,
		}
		group.value.Add(group.value, value)
		group.cost.Add(group.cost, cost)
		group.priced++
	}
	result := make(map[int64]TransactionGroupPerformance, len(groups))
	for id, group := range groups {
		summary := TransactionGroupPerformance{PricedLines: group.priced, TotalLines: group.all}
		if group.priced > 0 {
			v := valuation(group.value, group.cost)
			summary.TransactionChange = &TransactionChange{Change: v.Performance, ChangePct: v.PerformancePct}
		}
		result[id] = summary
	}
	return result, nil
}
