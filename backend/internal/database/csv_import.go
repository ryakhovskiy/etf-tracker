package database

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

type ImportError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Preview *ImportPreview `json:"preview,omitempty"`
}

func (e *ImportError) Error() string { return e.Message }

type ImportInput struct {
	Rows     []ImportRow `json:"rows"`
	Settings CSVSettings `json:"settings"`
	Revision string      `json:"revision"`
}
type ImportResult struct {
	TransactionsCreated int `json:"transactionsCreated"`
	LinesCreated        int `json:"linesCreated"`
	InstrumentsCreated  int `json:"instrumentsCreated"`
	ItemsAdded          int `json:"itemsAdded"`
	Skipped             int `json:"skipped"`
}

func (s *Store) ParseImport(ctx context.Context, user string, id int64, data []byte, settings CSVSettings) (ImportPreview, error) {
	if _, err := s.Portfolio(ctx, user, id); err != nil {
		return ImportPreview{}, err
	}
	headers, rows, err := ParseCSV(data, settings)
	if err != nil {
		return ImportPreview{}, err
	}
	result, err := s.ValidateImport(ctx, user, id, rows)
	result.Headers = headers
	for i := range result.Rows {
		if result.Rows[i].Status == "ERROR" {
			result.Rows[i].Include = false
		}
	}
	result.Summary = summarizeImport(result.Rows, result.Summary.ItemsAfter-result.Summary.NewItems, s.limits.Items)
	// Include defaults are part of the confirmed snapshot.
	result.Revision = previewRevision(result)
	return result, err
}
func previewRevision(p ImportPreview) string {
	p.Revision = ""
	p.Headers = nil
	rows := make([]ImportRow, len(p.Rows))
	copy(rows, p.Rows)
	for i := range rows {
		rows[i].Raw = nil
	}
	p.Rows = rows
	b, _ := json.Marshal(p)
	return HashToken(string(b))
}
func (s *Store) ValidateImport(ctx context.Context, user string, id int64, rows []ImportRow) (ImportPreview, error) {
	var result ImportPreview
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return result, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN"); err != nil {
		return result, err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	return s.validateImport(ctx, conn, user, id, rows)
}
func (s *Store) validateImport(ctx context.Context, conn *sql.Conn, user string, id int64, input []ImportRow) (ImportPreview, error) {
	result := ImportPreview{Rows: []ImportRow{}, Summary: ImportSummary{ItemLimit: s.limits.Items}}
	if err := owns(ctx, conn, user, id); err != nil {
		return result, err
	}
	if len(input) > CSVMaxRows {
		return result, csvError("At most 5,000 rows are allowed.")
	}
	byISIN, byWKN := map[string]Instrument{}, map[string]Instrument{}
	rs, err := conn.QueryContext(ctx, `SELECT id,type,name,ticker,COALESCE(isin,''),wkn,EXISTS(SELECT 1 FROM portfolio_items WHERE portfolio_id=? AND instrument_id=i.id) FROM instruments i`, id)
	if err != nil {
		return result, err
	}
	for rs.Next() {
		var v Instrument
		if err = rs.Scan(&v.ID, &v.Type, &v.Name, &v.Ticker, &v.ISIN, &v.WKN, &v.InPortfolio); err != nil {
			rs.Close()
			return result, err
		}
		if v.ISIN != "" {
			byISIN[strings.ToUpper(v.ISIN)] = v
		}
		if v.WKN != nil {
			byWKN[strings.ToUpper(*v.WKN)] = v
		}
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return result, err
	}
	existing := map[string]bool{}
	rs, err = conn.QueryContext(ctx, `SELECT t.trade_date,l.instrument_id,l.quantity_e6,l.price_eur_e4 FROM transactions t JOIN transaction_lines l ON l.transaction_id=t.id WHERE t.portfolio_id=?`, id)
	if err != nil {
		return result, err
	}
	key := func(date, identity string, q, p int64) string {
		return fmt.Sprintf("%s|%s|%d|%d", date, identity, q, p)
	}
	for rs.Next() {
		var date string
		var instrument, q, p int64
		if err = rs.Scan(&date, &instrument, &q, &p); err != nil {
			rs.Close()
			return result, err
		}
		existing[key(date, fmt.Sprint(instrument), q, p)] = true
	}
	err = rs.Err()
	rs.Close()
	if err != nil {
		return result, err
	}
	var current int
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM portfolio_items WHERE portfolio_id=?", id).Scan(&current); err != nil {
		return result, err
	}
	newNames := map[string]string{}
	newWKN := map[string]string{}
	rowNumbers := map[int]bool{}
	for _, in := range input {
		r := in
		r.Errors = map[string]string{}
		r.Warnings = []string{}
		r.Instrument = nil
		r.Status = "OK"
		if r.RowNumber < 1 || rowNumbers[r.RowNumber] {
			return result, csvError("Row numbers must be positive and unique.")
		}
		rowNumbers[r.RowNumber] = true
		f := &r.Fields
		f.ISIN = strings.ToUpper(strings.TrimSpace(f.ISIN))
		f.WKN = strings.ToUpper(strings.TrimSpace(f.WKN))
		f.Name = strings.TrimSpace(f.Name)
		f.Ticker = strings.TrimSpace(f.Ticker)
		if !choice(r.Decision, "", "skip", "import") {
			r.Errors["decision"] = "Choose Skip or Import."
		}
		for _, field := range []struct {
			name, raw string
			scale     int
		}{{"quantity", f.Quantity, 6}, {"price", f.Price, 4}, {"total", f.Total, 2}} {
			if e := numberError(field.raw, field.scale); e != "" {
				r.Errors[field.name] = e
			}
		}
		d, e := time.Parse("2006-01-02", f.Date)
		if e != nil || f.Date < "1990-01-01" || d.After(time.Now().UTC()) {
			r.Errors["date"] = "Choose a date from 1990-01-01 through today."
		}
		q, _ := parseDecimal(f.Quantity, 6)
		p, _ := parseDecimal(f.Price, 4)
		a, _ := parseDecimal(f.Total, 2)
		if q > 0 && p > 0 && a > 0 {
			expected := new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(q), big.NewInt(p)), big.NewInt(10000000000))
			diff := new(big.Rat).Abs(new(big.Rat).Sub(expected, big.NewRat(a, 100)))
			threshold := big.NewRat(a, 100000)
			if threshold.Cmp(big.NewRat(5, 100)) < 0 {
				threshold = big.NewRat(5, 100)
			}
			if diff.Cmp(threshold) > 0 {
				r.Warnings = append(r.Warnings, "Total differs from Amount × Price.")
			}
		}
		if f.WKN != "" {
			if len(f.WKN) != 6 {
				r.Errors["wkn"] = "WKN must contain six letters or digits."
			}
			for _, c := range f.WKN {
				if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
					r.Errors["wkn"] = "WKN must contain six letters or digits."
				}
			}
		}
		isin, hasISIN := byISIN[f.ISIN]
		wkn, hasWKN := byWKN[f.WKN]
		if hasISIN && hasWKN && isin.ID != wkn.ID {
			r.Errors["isin"] = "ISIN and WKN refer to different instruments."
		}
		identity := "new:" + f.ISIN
		if hasISIN || hasWKN {
			v := isin
			if !hasISIN {
				v = wkn
			}
			r.Instrument = &v
			identity = fmt.Sprint(v.ID)
			if !v.InPortfolio {
				r.Status = "WILL_ADD_TO_PORTFOLIO"
			}
			if f.WKN != "" && v.WKN != nil && *v.WKN != "" && *v.WKN != f.WKN {
				r.Warnings = append(r.Warnings, "WKN differs from database ("+*v.WKN+")")
			}
			f.Ticker = v.Ticker
			f.Name = v.Name
			f.Type = v.Type
		} else if !validISIN(f.ISIN) {
			r.Errors["isin"] = "Unknown instrument. Add a valid ISIN or add it via search first."
		} else {
			r.Status = "NEW_INSTRUMENT"
			if f.Ticker == "" {
				f.Ticker = f.ISIN
			}
			if f.Name == "" {
				f.Name = f.ISIN
			}
			if f.Type == "" {
				f.Type = "STOCK"
				upper := strings.ToUpper(f.Name)
				if strings.Contains(upper, "ETF") || strings.Contains(upper, "UCITS") || strings.Contains(upper, "ETC") {
					f.Type = "ETF"
				}
			}
			if !choice(f.Type, "STOCK", "ETF") {
				r.Errors["type"] = "Choose STOCK or ETF."
			}
			if len(f.Name) > 500 || len(f.Ticker) > 100 {
				r.Errors["name"] = "Name or symbol is too long."
			}
			if first, ok := newNames[f.ISIN]; ok && first != f.Name {
				r.Warnings = append(r.Warnings, "Name differs between rows for this new ISIN.")
			} else if !ok {
				newNames[f.ISIN] = f.Name
			}
			if f.WKN != "" {
				if other, ok := newWKN[f.WKN]; ok && other != f.ISIN {
					r.Errors["wkn"] = "WKN is used by another new instrument."
				}
				newWKN[f.WKN] = f.ISIN
			}
		}
		k := key(f.Date, identity, q, p)
		if len(r.Errors) == 0 {
			if existing[k] {
				r.Status = "DUPLICATE"
			}
			existing[k] = true
		} else {
			r.Status = "ERROR"
		}
		result.Rows = append(result.Rows, r)
	}
	// The cap counts distinct new instruments in the entire import preview.
	if len(newNames) > 100 {
		for i := range result.Rows {
			r := &result.Rows[i]
			if r.Instrument == nil && validISIN(r.Fields.ISIN) {
				r.Status = "ERROR"
				r.Errors["isin"] = "At most 100 new instruments per import."
			}
		}
	}
	result.Summary = summarizeImport(result.Rows, current, s.limits.Items)
	result.Revision = previewRevision(result)
	return result, nil
}
func summarizeImport(rows []ImportRow, current, limit int) ImportSummary {
	summary := ImportSummary{Rows: len(rows), ItemLimit: limit}
	instruments, items, dates := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		if r.Status == "ERROR" {
			summary.Errors++
		}
		if !r.Include {
			continue
		}
		summary.Included++
		if r.Status == "DUPLICATE" && r.Decision == "" {
			summary.UndecidedDuplicates++
		}
		if r.Status == "ERROR" || r.Status == "DUPLICATE" && r.Decision == "skip" {
			continue
		}
		summary.Lines++
		dates[r.Fields.Date] = true
		if r.Instrument == nil {
			instruments[r.Fields.ISIN] = true
			items["new:"+r.Fields.ISIN] = true
		} else if !r.Instrument.InPortfolio {
			items[fmt.Sprint(r.Instrument.ID)] = true
		}
	}
	summary.NewInstruments = len(instruments)
	summary.NewItems = len(items)
	summary.Transactions = len(dates)
	summary.ItemsAfter = current + len(items)
	return summary
}
func (s *Store) CommitImport(ctx context.Context, user string, id int64, in ImportInput) (ImportResult, error) {
	result := ImportResult{}
	if err := in.Settings.Validate(); err != nil {
		return result, err
	}
	err := s.write(ctx, func(conn *sql.Conn) error {
		preview, err := s.validateImport(ctx, conn, user, id, in.Rows)
		if err != nil {
			return err
		}
		if preview.Summary.ItemsAfter > s.limits.Items {
			return limitError("ITEM_LIMIT_REACHED", s.limits.Items, preview.Summary.ItemsAfter-preview.Summary.NewItems)
		}
		if in.Revision == "" || in.Revision != preview.Revision {
			return &ImportError{"IMPORT_STALE", "The import has changed. Review the updated preview.", &preview}
		}
		for _, r := range preview.Rows {
			if r.Include && (r.Status == "ERROR" || r.Status == "DUPLICATE" && r.Decision == "") {
				return csvError("Resolve included errors and duplicate decisions before importing.")
			}
		}
		if preview.Summary.Lines == 0 {
			return csvError("Include at least one importable row.")
		}
		instruments := map[string]int64{}
		transactions := map[string]int64{}
		// First occurrence defines metadata, even when a later row is the included one.
		first := map[string]ImportFields{}
		for _, r := range preview.Rows {
			if _, ok := first[r.Fields.ISIN]; !ok && r.Instrument == nil {
				first[r.Fields.ISIN] = r.Fields
			}
		}
		for _, r := range preview.Rows {
			if !r.Include || r.Status == "DUPLICATE" && r.Decision == "skip" {
				result.Skipped++
				continue
			}
			f := r.Fields
			var instrument int64
			if r.Instrument != nil {
				instrument = r.Instrument.ID
			} else if cached := instruments[f.ISIN]; cached != 0 {
				instrument = cached
			} else {
				meta := first[f.ISIN]
				res, err := conn.ExecContext(ctx, "INSERT INTO instruments(type,name,ticker,isin,wkn,source) VALUES (?,?,?,?,NULLIF(?,''),'CSV')", meta.Type, meta.Name, meta.Ticker, f.ISIN, meta.WKN)
				if err != nil {
					return err
				}
				instrument, err = res.LastInsertId()
				if err != nil {
					return err
				}
				instruments[f.ISIN] = instrument
				result.InstrumentsCreated++
			}
			if f.WKN != "" {
				if _, err := conn.ExecContext(ctx, "UPDATE instruments SET wkn=? WHERE id=? AND (wkn IS NULL OR wkn='')", f.WKN, instrument); err != nil {
					return err
				}
			}
			res, err := conn.ExecContext(ctx, "INSERT OR IGNORE INTO portfolio_items(portfolio_id,instrument_id) VALUES (?,?)", id, instrument)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			result.ItemsAdded += int(n)
			txID := transactions[f.Date]
			if txID == 0 {
				res, err := conn.ExecContext(ctx, "INSERT INTO transactions(portfolio_id,trade_date,source) VALUES (?,?,'CSV')", id, f.Date)
				if err != nil {
					return err
				}
				txID, err = res.LastInsertId()
				if err != nil {
					return err
				}
				transactions[f.Date] = txID
				result.TransactionsCreated++
			}
			a, _ := parseDecimal(f.Total, 2)
			q, _ := parseDecimal(f.Quantity, 6)
			p, _ := parseDecimal(f.Price, 4)
			if _, err := conn.ExecContext(ctx, "INSERT INTO transaction_lines(transaction_id,instrument_id,amount_eur_e2,quantity_e6,price_eur_e4) VALUES (?,?,?,?,?)", txID, instrument, a, q, p); err != nil {
				return err
			}
			result.LinesCreated++
		}
		_, err = saveCSVSettings(ctx, conn, user, in.Settings)
		return err
	})
	if err != nil {
		return ImportResult{}, err
	}
	return result, nil
}
