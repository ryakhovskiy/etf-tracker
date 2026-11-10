package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"holdings/backend/internal/marketdata"
)

// LatestQuote is the most recent stored quote of an instrument, in EUR.
type LatestQuote struct {
	PriceEUR  string `json:"priceEur"`
	QuoteDate string `json:"quoteDate"`
	Provider  string `json:"provider"`
}

// PortfolioItemOverview is one item of a portfolio with the user's settings.
type PortfolioItemOverview struct {
	InstrumentID int64        `json:"instrumentId"`
	Ticker       string       `json:"ticker"`
	ISIN         *string      `json:"isin"`
	Name         string       `json:"name"`
	Type         string       `json:"type"`
	LatestQuote  *LatestQuote `json:"latestQuote"`
	PayDelayDays int          `json:"payDelayDays"`
}

// PortfolioItems lists the items of one of the user's portfolios with their
// latest stored quote (no provider call) and the user's pay-date delay.
func (s *Store) PortfolioItems(ctx context.Context, user string, id int64) ([]PortfolioItemOverview, error) {
	if _, err := s.Portfolio(ctx, user, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.ticker,COALESCE(i.isin,''),i.name,i.type,COALESCE(d.days,0)
		FROM portfolio_items pi JOIN instruments i ON i.id=pi.instrument_id
		LEFT JOIN instrument_pay_delays d ON d.instrument_id=i.id AND d.user_id=?
		WHERE pi.portfolio_id=? ORDER BY i.ticker COLLATE NOCASE,i.id`, user, id)
	if err != nil {
		return nil, err
	}
	out := []PortfolioItemOverview{}
	for rows.Next() {
		var it PortfolioItemOverview
		var isin string
		if err = rows.Scan(&it.InstrumentID, &it.Ticker, &isin, &it.Name, &it.Type, &it.PayDelayDays); err != nil {
			rows.Close()
			return nil, err
		}
		if isin != "" {
			it.ISIN = &isin
		}
		out = append(out, it)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		q, err := scanQuote(s.db.QueryRowContext(ctx, "SELECT "+quoteColumns+" FROM price_quotes WHERE instrument_id=? ORDER BY quote_date DESC,fetched_at DESC,id DESC LIMIT 1", out[i].InstrumentID))
		if err != nil {
			return nil, err
		}
		if q != nil {
			out[i].LatestQuote = &LatestQuote{PriceEUR: q.Price, QuoteDate: q.QuoteDate, Provider: q.Provider}
		}
	}
	return out, nil
}

// SetPayDelay stores the user's pay-date delay for an instrument held in one of
// their portfolios. Other instruments are ErrNotFound.
func (s *Store) SetPayDelay(ctx context.Context, user string, instrumentID int64, days int) error {
	if days < 0 || days > marketdata.MaxPayDelayDays {
		return invalidField("payDelayDays", fmt.Sprintf("Enter a whole number of days from 0 to %d.", marketdata.MaxPayDelayDays))
	}
	return s.write(ctx, func(c *sql.Conn) error {
		var held bool
		err := c.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM portfolio_items pi JOIN portfolios p ON p.id=pi.portfolio_id WHERE p.user_id=? AND pi.instrument_id=?)`, user, instrumentID).Scan(&held)
		if err != nil {
			return err
		}
		if !held {
			return ErrNotFound
		}
		_, err = c.ExecContext(ctx, `INSERT INTO instrument_pay_delays(user_id,instrument_id,days,updated_at) VALUES(?,?,?,?)
			ON CONFLICT(user_id,instrument_id) DO UPDATE SET days=excluded.days,updated_at=excluded.updated_at`,
			user, instrumentID, days, time.Now().UTC().Format(stamp))
		return err
	})
}

// payDelays returns the user's pay-date delays by instrument (absent = 0).
func (s *Store) payDelays(ctx context.Context, user string) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT instrument_id,days FROM instrument_pay_delays WHERE user_id=?`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var id int64
		var days int
		if err = rows.Scan(&id, &days); err != nil {
			return nil, err
		}
		out[id] = days
	}
	return out, rows.Err()
}
