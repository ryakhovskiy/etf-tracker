package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"holdings/backend/internal/marketdata"
)

// SetYahooSymbol caches the Yahoo symbol of an instrument. An empty symbol
// records that Yahoo did not know the ISIN at checkedAt.
func (s *Store) SetYahooSymbol(ctx context.Context, id int64, symbol string, checkedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, "UPDATE instruments SET yahoo_symbol=NULLIF(?,''),yahoo_checked_at=? WHERE id=?", symbol, checkedAt.UTC().Format(stamp), id)
	if err != nil {
		return err
	}
	if n, e := res.RowsAffected(); e == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// HistoryPoint is one stored daily close.
type HistoryPoint struct {
	Day      string `json:"date"`
	Close    string `json:"close"`
	Currency string `json:"currency"`
}

// SaveHistory upserts daily closes for an instrument.
func (s *Store) SaveHistory(ctx context.Context, id int64, symbol, currency, provider string, points []marketdata.YahooPoint, fetchedAt time.Time) error {
	return s.write(ctx, func(c *sql.Conn) error {
		for _, p := range points {
			v, err := marketdata.Scaled(p.Close, 4)
			if err != nil {
				continue
			}
			if _, err = c.ExecContext(ctx, `INSERT INTO price_history(instrument_id,day,close_e4,currency,provider,symbol,fetched_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(instrument_id,day,provider) DO UPDATE SET close_e4=excluded.close_e4,currency=excluded.currency,symbol=excluded.symbol,fetched_at=excluded.fetched_at`,
				id, p.Day, v, currency, provider, symbol, fetchedAt.UTC().Format(stamp)); err != nil {
				return err
			}
		}
		return nil
	})
}

// HistoryDay returns the stored close of exactly one day, or nil.
func (s *Store) HistoryDay(ctx context.Context, id int64, provider, day string) (*HistoryPoint, error) {
	var p HistoryPoint
	var v int64
	err := s.db.QueryRowContext(ctx, "SELECT day,close_e4,currency FROM price_history WHERE instrument_id=? AND provider=? AND day=?", id, provider, day).Scan(&p.Day, &v, &p.Currency)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	p.Close = decimalString(v, 4)
	return &p, err
}

// History returns stored closes in [from, to], ascending.
func (s *Store) History(ctx context.Context, id int64, provider, from, to string) ([]HistoryPoint, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT day,close_e4,currency FROM price_history WHERE instrument_id=? AND provider=? AND day>=? AND day<=? ORDER BY day", id, provider, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryPoint{}
	for rows.Next() {
		var p HistoryPoint
		var v int64
		if err = rows.Scan(&p.Day, &v, &p.Currency); err != nil {
			return nil, err
		}
		p.Close = decimalString(v, 4)
		out = append(out, p)
	}
	return out, rows.Err()
}
