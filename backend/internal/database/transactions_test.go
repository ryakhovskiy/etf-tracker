package database

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
)

func TestTransactionDecimals(t *testing.T) {
	if got := money(big.NewRat(1005, 1000)); got != "1.01" {
		t.Fatal("half-up:", got)
	}
	large, _ := new(big.Rat).SetString("1000000000000000000.005")
	if got := money(large); got != "1000000000000000000.01" {
		t.Fatal("aggregate overflow:", got)
	}
	for _, tc := range []struct {
		raw   string
		scale int
		want  int64
	}{{"1000000", 6, 1000000000000}, {"0.000001", 6, 1}, {"1000000.0000", 4, 10000000000}, {"1.23", 2, 123}} {
		got, err := parseDecimal(tc.raw, tc.scale)
		if err != nil || got != tc.want {
			t.Fatalf("%+v: %d %v", tc, got, err)
		}
	}
	for _, raw := range []string{"1,23", "1e3", "-1", "0", "1000000.01", "1.001", "NaN", "", " 1"} {
		if _, err := parseDecimal(raw, 2); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	for _, tc := range []struct{ amount, price, quantity string }{{"1.00", "128.0000", "0.007813"}, {"1000000.00", "1.0000", "1000000.000000"}, {"1000000.00", "1000000.0000", "1.000000"}} {
		if _, err := validateTransaction(TransactionInput{Date: "2025-01-01", Lines: []TransactionLine{{InstrumentID: 1, Amount: tc.amount, Price: tc.price, Quantity: tc.quantity}}}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTransactionLifecycleAndPerformance(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, "alice", 0, "Savings")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, "alice", p.ID, []int64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	defaults, err := s.SavingPlan(ctx, "alice", p.ID)
	if err != nil || len(defaults) != 3 || defaults[0].DefaultPrice != "" || defaults[0].DefaultAmount != "0.00" {
		t.Fatalf("%+v %v", defaults, err)
	}
	commit := func(date string, lines ...TransactionLine) int64 {
		t.Helper()
		id, err := s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: date, Lines: lines})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	l := func(i int64, a, p, q string) TransactionLine {
		return TransactionLine{InstrumentID: i, Amount: a, Price: p, Quantity: q}
	}
	first := commit("2025-01-01", l(1, "20", "10", "2"), l(2, "20", "20", "1"))
	commit("2025-01-10", l(1, "20", "20", "1"), l(1, "120", "40", "3")) // weighted price = 35, held = 6, value = 210
	later := commit("2025-03-01", l(3, "15", "5", "3"))
	perf, err := s.Performance(ctx, "alice", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(perf.Dates) != "[2025-01-01 2025-01-10 2025-03-01]" || fmt.Sprint(perf.Totals) != "[40.00 230.00 245.00]" || perf.Invested != "195.00" || fmt.Sprint(perf.Series[2].Values) != "[0.00 0.00 15.00]" {
		t.Fatalf("%+v", perf)
	}
	defaults, err = s.SavingPlan(ctx, "alice", p.ID)
	if err != nil || defaults[0].DefaultPrice != "40.0000" || defaults[0].DefaultAmount != "120.00" {
		t.Fatalf("%+v %v", defaults, err)
	}
	// A backdated commit must not replace a later default; same-date insertion breaks timestamp ties deterministically.
	commit("2024-01-01", l(1, "4", "4", "1"))
	commit("2025-01-10", l(1, "30", "30", "1"))
	defaults, _ = s.SavingPlan(ctx, "alice", p.ID)
	if defaults[0].DefaultPrice != "30.0000" {
		t.Fatal(defaults)
	}
	dup := TransactionInput{Date: "2025-01-01", Lines: []TransactionLine{l(1, "20", "10", "2")}}
	_, err = s.CreateTransaction(ctx, "alice", p.ID, dup)
	var duplicate *DuplicateError
	if !errors.As(err, &duplicate) || len(duplicate.Lines) != 1 {
		t.Fatalf("%v", err)
	}
	dup.Force = true
	if _, err = s.CreateTransaction(ctx, "alice", p.ID, dup); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveItem(ctx, "alice", p.ID, 1); err != ErrItemHasTransactions {
		t.Fatal(err)
	}
	if err = s.DeleteTransaction(ctx, "bob", p.ID, first); err != ErrNotFound {
		t.Fatal(err)
	}
	if _, err = s.Performance(ctx, "bob", p.ID); err != ErrNotFound {
		t.Fatal(err)
	}
	if err = s.DeleteTransaction(ctx, "alice", p.ID, later); err != nil {
		t.Fatal(err)
	}
	perf, _ = s.Performance(ctx, "alice", p.ID)
	if len(perf.Dates) != 3 || perf.Dates[2] != "2025-01-10" {
		t.Fatal(perf)
	}
	if err = s.RemoveItem(ctx, "alice", p.ID, 3); err != nil {
		t.Fatal(err)
	}
	if err = s.DeletePortfolio(ctx, "alice", p.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"transactions", "transaction_lines"} {
		var n int
		if err = s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("cascade %s: %d %v", table, n, err)
		}
	}
}
func TestTransactionValidationAtomicAndPagination(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p, _ := s.SavePortfolio(ctx, "alice", 0, "Savings")
	s.AddItems(ctx, "alice", p.ID, []int64{1, 2})
	good := TransactionLine{InstrumentID: 1, Amount: "10", Price: "5", Quantity: "2"}
	for _, in := range []TransactionInput{{Date: "2099-01-01", Lines: []TransactionLine{good}}, {Date: "1989-12-31", Lines: []TransactionLine{good}}, {Date: "2025-01-01", Lines: []TransactionLine{good, {InstrumentID: 3, Amount: "10", Price: "5", Quantity: "2"}}}, {Date: "2025-01-01", Lines: []TransactionLine{{InstrumentID: 1, Amount: "10", Price: "5", Quantity: "3"}}}, {Date: "2025-01-01", Lines: []TransactionLine{{InstrumentID: 1, Amount: "1000000.01", Price: "5", Quantity: "2"}}}, {Date: "2025-01-01", Lines: []TransactionLine{{InstrumentID: 1, Amount: "10", Price: "0", Quantity: "2"}}}} {
		if _, err := s.CreateTransaction(ctx, "alice", p.ID, in); err == nil {
			t.Fatal("accepted invalid", in)
		}
	}
	var n int
	s.db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&n)
	if n != 0 {
		t.Fatal("partial write", n)
	}
	// A DB failure on a later line must roll back the header and earlier lines too.
	if _, err := s.db.Exec(`CREATE TRIGGER fail_second BEFORE INSERT ON transaction_lines WHEN NEW.instrument_id=2 BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2025-01-01", Lines: []TransactionLine{good, {InstrumentID: 2, Amount: "10", Price: "5", Quantity: "2"}}}); err == nil {
		t.Fatal("expected database failure")
	}
	s.db.QueryRow("SELECT COUNT(*) FROM transactions").Scan(&n)
	if n != 0 {
		t.Fatal("partial write", n)
	}
	s.db.Exec("DROP TRIGGER fail_second")
	for i := 0; i < 51; i++ {
		if _, err := s.CreateTransaction(ctx, "alice", p.ID, TransactionInput{Date: "2025-01-01", Lines: []TransactionLine{good}, Force: true}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.Transactions(ctx, "alice", p.ID, 1, 50)
	if err != nil || len(page.Lines) != 50 || page.Total != 51 || page.Lines[0].TransactionID != 51 {
		t.Fatalf("%+v %v", page, err)
	}
	page, err = s.Transactions(ctx, "alice", p.ID, 2, 50)
	if err != nil || len(page.Lines) != 1 {
		t.Fatalf("%+v %v", page, err)
	}
	page, err = s.Transactions(ctx, "alice", p.ID, 2, 25)
	if err != nil || len(page.Lines) != 25 || page.Total != 51 || page.Lines[0].TransactionID != 26 {
		t.Fatalf("custom page size: %+v %v", page, err)
	}
	page, err = s.Transactions(ctx, "alice", p.ID, 1, 100)
	if err != nil || len(page.Lines) != 51 {
		t.Fatalf("large page size: %+v %v", page, err)
	}
}
