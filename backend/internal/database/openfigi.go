package database

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"

	"holdings/backend/internal/marketdata"
)

// MatchOnline adds local identity and membership without matching tickers across venues.
func (s *Store) MatchOnline(ctx context.Context, user string, portfolio int64, rows []marketdata.Result) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	if portfolio != 0 {
		if err = owns(ctx, conn, user, portfolio); err != nil {
			return err
		}
	}
	for i := range rows {
		r := &rows[i]
		err = conn.QueryRowContext(ctx, `SELECT id,EXISTS(SELECT 1 FROM portfolio_items WHERE portfolio_id=? AND instrument_id=i.id) FROM instruments i WHERE figi=NULLIF(?,'') OR isin=NULLIF(?,'') OR wkn=NULLIF(?,'') OR av_symbol=NULLIF(?,'') OR yahoo_symbol=NULLIF(?,'') ORDER BY id LIMIT 1`, portfolio, r.FIGI, r.ISIN, r.WKN, r.AVSymbol, r.YahooSymbol).Scan(&r.ExistingInstrumentID, &r.InPortfolio)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

var figiPattern = regexp.MustCompile(`^BBG[A-Z0-9]{9}$`)
var onlineWKNPattern = regexp.MustCompile(`^[A-Z0-9]{6}$`)

func (s *Store) SaveOnlineInstrument(ctx context.Context, r marketdata.Result) (Instrument, error) {
	r.FIGI = marketdata.Normalize(r.FIGI)
	r.ISIN = marketdata.Normalize(r.ISIN)
	r.WKN = marketdata.Normalize(r.WKN)
	r.ExchCode = marketdata.Normalize(r.ExchCode)
	r.Ticker = marketdata.Normalize(r.Ticker)
	r.Name = strings.TrimSpace(r.Name)
	var out Instrument
	if !figiPattern.MatchString(r.FIGI) || !marketdata.GermanCodes[r.ExchCode] || r.Name == "" || len(r.Name) > 500 || r.Ticker == "" || len(r.Ticker) > 100 || (r.Type != "STOCK" && r.Type != "ETF") || (r.Currency != "" && r.Currency != "EUR") || (r.ISIN != "" && !marketdata.ValidISIN(r.ISIN)) || (r.WKN != "" && !onlineWKNPattern.MatchString(r.WKN)) {
		return out, invalidField("instrument", "Invalid online instrument.")
	}
	err := s.write(ctx, func(conn *sql.Conn) error {
		rows, err := conn.QueryContext(ctx, `SELECT id FROM instruments WHERE figi=? OR isin=NULLIF(?,'') OR wkn=NULLIF(?,'')`, r.FIGI, r.ISIN, r.WKN)
		if err != nil {
			return err
		}
		var id int64
		for rows.Next() {
			var found int64
			if err = rows.Scan(&found); err != nil {
				rows.Close()
				return err
			}
			if id != 0 && id != found {
				rows.Close()
				return invalidField("instrument", "Identifiers refer to different instruments.")
			}
			id = found
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if id == 0 {
			res, err := conn.ExecContext(ctx, `INSERT INTO instruments(type,name,ticker,isin,wkn,figi,exch_code,source) VALUES(?,?,?,NULLIF(?,''),NULLIF(?,''),?,?,'OPENFIGI')`, r.Type, r.Name, r.Ticker, r.ISIN, r.WKN, r.FIGI, r.ExchCode)
			if err != nil {
				return err
			}
			id, err = res.LastInsertId()
			if err != nil {
				return err
			}
		} else {
			_, err = conn.ExecContext(ctx, `UPDATE instruments SET type=COALESCE(NULLIF(type,''),?),name=COALESCE(NULLIF(name,''),?),ticker=COALESCE(NULLIF(ticker,''),?),isin=COALESCE(NULLIF(isin,''),NULLIF(?,'')),wkn=COALESCE(NULLIF(wkn,''),NULLIF(?,'')),figi=COALESCE(NULLIF(figi,''),?),exch_code=COALESCE(NULLIF(exch_code,''),?) WHERE id=?`, r.Type, r.Name, r.Ticker, r.ISIN, r.WKN, r.FIGI, r.ExchCode, id)
			if err != nil {
				return err
			}
		}
		out.Currency = "EUR"
		return conn.QueryRowContext(ctx, `SELECT id,type,name,ticker,COALESCE(isin,''),wkn,COALESCE(figi,''),COALESCE(exch_code,'') FROM instruments WHERE id=?`, id).Scan(&out.ID, &out.Type, &out.Name, &out.Ticker, &out.ISIN, &out.WKN, &out.FIGI, &out.ExchCode)
	})
	return out, err
}
