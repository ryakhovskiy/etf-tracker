package database

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"holdings/backend/internal/marketdata"
)

// SetYahooSymbol caches the Yahoo symbol of an instrument. An empty symbol
// records that Yahoo did not know the ISIN at checkedAt.
func (s *Store) SetYahooSymbol(ctx context.Context, id int64, symbol string, checkedAt time.Time) error {
	res, err := s.db.ExecContext(ctx, "UPDATE instruments SET yahoo_symbol=NULLIF(?,''),yahoo_checked_at=? WHERE id=?", symbol, checkedAt.UTC().Format(stamp), id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: instruments.yahoo_symbol") {
			return ErrSymbolInUse
		}
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

var yahooSymbolPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9.\-=^]{0,29}$`)

// SetEPCurrency caches the Eulerpool listing currency of a stock.
func (s *Store) SetEPCurrency(ctx context.Context, id int64, currency string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE instruments SET ep_currency=NULLIF(?,'') WHERE id=?", currency, id)
	return err
}

// FillISIN sets the ISIN of an instrument that has none. It reports false when
// the instrument already has one or the ISIN belongs to another instrument.
func (s *Store) FillISIN(ctx context.Context, id int64, isin string) (bool, error) {
	isin = marketdata.Normalize(isin)
	if !marketdata.ValidISIN(isin) {
		return false, nil
	}
	filled := false
	err := s.write(ctx, func(c *sql.Conn) error {
		var other int64
		if err := c.QueryRowContext(ctx, "SELECT id FROM instruments WHERE isin=?", isin).Scan(&other); err == nil {
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		res, err := c.ExecContext(ctx, "UPDATE instruments SET isin=? WHERE id=? AND COALESCE(isin,'')=''", isin, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		filled = n > 0
		return err
	})
	return filled, err
}

// LinkYahooSymbol sets (or replaces) the Yahoo symbol of an instrument. Stored
// Yahoo history belongs to the old listing and is dropped.
func (s *Store) LinkYahooSymbol(ctx context.Context, id int64, symbol string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if !yahooSymbolPattern.MatchString(symbol) {
		return invalidField("symbol", "Invalid symbol.")
	}
	return s.write(ctx, func(c *sql.Conn) error {
		var other int64
		err := c.QueryRowContext(ctx, "SELECT id FROM instruments WHERE yahoo_symbol=? AND id<>?", symbol, id).Scan(&other)
		if err == nil {
			return ErrSymbolInUse
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var previous string
		if err = c.QueryRowContext(ctx, "SELECT COALESCE(yahoo_symbol,'') FROM instruments WHERE id=?", id).Scan(&previous); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		res, err := c.ExecContext(ctx, "UPDATE instruments SET yahoo_symbol=?,yahoo_checked_at=? WHERE id=?", symbol, time.Now().UTC().Format(stamp), id)
		if err != nil {
			return err
		}
		if n, e := res.RowsAffected(); e == nil && n == 0 {
			return ErrNotFound
		}
		if _, err = c.ExecContext(ctx, "DELETE FROM price_history WHERE instrument_id=? AND provider=?", id, marketdata.ProviderYahoo); err != nil {
			return err
		}
		// Dividend data belongs to the old listing (currency, amounts): sync again.
		if previous != symbol {
			for _, q := range []string{"DELETE FROM dividend_events WHERE instrument_id=?", "DELETE FROM dividend_sync WHERE instrument_id=?"} {
				if _, err = c.ExecContext(ctx, q, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// SaveYahooInstrument stores a Yahoo search hit. An instrument with the same
// Yahoo symbol, or (when the ISIN is known) the same ISIN, is reused and only
// has its empty fields filled; otherwise a new instrument is created.
func (s *Store) SaveYahooInstrument(ctx context.Context, r marketdata.Result, isin string) (Instrument, error) {
	symbol := strings.ToUpper(strings.TrimSpace(r.YahooSymbol))
	isin = marketdata.Normalize(isin)
	r.Name, r.Ticker = strings.TrimSpace(r.Name), strings.TrimSpace(r.Ticker)
	var out Instrument
	if !yahooSymbolPattern.MatchString(symbol) || r.Name == "" || len(r.Name) > 500 || (r.Type != "STOCK" && r.Type != "ETF") || (isin != "" && !marketdata.ValidISIN(isin)) {
		return out, invalidField("instrument", "Invalid Yahoo instrument.")
	}
	if r.Ticker == "" {
		r.Ticker = symbol
	}
	err := s.write(ctx, func(c *sql.Conn) error {
		var id int64
		err := c.QueryRowContext(ctx, "SELECT id FROM instruments WHERE yahoo_symbol=?", symbol).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if id == 0 && isin != "" {
			err = c.QueryRowContext(ctx, "SELECT id FROM instruments WHERE isin=?", isin).Scan(&id)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if id != 0 {
				// The same security under another listing: keep the first symbol.
				if _, err = c.ExecContext(ctx, "UPDATE instruments SET yahoo_symbol=COALESCE(yahoo_symbol,?),yahoo_checked_at=COALESCE(yahoo_checked_at,?) WHERE id=?", symbol, time.Now().UTC().Format(stamp), id); err != nil {
					return err
				}
			}
		}
		if id == 0 {
			res, e := c.ExecContext(ctx, `INSERT INTO instruments(type,name,ticker,isin,source,yahoo_symbol,yahoo_checked_at) VALUES(?,?,?,NULLIF(?,''),'YAHOO',?,?)`, r.Type, r.Name, r.Ticker, isin, symbol, time.Now().UTC().Format(stamp))
			if e != nil {
				return e
			}
			if id, e = res.LastInsertId(); e != nil {
				return e
			}
		}
		return c.QueryRowContext(ctx, "SELECT id,type,name,ticker,COALESCE(isin,''),wkn FROM instruments WHERE id=?", id).Scan(&out.ID, &out.Type, &out.Name, &out.Ticker, &out.ISIN, &out.WKN)
	})
	return out, err
}

// YahooSymbolExists reports whether an instrument already uses the symbol.
func (s *Store) YahooSymbolExists(ctx context.Context, symbol string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM instruments WHERE yahoo_symbol=?", strings.ToUpper(strings.TrimSpace(symbol))).Scan(&n)
	return n > 0, err
}

// InstrumentName returns the stored name of an instrument.
func (s *Store) InstrumentName(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, "SELECT name FROM instruments WHERE id=?", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}
