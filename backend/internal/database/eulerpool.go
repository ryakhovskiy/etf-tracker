package database

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"strings"
	"time"

	"holdings/backend/internal/marketdata"
)

const stamp = "2006-01-02T15:04:05.000000000Z07:00"

// SetQuoteTTL sets how long a Eulerpool stock quote counts as fresh.
func (s *Store) SetQuoteTTL(d time.Duration) { s.quoteTTLNS.Store(int64(d)) }
func (s *Store) quoteTTL() time.Duration {
	if d := time.Duration(s.quoteTTLNS.Load()); d > 0 {
		return d
	}
	return 15 * time.Minute
}

func (s *Store) RecordProviderRequest(ctx context.Context, provider, user, function string, at time.Time, counted bool) error {
	var u any
	if user != "" {
		u = user
	}
	n := 0
	if counted {
		n = 1
	}
	_, err := s.db.ExecContext(ctx, "INSERT INTO provider_requests(provider,requested_at,user_id,function,counted) VALUES(?,?,?,?,?)", provider, at.UTC().Format(stamp), u, function, n)
	return err
}
func (s *Store) ProviderUsage(ctx context.Context, provider string, since time.Time) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM provider_requests WHERE provider=? AND counted=1 AND requested_at>?", provider, since.UTC().Format(stamp)).Scan(&n)
	return n, err
}
func (s *Store) ProviderState(ctx context.Context, provider, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT v FROM provider_state WHERE provider=? AND k=?", provider, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
func (s *Store) SetProviderState(ctx context.Context, provider, key, value string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO provider_state(provider,k,v) VALUES(?,?,?) ON CONFLICT(provider,k) DO UPDATE SET v=excluded.v", provider, key, value)
	return err
}

// InstrumentRef is what price fetching needs to know about an instrument.
type InstrumentRef struct {
	ID       int64
	ISIN     string
	Type     string
	Currency string // ETF profile currency, if known
	NAV      string // ETF profile NAV, if known; used to sanity-check price scale
	// Yahoo symbol resolved from the ISIN. Empty with a non-zero YahooCheckedAt
	// means Yahoo did not know the ISIN when it was last asked.
	YahooSymbol    string
	YahooCheckedAt time.Time
	EPCurrency     string // Eulerpool listing currency of a stock, cached
}

func (s *Store) InstrumentRef(ctx context.Context, id int64) (InstrumentRef, error) {
	r := InstrumentRef{ID: id}
	var checked sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(i.isin,''),i.type,COALESCE(p.currency,''),COALESCE(CAST(json_extract(p.raw,'$.nav') AS TEXT),''),COALESCE(i.yahoo_symbol,''),i.yahoo_checked_at,COALESCE(i.ep_currency,'') FROM instruments i LEFT JOIN etf_profiles p ON p.instrument_id=i.id WHERE i.id=?`, id).Scan(&r.ISIN, &r.Type, &r.Currency, &r.NAV, &r.YahooSymbol, &checked, &r.EPCurrency)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if err == nil && checked.Valid {
		r.YahooCheckedAt, _ = time.Parse(time.RFC3339Nano, checked.String)
	}
	return r, err
}

// SaveEulerpoolInstrument stores a Eulerpool search hit. Existing instruments
// (matched by ISIN) only have their empty fields filled.
func (s *Store) SaveEulerpoolInstrument(ctx context.Context, r marketdata.Result) (Instrument, error) {
	r.ISIN = marketdata.Normalize(r.ISIN)
	r.Ticker = strings.TrimSpace(r.Ticker)
	r.Name = strings.TrimSpace(r.Name)
	var out Instrument
	if !marketdata.ValidISIN(r.ISIN) || r.Name == "" || len(r.Name) > 500 || r.Ticker == "" || len(r.Ticker) > 100 || (r.Type != "STOCK" && r.Type != "ETF") {
		return out, invalidField("instrument", "Invalid Eulerpool instrument.")
	}
	err := s.write(ctx, func(c *sql.Conn) error {
		var id int64
		err := c.QueryRowContext(ctx, "SELECT id FROM instruments WHERE isin=?", r.ISIN).Scan(&id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if id == 0 {
			res, e := c.ExecContext(ctx, `INSERT INTO instruments(type,name,ticker,isin,source) VALUES(?,?,?,?,'EULERPOOL')`, r.Type, r.Name, r.Ticker, r.ISIN)
			if e != nil {
				return e
			}
			if id, e = res.LastInsertId(); e != nil {
				return e
			}
		} else if _, err = c.ExecContext(ctx, `UPDATE instruments SET name=COALESCE(NULLIF(name,''),?),ticker=COALESCE(NULLIF(ticker,''),?) WHERE id=?`, r.Name, r.Ticker, id); err != nil {
			return err
		}
		return c.QueryRowContext(ctx, "SELECT id,type,name,ticker,COALESCE(isin,''),wkn FROM instruments WHERE id=?", id).Scan(&out.ID, &out.Type, &out.Name, &out.Ticker, &out.ISIN, &out.WKN)
	})
	return out, err
}

// SaveETFProfile upserts a profile and fills empty instrument fields without overwriting.
func (s *Store) SaveETFProfile(ctx context.Context, id int64, p *marketdata.ETFProfile) error {
	return s.write(ctx, func(c *sql.Conn) error {
		var exists int
		if err := c.QueryRowContext(ctx, "SELECT COUNT(*) FROM instruments WHERE id=?", id).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			return ErrNotFound
		}
		null := func(v string) any {
			if v == "" {
				return nil
			}
			return v
		}
		_, err := c.ExecContext(ctx, `INSERT INTO etf_profiles(instrument_id,etf_company,tracking_index,asset_class,domicile,expense_ratio,aum_eur,dividend_yield,inception_date,currency,website,raw,fetched_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
 ON CONFLICT(instrument_id) DO UPDATE SET etf_company=excluded.etf_company,tracking_index=excluded.tracking_index,asset_class=excluded.asset_class,domicile=excluded.domicile,expense_ratio=excluded.expense_ratio,aum_eur=excluded.aum_eur,dividend_yield=excluded.dividend_yield,inception_date=excluded.inception_date,currency=excluded.currency,website=excluded.website,raw=excluded.raw,fetched_at=excluded.fetched_at`,
			id, null(p.ETFCompany), null(p.TrackingIndex), null(p.AssetClass), null(p.Domicile), null(p.ExpenseRatio), null(p.AUMEUR), null(p.DividendYield), null(p.InceptionDate), null(p.Currency), null(p.Website), string(p.Raw), p.FetchedAt.UTC().Format(stamp))
		if err != nil {
			return err
		}
		_, err = c.ExecContext(ctx, `UPDATE instruments SET name=COALESCE(NULLIF(name,''),NULLIF(?,'')),ticker=COALESCE(NULLIF(ticker,''),NULLIF(?,'')) WHERE id=?`, p.Name, p.Symbol, id)
		return err
	})
}

// ETFProfile returns the stored profile or ErrNotFound.
func (s *Store) ETFProfile(ctx context.Context, id int64) (*marketdata.ETFProfile, error) {
	p := &marketdata.ETFProfile{}
	var company, index, class, domicile, ter, aum, yield, inception, currency, website sql.NullString
	var fetched string
	err := s.db.QueryRowContext(ctx, `SELECT i.isin,etf_company,tracking_index,asset_class,domicile,expense_ratio,aum_eur,dividend_yield,inception_date,currency,website,fetched_at FROM etf_profiles e JOIN instruments i ON i.id=e.instrument_id WHERE e.instrument_id=?`, id).
		Scan(&p.ISIN, &company, &index, &class, &domicile, &ter, &aum, &yield, &inception, &currency, &website, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.ETFCompany, p.TrackingIndex, p.AssetClass, p.Domicile = company.String, index.String, class.String, domicile.String
	p.ExpenseRatio, p.AUMEUR, p.DividendYield, p.InceptionDate = ter.String, aum.String, yield.String, inception.String
	p.Currency, p.Website = currency.String, website.String
	p.FetchedAt, err = time.Parse(time.RFC3339Nano, fetched)
	return p, err
}

// AUMToEUR converts a decimal amount using a rate stored as 1 CCY = rate/1e8 EUR.
func AUMToEUR(aum string, rate int64) string {
	a, ok := new(big.Rat).SetString(aum)
	if !ok || rate <= 0 {
		return ""
	}
	a.Mul(a, new(big.Rat).SetFrac64(rate, 100000000))
	return a.FloatString(0)
}

// InstrumentSource reports where an instrument came from (SEED, EULERPOOL, ...).
func (s *Store) InstrumentSource(ctx context.Context, id int64) (string, error) {
	var src string
	err := s.db.QueryRowContext(ctx, "SELECT source FROM instruments WHERE id=?", id).Scan(&src)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return src, err
}

var ErrISINInUse = errors.New("ISIN_IN_USE")

// LinkISIN attaches an ISIN to an instrument that has none, so Eulerpool can price it.
func (s *Store) LinkISIN(ctx context.Context, id int64, isin string) error {
	isin = marketdata.Normalize(isin)
	if !marketdata.ValidISIN(isin) {
		return invalidField("isin", "Enter a valid ISIN.")
	}
	return s.write(ctx, func(c *sql.Conn) error {
		var current string
		err := c.QueryRowContext(ctx, "SELECT COALESCE(isin,'') FROM instruments WHERE id=?", id).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if current != "" {
			return ErrAlreadyLinked
		}
		var other int64
		err = c.QueryRowContext(ctx, "SELECT id FROM instruments WHERE isin=?", isin).Scan(&other)
		if err == nil {
			return ErrISINInUse
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = c.ExecContext(ctx, "UPDATE instruments SET isin=? WHERE id=?", isin, id)
		return err
	})
}
