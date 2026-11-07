package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"holdings/backend/internal/marketdata"
)

// StoredDividend is one cached dividend event. CloseBeforeE4 is 0 when unknown.
type StoredDividend struct {
	ExDate        string
	AmountE6      int64
	Currency      string
	CloseBeforeE4 int64
}

// DividendSync is the sync state of one instrument. Zero times mean "never".
type DividendSync struct {
	InstrumentID    int64
	SyncedFrom      string
	LastSyncedAt    time.Time
	LatestCloseE4   int64
	LatestCloseDate string
	Currency        string
	LastError       string
	LastErrorAt     time.Time
}

type holding struct {
	date string
	qty  int64 // units × 10^6
}

// DividendItem is a current portfolio item with its transaction lines.
type DividendItem struct {
	InstrumentID int64
	Ticker       string
	ISIN         string
	Name         string
	lines        []holding
}

// DividendSnapshot holds everything the calendar needs for one portfolio.
type DividendSnapshot struct {
	FirstTradeDate string // "" without transactions
	Items          []DividendItem
	Events         map[int64][]StoredDividend // ascending ex-date
	Sync           map[int64]DividendSync
}

// parseStamp reads a DATETIME column; the driver returns it as RFC 3339 text.
func parseStamp(v sql.NullString) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, v.String)
	if err != nil {
		t, _ = time.Parse(stamp, v.String)
	}
	return t
}

// DividendSnapshot loads the items currently in the portfolio, their lines and
// their cached dividend data. Other users' portfolios are ErrNotFound.
func (s *Store) DividendSnapshot(ctx context.Context, user string, id int64) (DividendSnapshot, error) {
	out := DividendSnapshot{Items: []DividendItem{}, Events: map[int64][]StoredDividend{}, Sync: map[int64]DividendSync{}}
	if _, err := s.Portfolio(ctx, user, id); err != nil {
		return out, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.ticker,COALESCE(i.isin,''),i.name FROM portfolio_items pi JOIN instruments i ON i.id=pi.instrument_id WHERE pi.portfolio_id=? ORDER BY i.id`, id)
	if err != nil {
		return out, err
	}
	index := map[int64]int{}
	for rows.Next() {
		var it DividendItem
		if err = rows.Scan(&it.InstrumentID, &it.Ticker, &it.ISIN, &it.Name); err != nil {
			rows.Close()
			return out, err
		}
		index[it.InstrumentID] = len(out.Items)
		out.Items = append(out.Items, it)
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT l.instrument_id,t.trade_date,l.quantity_e6 FROM transactions t JOIN transaction_lines l ON l.transaction_id=t.id WHERE t.portfolio_id=? ORDER BY t.trade_date`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item int64
		var h holding
		if err = rows.Scan(&item, &h.date, &h.qty); err != nil {
			rows.Close()
			return out, err
		}
		if out.FirstTradeDate == "" || h.date < out.FirstTradeDate {
			out.FirstTradeDate = h.date
		}
		if i, ok := index[item]; ok {
			out.Items[i].lines = append(out.Items[i].lines, h)
		}
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT e.instrument_id,e.ex_date,e.amount_e6,e.currency,COALESCE(e.close_before_e4,0) FROM dividend_events e JOIN portfolio_items pi ON pi.instrument_id=e.instrument_id WHERE pi.portfolio_id=? ORDER BY e.instrument_id,e.ex_date`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var item int64
		var e StoredDividend
		if err = rows.Scan(&item, &e.ExDate, &e.AmountE6, &e.Currency, &e.CloseBeforeE4); err != nil {
			rows.Close()
			return out, err
		}
		out.Events[item] = append(out.Events[item], e)
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT `+syncColumns+` FROM dividend_sync d JOIN portfolio_items pi ON pi.instrument_id=d.instrument_id WHERE pi.portfolio_id=?`, id)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		st, err := scanSync(rows)
		if err != nil {
			return out, err
		}
		out.Sync[st.InstrumentID] = st
	}
	return out, rows.Err()
}

const syncColumns = `d.instrument_id,d.synced_from,d.last_synced_at,COALESCE(d.latest_close_e4,0),COALESCE(d.latest_close_date,''),COALESCE(d.currency,''),COALESCE(d.last_error,''),d.last_error_at`

func scanSync(row interface{ Scan(...any) error }) (DividendSync, error) {
	var st DividendSync
	var synced, errAt sql.NullString
	err := row.Scan(&st.InstrumentID, &st.SyncedFrom, &synced, &st.LatestCloseE4, &st.LatestCloseDate, &st.Currency, &st.LastError, &errAt)
	st.LastSyncedAt, st.LastErrorAt = parseStamp(synced), parseStamp(errAt)
	return st, err
}

// DividendSyncState returns the sync state of an instrument, or nil.
func (s *Store) DividendSyncState(ctx context.Context, id int64) (*DividendSync, error) {
	st, err := scanSync(s.db.QueryRowContext(ctx, `SELECT `+syncColumns+` FROM dividend_sync d WHERE d.instrument_id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &st, nil
}

// SaveDividends stores a successful fetch that fully covers [from, to]: events
// are upserted (providers correct amounts), stored events in the range that the
// response no longer contains are deleted, and the sync state is updated.
func (s *Store) SaveDividends(ctx context.Context, id int64, h *marketdata.DividendHistory, provider, from, to string, at time.Time) error {
	fetched := at.UTC().Format(stamp)
	return s.write(ctx, func(c *sql.Conn) error {
		keep := map[string]bool{}
		for _, e := range h.Events {
			amount, err := marketdata.Scaled(e.AmountPerShare, 6)
			if err != nil || e.ExDate < from || e.ExDate > to {
				continue
			}
			var closeBefore any
			if v, err := marketdata.Scaled(h.CloseBefore[e.ExDate], 4); err == nil {
				closeBefore = v
			}
			if _, err = c.ExecContext(ctx, `INSERT INTO dividend_events(instrument_id,ex_date,amount_e6,currency,close_before_e4,provider,fetched_at) VALUES(?,?,?,?,?,?,?)
 ON CONFLICT(instrument_id,ex_date) DO UPDATE SET amount_e6=excluded.amount_e6,currency=excluded.currency,close_before_e4=COALESCE(excluded.close_before_e4,dividend_events.close_before_e4),provider=excluded.provider,fetched_at=excluded.fetched_at`,
				id, e.ExDate, amount, h.Currency, closeBefore, provider, fetched); err != nil {
				return err
			}
			keep[e.ExDate] = true
		}
		rows, err := c.QueryContext(ctx, "SELECT ex_date FROM dividend_events WHERE instrument_id=? AND ex_date>=? AND ex_date<=?", id, from, to)
		if err != nil {
			return err
		}
		var gone []string
		for rows.Next() {
			var day string
			if err = rows.Scan(&day); err != nil {
				rows.Close()
				return err
			}
			if !keep[day] {
				gone = append(gone, day)
			}
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for _, day := range gone {
			if _, err = c.ExecContext(ctx, "DELETE FROM dividend_events WHERE instrument_id=? AND ex_date=?", id, day); err != nil {
				return err
			}
		}
		var latest, latestDate any
		if v, err := marketdata.Scaled(h.LatestClose, 4); err == nil {
			latest, latestDate = v, h.LatestCloseDate
		}
		_, err = c.ExecContext(ctx, `INSERT INTO dividend_sync(instrument_id,synced_from,last_synced_at,latest_close_e4,latest_close_date,currency,last_error,last_error_at) VALUES(?,?,?,?,?,?,NULL,NULL)
 ON CONFLICT(instrument_id) DO UPDATE SET
  synced_from=CASE WHEN dividend_sync.last_synced_at IS NULL OR excluded.synced_from<dividend_sync.synced_from THEN excluded.synced_from ELSE dividend_sync.synced_from END,
  last_synced_at=excluded.last_synced_at,
  latest_close_e4=COALESCE(excluded.latest_close_e4,dividend_sync.latest_close_e4),
  latest_close_date=COALESCE(excluded.latest_close_date,dividend_sync.latest_close_date),
  currency=excluded.currency,last_error=NULL,last_error_at=NULL`,
			id, from, fetched, latest, latestDate, h.Currency)
		return err
	})
}

// RecordDividendError keeps the cached data and remembers why the last sync failed.
func (s *Store) RecordDividendError(ctx context.Context, id int64, from, message string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dividend_sync(instrument_id,synced_from,last_error,last_error_at) VALUES(?,?,?,?)
 ON CONFLICT(instrument_id) DO UPDATE SET last_error=excluded.last_error,last_error_at=excluded.last_error_at`, id, from, message, at.UTC().Format(stamp))
	return err
}
