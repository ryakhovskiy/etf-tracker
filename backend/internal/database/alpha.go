package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"holdings/backend/internal/marketdata"
)

var ErrSymbolInUse = errors.New("SYMBOL_IN_USE")
var ErrAlreadyLinked = errors.New("ALREADY_LINKED")

func (s *Store) AVBudget(ctx context.Context, user string, now time.Time, l marketdata.AVLimits) (marketdata.Budget, error) {
	b := marketdata.Budget{NextSlotAt: now, UserLimit: l.User}
	var global, personal int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN user_id=? THEN 1 ELSE 0 END),0) FROM av_requests WHERE requested_at>?`, user, now.UTC().Add(-24*time.Hour).Format("2006-01-02T15:04:05.000000000Z07:00")).Scan(&global, &personal)
	if err != nil {
		return b, err
	}
	b.GlobalRemaining = max(0, l.Day-global)
	b.UserRemaining = max(0, l.User-personal)
	var blocked string
	err = s.db.QueryRowContext(ctx, "SELECT v FROM av_state WHERE k='blocked_until'").Scan(&blocked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	if blocked != "" {
		t, e := time.Parse(time.RFC3339Nano, blocked)
		if e != nil {
			return b, e
		}
		if t.After(now) {
			b.BlockedUntil = &t
		}
	}

	var last string
	err = s.db.QueryRowContext(ctx, "SELECT requested_at FROM av_requests ORDER BY requested_at DESC LIMIT 1").Scan(&last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}

	if last != "" {
		t, e := time.Parse(time.RFC3339Nano, last)
		if e != nil {
			return b, e
		}
		b.NextSlotAt = maxTime(b.NextSlotAt, t.Add(time.Minute/time.Duration(l.Minute)))
	}
	// Preserve a full sliding-window bound when settings change across restarts.
	var oldest string
	err = s.db.QueryRowContext(ctx, `SELECT requested_at FROM av_requests WHERE requested_at>? ORDER BY requested_at DESC LIMIT 1 OFFSET ?`, now.UTC().Add(-time.Minute).Format("2006-01-02T15:04:05.000000000Z07:00"), l.Minute-1).Scan(&oldest)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return b, err
	}
	if oldest != "" {
		t, e := time.Parse(time.RFC3339Nano, oldest)
		if e != nil {
			return b, e
		}
		b.NextSlotAt = maxTime(b.NextSlotAt, t.Add(time.Minute))
	}
	// Daily limits can outlast the minute slot. Report when enough requests expire
	// for another call, including when a configured limit was lowered.
	for _, quota := range []struct {
		remaining, limit int
		personal         bool
	}{
		{b.GlobalRemaining, l.Day, false}, {b.UserRemaining, l.User, true},
	} {
		if quota.remaining > 0 {
			continue
		}
		query := "SELECT requested_at FROM av_requests WHERE requested_at>?"
		args := []any{now.UTC().Add(-24 * time.Hour).Format("2006-01-02T15:04:05.000000000Z07:00")}
		if quota.personal {
			query += " AND user_id=?"
			args = append(args, user)
		}
		query += " ORDER BY requested_at DESC LIMIT 1 OFFSET ?"
		args = append(args, quota.limit-1)
		var stamp string
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&stamp); err != nil {
			return b, err
		}
		t, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return b, err
		}
		b.NextSlotAt = maxTime(b.NextSlotAt, t.Add(24*time.Hour))
	}
	if b.BlockedUntil != nil {
		b.NextSlotAt = maxTime(b.NextSlotAt, *b.BlockedUntil)
	}
	return b, nil
}
func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
func (s *Store) ReserveAV(ctx context.Context, user, function string, now time.Time, l marketdata.AVLimits) error {
	return s.write(ctx, func(c *sql.Conn) error {
		var blocked string
		err := c.QueryRowContext(ctx, "SELECT v FROM av_state WHERE k='blocked_until'").Scan(&blocked)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if blocked != "" {
			t, e := time.Parse(time.RFC3339Nano, blocked)
			if e != nil {
				return e
			}
			if t.After(now) {
				return marketdata.ErrAVLimit
			}
		}
		var global, personal int
		if err = c.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN user_id=? THEN 1 ELSE 0 END),0) FROM av_requests WHERE requested_at>?`, user, now.UTC().Add(-24*time.Hour).Format("2006-01-02T15:04:05.000000000Z07:00")).Scan(&global, &personal); err != nil {
			return err
		}
		if global >= l.Day {
			return marketdata.ErrAVLimit
		}
		if personal >= l.User {
			return marketdata.ErrAVUserLimit
		}
		_, err = c.ExecContext(ctx, "INSERT INTO av_requests(requested_at,user_id,function) VALUES(?,?,?)", now.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"), user, function)
		return err
	})
}
func (s *Store) BlockAV(ctx context.Context, until time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO av_state(k,v) VALUES('blocked_until',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v", until.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"))
	return err
}
func (s *Store) CleanupAV(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM av_requests WHERE requested_at<?", now.UTC().Add(-48*time.Hour).Format("2006-01-02T15:04:05.000000000Z07:00"))
	return err
}
func scanQuote(row interface{ Scan(...any) error }) (*marketdata.Quote, error) {
	q := &marketdata.Quote{}
	var p, orig int64
	var rate sql.NullInt64
	var fetched string
	err := row.Scan(&q.ID, &q.InstrumentID, &p, &orig, &q.Currency, &rate, &q.FXRateDate, &q.QuoteDate, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	q.Price = decimalString(p, 4)
	q.OrigPrice = decimalString(orig, 4)
	if rate.Valid {
		q.FXRate = decimalString(rate.Int64, 8)
	}
	q.FetchedAt, err = time.Parse(time.RFC3339Nano, fetched)
	return q, err
}

const quoteColumns = `id,instrument_id,price_eur_e4,orig_price_e4,orig_currency,fx_rate_e8,COALESCE(fx_rate_date,''),quote_date,fetched_at`

func (s *Store) FreshQuote(ctx context.Context, id int64, now time.Time) (*marketdata.Quote, error) {
	q, err := scanQuote(s.db.QueryRowContext(ctx, "SELECT "+quoteColumns+" FROM price_quotes WHERE instrument_id=? ORDER BY fetched_at DESC,id DESC LIMIT 1", id))
	if err != nil || q == nil {
		return q, err
	}
	if marketdata.BerlinDay(q.FetchedAt) != marketdata.BerlinDay(now) {
		return nil, nil
	}
	return q, nil
}
func (s *Store) SaveQuote(ctx context.Context, q *marketdata.Quote, symbol string) error {
	return s.write(ctx, func(c *sql.Conn) error {
		var current string
		if err := c.QueryRowContext(ctx, "SELECT COALESCE(av_symbol,'') FROM instruments WHERE id=?", q.InstrumentID).Scan(&current); err != nil {
			return err
		}
		if current != symbol {
			return ErrAlreadyLinked
		}
		p, err := marketdata.Scaled(q.Price, 4)
		if err != nil {
			return err
		}
		orig, err := marketdata.Scaled(q.OrigPrice, 4)
		if err != nil {
			return err
		}
		var rate any
		if q.FXRate != "" {
			r, e := marketdata.Scaled(q.FXRate, 8)
			if e != nil {
				return e
			}
			rate = r
		}
		res, err := c.ExecContext(ctx, `INSERT INTO price_quotes(instrument_id,price_eur_e4,orig_price_e4,orig_currency,fx_rate_e8,fx_rate_date,quote_date,fetched_at) VALUES(?,?,?,?,?,?,?,?)`, q.InstrumentID, p, orig, q.Currency, rate, q.FXRateDate, q.QuoteDate, q.FetchedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00"))
		if err != nil {
			return err
		}
		q.ID, err = res.LastInsertId()
		return err
	})
}
func (s *Store) CachedFX(ctx context.Context, currency, date, provider string) (*marketdata.FXRate, error) {
	r := &marketdata.FXRate{}
	err := s.db.QueryRowContext(ctx, `SELECT rate_date,from_ccy,to_ccy,rate_e8 FROM fx_rates WHERE from_ccy=? AND to_ccy='EUR' AND provider=? AND rate_date<=? ORDER BY rate_date DESC LIMIT 1`, currency, provider, date).Scan(&r.Date, &r.Base, &r.Quote, &r.Rate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

const sqliteTimestamp = "2006-01-02 15:04:05"

// FXCoverage reports the newest stored publication date and the latest fetch time for a currency.
func (s *Store) FXCoverage(ctx context.Context, currency, provider string) (string, time.Time, error) {
	var newest, fetched string
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(rate_date),''),COALESCE(MAX(fetched_at),'') FROM fx_rates WHERE from_ccy=? AND to_ccy='EUR' AND provider=?`, currency, provider).Scan(&newest, &fetched)
	if err != nil || fetched == "" {
		return newest, time.Time{}, err
	}
	t, err := time.Parse(sqliteTimestamp, fetched)
	return newest, t, err
}
func (s *Store) SaveFX(ctx context.Context, rates []marketdata.FXRate, provider string, fetchedAt time.Time) error {
	return s.write(ctx, func(c *sql.Conn) error {
		for _, r := range rates {
			if r.Base == r.Quote {
				continue
			}
			if _, err := c.ExecContext(ctx, `INSERT OR REPLACE INTO fx_rates(rate_date,from_ccy,to_ccy,rate_e8,provider,fetched_at) VALUES(?,?,?,?,?,?)`, r.Date, r.Base, r.Quote, r.Rate, provider, fetchedAt.UTC().Format(sqliteTimestamp)); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *Store) LinkSymbol(ctx context.Context, user string, id int64, symbol, currency, region string) error {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	currency = strings.TrimSpace(currency)
	region = strings.TrimSpace(region)
	if symbol == "" || len(symbol) > 100 || len(currency) != 3 || len(region) > 100 {
		return invalidField("avSymbol", "Provide a symbol and three-letter currency.")
	}
	for _, r := range symbol {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(".-_^", r)) {
			return invalidField("avSymbol", "Invalid symbol.")
		}
	}
	return s.write(ctx, func(c *sql.Conn) error {
		var current string
		err := c.QueryRowContext(ctx, "SELECT COALESCE(av_symbol,'') FROM instruments WHERE id=?", id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current != "" {
			return ErrAlreadyLinked
		}
		var count int
		if err = c.QueryRowContext(ctx, "SELECT COUNT(*) FROM instruments WHERE av_symbol=?", symbol).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrSymbolInUse
		}
		_, err = c.ExecContext(ctx, `UPDATE instruments SET av_symbol=?,av_currency=?,av_region=?,av_linked_by=?,av_linked_at=CURRENT_TIMESTAMP WHERE id=?`, symbol, currency, region, user, id)
		return err
	})
}
func (s *Store) UnlinkSymbol(ctx context.Context, id int64) error {
	return s.write(ctx, func(c *sql.Conn) error {
		res, err := c.ExecContext(ctx, `UPDATE instruments SET av_symbol=NULL,av_currency=NULL,av_region=NULL,av_linked_by=NULL,av_linked_at=NULL WHERE id=?`, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		_, err = c.ExecContext(ctx, "DELETE FROM price_quotes WHERE instrument_id=?", id)
		return err
	})
}

// AVSearchTerm returns the default Alpha Vantage search keywords for an instrument.
func (s *Store) AVSearchTerm(ctx context.Context, id int64) (string, error) {
	var ticker, name string
	err := s.db.QueryRowContext(ctx, "SELECT ticker,name FROM instruments WHERE id=?", id).Scan(&ticker, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return marketdata.AVSearchTerm(ticker, name), err
}
func (s *Store) SaveAVInstrument(ctx context.Context, user string, r marketdata.Result) (Instrument, error) {
	var out Instrument
	if r.AVSymbol == "" || len(r.AVSymbol) > 100 || r.Name == "" || len(r.Name) > 500 || (r.Type != "STOCK" && r.Type != "ETF") || len(r.Currency) != 3 || len(r.Region) > 100 {
		return out, invalidField("instrument", "Invalid Alpha Vantage instrument.")
	}
	symbol := strings.ToUpper(strings.TrimSpace(r.AVSymbol))
	for _, c := range symbol {
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune(".-_^", c)) {
			return out, invalidField("instrument", "Invalid symbol.")
		}
	}
	err := s.write(ctx, func(c *sql.Conn) error {
		var id int64
		err := c.QueryRowContext(ctx, `SELECT id FROM instruments WHERE av_symbol=?`, symbol).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if id == 0 && r.ExistingInstrumentID != 0 {
			var current string
			if err = c.QueryRowContext(ctx, "SELECT id,COALESCE(av_symbol,'') FROM instruments WHERE id=?", r.ExistingInstrumentID).Scan(&id, &current); err != nil {
				return err
			}
			if current != "" && current != symbol {
				return ErrAlreadyLinked
			}
		}
		if id == 0 {
			res, e := c.ExecContext(ctx, `INSERT INTO instruments(type,name,ticker,source,av_symbol,av_currency,av_region,av_linked_by,av_linked_at) VALUES(?,?,?,'ALPHAVANTAGE',?,?,?,?,CURRENT_TIMESTAMP)`, r.Type, r.Name, symbol, symbol, r.Currency, r.Region, user)
			if e != nil {
				return e
			}
			id, e = res.LastInsertId()
			if e != nil {
				return e
			}
		} else {
			if _, err = c.ExecContext(ctx, `UPDATE instruments SET av_symbol=COALESCE(av_symbol,?),av_currency=COALESCE(av_currency,?),av_region=COALESCE(av_region,?),av_linked_by=COALESCE(av_linked_by,?),av_linked_at=COALESCE(av_linked_at,CURRENT_TIMESTAMP) WHERE id=?`, symbol, r.Currency, r.Region, user, id); err != nil {
				return err
			}
		}
		out.Currency = r.Currency
		return c.QueryRowContext(ctx, "SELECT id,type,name,ticker,COALESCE(isin,''),wkn FROM instruments WHERE id=?", id).Scan(&out.ID, &out.Type, &out.Name, &out.Ticker, &out.ISIN, &out.WKN)
	})
	return out, err
}
func (s *Store) applyQuoteDefaults(ctx context.Context, items []SavingPlanItem) error {
	for i := range items {
		v := &items[i]
		q, err := scanQuote(s.db.QueryRowContext(ctx, "SELECT "+quoteColumns+" FROM price_quotes WHERE instrument_id=? ORDER BY quote_date DESC,fetched_at DESC,id DESC LIMIT 1", v.InstrumentID))
		if err != nil {
			return err
		}
		if q == nil {
			continue
		}
		// PriceSource already contains the portfolio's most recent purchase date.
		last := ""
		if strings.HasPrefix(v.PriceSource, "Last purchase ") {
			d, e := time.Parse("02.01.2006", strings.TrimPrefix(v.PriceSource, "Last purchase "))
			if e == nil {
				last = d.Format("2006-01-02")
			}
		}
		if q.QuoteDate < last {
			continue
		}
		v.DefaultPrice = q.Price
		v.QuoteID = q.ID
		d, _ := time.Parse("2006-01-02", q.QuoteDate)
		v.PriceSource = "Fetched " + d.Format("02.01.2006")
		if q.FXRate != "" {
			rate, err := marketdata.Scaled(q.FXRate, 4)
			if err != nil {
				return err
			}
			fxDate, _ := time.Parse("2006-01-02", q.FXRateDate)
			currency := q.Currency
			if currency == "GBX" || currency == "GBp" {
				currency = "GBP"
			}
			v.PriceSource += " · 1 " + currency + " = " + strings.ReplaceAll(marketdata.Fixed(rate, 4), ".", ",") + " € (ECB " + fxDate.Format("02.01.2006") + ")"
		}
	}
	return nil
}
