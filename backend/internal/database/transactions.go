package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"holdings/backend/internal/marketdata"
	"math/big"
	"regexp"
	"strings"
	"time"
)

var ErrItemHasTransactions = errors.New("This item has transactions. Delete its transactions first.")

type ValidationError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields"`
}

func (e *ValidationError) Error() string { return e.Message }
func invalidField(field, message string) error {
	return &ValidationError{"VALIDATION_ERROR", "Check the highlighted fields.", map[string]string{field: message}}
}

type TransactionLine struct {
	QuoteID      int64  `json:"quoteId,omitempty"`
	OrigPrice    string `json:"origPrice,omitempty"`
	OrigCurrency string `json:"origCurrency,omitempty"`
	FXRate       string `json:"fxRate,omitempty"`
	FXRateDate   string `json:"fxRateDate,omitempty"`
	InstrumentID int64  `json:"instrumentId"`
	Amount       string `json:"amount"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
}
type TransactionInput struct {
	Date  string            `json:"date"`
	Lines []TransactionLine `json:"lines"`
	Force bool              `json:"force"`
}
type DuplicateError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Lines   []TransactionLine `json:"lines"`
}

func (e *DuplicateError) Error() string { return e.Message }

var decimalPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)

// API decimals are canonical dot-separated strings. UI comma normalization happens in the client.
func parseDecimal(raw string, scale int) (int64, error) {
	if len(raw) > 32 || !decimalPattern.MatchString(raw) {
		return 0, errors.New("Use a positive decimal with a dot separator.")
	}
	parts := strings.Split(raw, ".")
	if len(parts) == 2 && len(parts[1]) > scale {
		return 0, fmt.Errorf("Use at most %d decimal places.", scale)
	}
	r, ok := new(big.Rat).SetString(raw)
	if !ok || r.Sign() <= 0 || r.Cmp(big.NewRat(1000000, 1)) > 0 {
		return 0, errors.New("Must be greater than 0 and at most 1,000,000.")
	}
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
	return r.Num().Int64(), nil
}
func halfUp(r *big.Rat) *big.Int {
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	rem.Abs(rem)
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(int64(r.Sign())))
	}
	return q
}
func decimalString(n int64, scale int) string {
	s := fmt.Sprintf("%0*d", scale+1, n)
	return s[:len(s)-scale] + "." + s[len(s)-scale:]
}
func money(r *big.Rat) string {
	n := halfUp(new(big.Rat).Mul(r, big.NewRat(100, 1)))
	sign := ""
	if n.Sign() < 0 {
		sign = "-"
	}
	s := new(big.Int).Abs(n).String()
	if len(s) < 3 {
		s = strings.Repeat("0", 3-len(s)) + s
	}
	return sign + s[:len(s)-2] + "." + s[len(s)-2:]
}

type savedLine struct {
	TransactionLine
	a, q, p int64
}

func validateTransaction(in TransactionInput) ([]savedLine, error) {
	_, err := time.Parse("2006-01-02", in.Date)
	if err != nil || in.Date < "1990-01-01" || in.Date > marketdata.BerlinDay(time.Now()) {
		return nil, invalidField("date", "Choose a date from 1990-01-01 through today.")
	}
	if len(in.Lines) == 0 || len(in.Lines) > 100 {
		return nil, invalidField("lines", "Provide between 1 and 100 lines.")
	}
	result := make([]savedLine, 0, len(in.Lines))
	for i, l := range in.Lines {
		v := savedLine{TransactionLine: l}
		for _, f := range []struct {
			name, raw string
			scale     int
			out       *int64
		}{{"amount", l.Amount, 2, &v.a}, {"price", l.Price, 4, &v.p}, {"quantity", l.Quantity, 6, &v.q}} {
			n, err := parseDecimal(f.raw, f.scale)
			if err != nil {
				return nil, invalidField(fmt.Sprintf("lines.%d.%s", i, f.name), err.Error())
			}
			*f.out = n
		}
		expected := halfUp(new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(v.a), big.NewInt(100000000)), big.NewInt(v.p)))
		if expected.Sign() <= 0 || expected.Cmp(big.NewInt(1000000000000)) > 0 || new(big.Int).Abs(new(big.Int).Sub(expected, big.NewInt(v.q))).Cmp(big.NewInt(1)) > 0 {
			return nil, invalidField(fmt.Sprintf("lines.%d.quantity", i), "Quantity must match amount divided by price, within 0.000001.")
		}
		result = append(result, v)
	}
	return result, nil
}
func (s *Store) CreateTransaction(ctx context.Context, user string, id int64, in TransactionInput) (int64, error) {
	var txID int64
	err := s.write(ctx, func(conn *sql.Conn) error {
		if err := owns(ctx, conn, user, id); err != nil {
			return err
		}
		lines, err := validateTransaction(in)
		if err != nil {
			return err
		}
		duplicates := []TransactionLine{}
		for i, l := range lines {
			var member int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM portfolio_items WHERE portfolio_id=? AND instrument_id=?", id, l.InstrumentID).Scan(&member); err != nil {
				return err
			}
			if member == 0 {
				return invalidField(fmt.Sprintf("lines.%d.instrumentId", i), "Instrument must be a portfolio item.")
			}
			var count int
			if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM transaction_lines l JOIN transactions t ON t.id=l.transaction_id WHERE t.portfolio_id=? AND t.trade_date=? AND l.instrument_id=? AND l.quantity_e6=? AND l.price_eur_e4=?`, id, in.Date, l.InstrumentID, l.q, l.p).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				duplicates = append(duplicates, l.TransactionLine)
			}
		}
		if len(duplicates) > 0 && !in.Force {
			return &DuplicateError{"DUPLICATE_TRANSACTION", "A matching transaction exists.", duplicates}
		}
		res, err := conn.ExecContext(ctx, "INSERT INTO transactions(portfolio_id,trade_date,source) VALUES (?,?,'MANUAL')", id, in.Date)
		if err != nil {
			return err
		}
		txID, err = res.LastInsertId()
		if err != nil {
			return err
		}
		for _, l := range lines {
			if _, err := conn.ExecContext(ctx, `INSERT INTO transaction_lines(transaction_id,instrument_id,amount_eur_e2,quantity_e6,price_eur_e4,orig_price_e4,orig_currency,fx_rate_e8,fx_rate_date) VALUES (?,?,?,?,?,(SELECT orig_price_e4 FROM price_quotes WHERE id=? AND instrument_id=? AND price_eur_e4=? AND fx_rate_e8 IS NOT NULL),(SELECT orig_currency FROM price_quotes WHERE id=? AND instrument_id=? AND price_eur_e4=? AND fx_rate_e8 IS NOT NULL),(SELECT fx_rate_e8 FROM price_quotes WHERE id=? AND instrument_id=? AND price_eur_e4=?),(SELECT fx_rate_date FROM price_quotes WHERE id=? AND instrument_id=? AND price_eur_e4=? AND fx_rate_e8 IS NOT NULL))`, txID, l.InstrumentID, l.a, l.q, l.p, l.QuoteID, l.InstrumentID, l.p, l.QuoteID, l.InstrumentID, l.p, l.QuoteID, l.InstrumentID, l.p, l.QuoteID, l.InstrumentID, l.p); err != nil {
				return err
			}
		}
		return nil
	})
	return txID, err
}

type SavingPlanItem struct {
	AVSymbol      string `json:"avSymbol,omitempty"`
	AVCurrency    string `json:"avCurrency,omitempty"`
	AVRegion      string `json:"avRegion,omitempty"`
	AVLinkedByYou bool   `json:"avLinkedByYou"`
	AVLinkedAt    string `json:"avLinkedAt,omitempty"`
	AVSearchTerm  string `json:"avSearchTerm,omitempty"`
	QuoteID       int64  `json:"quoteId,omitempty"`
	InstrumentID  int64  `json:"instrumentId"`
	Ticker        string `json:"ticker"`
	Name          string `json:"name"`
	DefaultAmount string `json:"defaultAmount"`
	DefaultPrice  string `json:"defaultPrice"`
	PriceSource   string `json:"priceSource"`
}

func (s *Store) SavingPlan(ctx context.Context, user string, id int64) ([]SavingPlanItem, error) {
	if _, err := s.Portfolio(ctx, user, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.id,i.ticker,i.name,COALESCE(l.amount_eur_e2,0),COALESCE(l.price_eur_e4,0),COALESCE(t.trade_date,''),COALESCE(i.av_symbol,''),COALESCE(i.av_currency,''),COALESCE(i.av_region,''),COALESCE(i.av_linked_by= ?,0),COALESCE(i.av_linked_at,'') FROM portfolio_items pi JOIN instruments i ON i.id=pi.instrument_id LEFT JOIN transaction_lines l ON l.id=(SELECT ll.id FROM transaction_lines ll JOIN transactions tt ON tt.id=ll.transaction_id WHERE tt.portfolio_id=pi.portfolio_id AND ll.instrument_id=i.id ORDER BY tt.trade_date DESC,tt.created_at DESC,tt.id DESC,ll.id DESC LIMIT 1) LEFT JOIN transactions t ON t.id=l.transaction_id WHERE pi.portfolio_id=? ORDER BY i.id`, user, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []SavingPlanItem{}
	for rows.Next() {
		var v SavingPlanItem
		var a, p int64
		var date string
		if err := rows.Scan(&v.InstrumentID, &v.Ticker, &v.Name, &a, &p, &date, &v.AVSymbol, &v.AVCurrency, &v.AVRegion, &v.AVLinkedByYou, &v.AVLinkedAt); err != nil {
			return nil, err
		}
		if v.AVSymbol == "" {
			v.AVSearchTerm = marketdata.AVSearchTerm(v.Ticker, v.Name)
		}
		v.DefaultAmount = decimalString(a, 2)
		v.PriceSource = "None"
		if p > 0 {
			v.DefaultPrice = decimalString(p, 4)
			d, _ := time.Parse("2006-01-02", date)
			v.PriceSource = "Last purchase " + d.Format("02.01.2006")
		}
		result = append(result, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := s.applyQuoteDefaults(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

type TransactionRow struct {
	TransactionLine
	ID            int64  `json:"id"`
	TransactionID int64  `json:"transactionId"`
	Date          string `json:"date"`
	Ticker        string `json:"ticker"`
	Name          string `json:"name"`
	Source        string `json:"source"`
	a, q, p       int64
}

func (s *Store) transactionRows(ctx context.Context, user string, id int64) ([]TransactionRow, error) {
	if _, err := s.Portfolio(ctx, user, id); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT l.id,t.id,t.trade_date,i.id,i.ticker,i.name,t.source,l.amount_eur_e2,l.quantity_e6,l.price_eur_e4,l.orig_price_e4,COALESCE(l.orig_currency,''),l.fx_rate_e8,COALESCE(l.fx_rate_date,'') FROM transactions t JOIN transaction_lines l ON l.transaction_id=t.id JOIN instruments i ON i.id=l.instrument_id WHERE t.portfolio_id=? ORDER BY t.trade_date,t.created_at,t.id,l.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []TransactionRow{}
	for rows.Next() {
		var v TransactionRow
		var orig, rate sql.NullInt64
		if err := rows.Scan(&v.ID, &v.TransactionID, &v.Date, &v.InstrumentID, &v.Ticker, &v.Name, &v.Source, &v.a, &v.q, &v.p, &orig, &v.OrigCurrency, &rate, &v.FXRateDate); err != nil {
			return nil, err
		}
		v.Amount = decimalString(v.a, 2)
		v.Quantity = decimalString(v.q, 6)
		v.Price = decimalString(v.p, 4)
		if orig.Valid {
			v.OrigPrice = decimalString(orig.Int64, 4)
		}
		if rate.Valid {
			v.FXRate = decimalString(rate.Int64, 8)
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

type TransactionPage struct {
	Lines []TransactionRow `json:"lines"`
	Total int              `json:"total"`
	Page  int              `json:"page"`
}

func (s *Store) Transactions(ctx context.Context, user string, id int64, page, pageSize int) (TransactionPage, error) {
	all, err := s.transactionRows(ctx, user, id)
	if err != nil {
		return TransactionPage{}, err
	}
	// Reverse transaction groups while preserving line order inside each group.
	ordered := make([]TransactionRow, 0, len(all))
	for end := len(all); end > 0; {
		start := end - 1
		for start > 0 && all[start-1].TransactionID == all[end-1].TransactionID {
			start--
		}
		ordered = append(ordered, all[start:end]...)
		end = start
	}
	start := min((page-1)*pageSize, len(ordered))
	return TransactionPage{ordered[start:min(start+pageSize, len(ordered))], len(ordered), page}, nil
}
func (s *Store) DeleteTransaction(ctx context.Context, user string, id, txID int64) error {
	return s.write(ctx, func(conn *sql.Conn) error {
		if err := owns(ctx, conn, user, id); err != nil {
			return err
		}
		res, err := conn.ExecContext(ctx, "DELETE FROM transactions WHERE portfolio_id=? AND id=?", id, txID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err == nil && n == 0 {
			return ErrNotFound
		}
		return err
	})
}
