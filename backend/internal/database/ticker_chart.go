package database

import (
	"context"
	"database/sql"
	"errors"

	"holdings/backend/internal/marketdata"
)

// TickerInstrument identifies the title of a ticker chart.
type TickerInstrument struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	ISIN   string `json:"isin"`
	Ticker string `json:"ticker"`
}

// TickerBuy is one BUY line of the title in one of the user's portfolios. The
// price is the EUR price paid per unit as stored with the line.
type TickerBuy struct {
	Day           string `json:"day"`
	Units         string `json:"units"`
	PriceEUR      string `json:"priceEur"`
	PortfolioID   int64  `json:"portfolioId"`
	PortfolioName string `json:"portfolioName"`
}

// TickerLatest is the latest stored quote of the title with the same figures a
// favorites tile shows (the null/Reasons rules are those of Favorite).
type TickerLatest struct {
	PriceEUR *string         `json:"priceEur"`
	Change   *FavoriteChange `json:"change"`
	AsOf     *SummaryAsOf    `json:"asOf"`
	Reasons  []string        `json:"reasons"`
}

// TickerBase is everything a ticker chart needs from the store except the daily
// closes: the title, its buys and its latest quote (nil when none is stored).
type TickerBase struct {
	Instrument TickerInstrument
	Buys       []TickerBuy
	Latest     TickerLatest
	Quote      *marketdata.Quote
	// Favorite is whether the user has starred the title.
	Favorite bool
}

// TickerBase loads the chart basics of an instrument held in one of the user's
// portfolios. Any other instrument, including another user's, is ErrNotFound.
func (s *Store) TickerBase(ctx context.Context, user string, id int64) (TickerBase, error) {
	out := TickerBase{Instrument: TickerInstrument{ID: id}, Buys: []TickerBuy{}}
	err := s.db.QueryRowContext(ctx, `SELECT i.name,COALESCE(i.isin,''),i.ticker FROM instruments i
		WHERE i.id=? AND EXISTS(SELECT 1 FROM portfolio_items pi JOIN portfolios p ON p.id=pi.portfolio_id WHERE p.user_id=? AND pi.instrument_id=i.id)`,
		id, user).Scan(&out.Instrument.Name, &out.Instrument.ISIN, &out.Instrument.Ticker)
	if errors.Is(err, sql.ErrNoRows) {
		return TickerBase{}, ErrNotFound
	}
	if err != nil {
		return TickerBase{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT t.trade_date,l.quantity_e6,l.price_eur_e4,p.id,p.name
		FROM transactions t JOIN transaction_lines l ON l.transaction_id=t.id JOIN portfolios p ON p.id=t.portfolio_id
		WHERE p.user_id=? AND l.instrument_id=? AND t.side='BUY' ORDER BY t.trade_date,t.created_at,t.id,l.id`, user, id)
	if err != nil {
		return TickerBase{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var b TickerBuy
		var units, price int64
		if err = rows.Scan(&b.Day, &units, &price, &b.PortfolioID, &b.PortfolioName); err != nil {
			return TickerBase{}, err
		}
		b.Units, b.PriceEUR = decimalString(units, 6), decimalString(price, 4)
		out.Buys = append(out.Buys, b)
	}
	if err = rows.Err(); err != nil {
		return TickerBase{}, err
	}
	if err = s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM favorites WHERE user_id=? AND instrument_id=?)", user, id).Scan(&out.Favorite); err != nil {
		return TickerBase{}, err
	}
	quotes, err := s.latestQuotes(ctx, []int64{id})
	if err != nil {
		return TickerBase{}, err
	}
	out.Quote = quotes[id]
	var f Favorite
	favoriteFigures(&f, out.Quote)
	out.Latest = TickerLatest{PriceEUR: f.PriceEUR, Change: f.Change, AsOf: f.AsOf, Reasons: f.Reasons}
	return out, nil
}
