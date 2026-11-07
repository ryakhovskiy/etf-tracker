package database

import (
	"context"
	"testing"
	"time"

	md "holdings/backend/internal/marketdata"
)

func TestEulerpoolMigrationKeepsDataAndForeignKeys(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	provider, err := s.migrationProvider()
	if err != nil {
		t.Fatal(err)
	}
	// Roll back to the Alpha Vantage schema and fill it, then migrate forward again.
	if _, err = provider.DownTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	var instruments int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM instruments").Scan(&instruments); err != nil || instruments == 0 {
		t.Fatal(instruments, err)
	}
	exec("INSERT INTO portfolios(user_id,name) VALUES('alice','P')")
	exec("INSERT INTO portfolio_items(portfolio_id,instrument_id) VALUES(1,1),(1,2)")
	exec("INSERT INTO transactions(portfolio_id,trade_date,source) VALUES(1,'2026-11-02','MANUAL')")
	exec("INSERT INTO transaction_lines(transaction_id,instrument_id,amount_eur_e2,quantity_e6,price_eur_e4) VALUES(1,1,1000,1000000,100000)")
	exec("UPDATE instruments SET av_symbol='AAPL',av_currency='USD',av_linked_by='alice' WHERE id=1")
	exec("INSERT INTO price_quotes(instrument_id,price_eur_e4,orig_price_e4,orig_currency,quote_date) VALUES(1,90000,100000,'USD','2026-11-02')")
	exec("INSERT INTO av_requests(requested_at,user_id,function) VALUES('2026-11-02T10:00:00.000000000Z','alice','GLOBAL_QUOTE')")
	exec("INSERT INTO av_state(k,v) VALUES('blocked_until','2026-11-02T11:00:00.000000000Z')")

	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var after int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM instruments").Scan(&after); err != nil || after != instruments {
		t.Fatalf("instruments %d -> %d %v", instruments, after, err)
	}
	count := func(q string) int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(q, err)
		}
		return n
	}
	if count("SELECT COUNT(*) FROM portfolio_items") != 2 || count("SELECT COUNT(*) FROM transaction_lines") != 1 {
		t.Fatal("dependent rows lost")
	}
	if count("SELECT COUNT(*) FROM price_quotes WHERE provider='ALPHAVANTAGE' AND instrument_id=1") != 1 {
		t.Fatal("quote lost or not defaulted to ALPHAVANTAGE")
	}
	if count("SELECT COUNT(*) FROM provider_requests WHERE provider='ALPHAVANTAGE' AND counted=1") != 1 || count("SELECT COUNT(*) FROM provider_state WHERE provider='ALPHAVANTAGE' AND k='blocked_until'") != 1 {
		t.Fatal("ledger or state not carried over")
	}
	if count("SELECT COUNT(*) FROM instruments WHERE av_symbol='AAPL' AND av_linked_by='alice'") != 1 {
		t.Fatal("AV link lost")
	}
	rows, err := s.db.Query("PRAGMA foreign_key_check")
	if err != nil || rows.Next() {
		t.Fatal("foreign_key_check not clean", err)
	}
	rows.Close()
	exec("INSERT INTO instruments(type,name,ticker,isin,source) VALUES('ETF','E','E','IE00B4X9L534','EULERPOOL')")
	if _, err = s.db.Exec("INSERT INTO instruments(type,name,ticker,isin,source) VALUES('ETF','E','E','XX','BOGUS')"); err == nil {
		t.Fatal("source CHECK lost")
	}
	// Existing AV counting still works on the renamed ledger and ignores other providers.
	if err = s.RecordProviderRequest(ctx, "EULERPOOL", "alice", "x", time.Now(), true); err != nil {
		t.Fatal(err)
	}
	b, err := s.AVBudget(ctx, "alice", time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC), md.AVLimits{Minute: 5, Day: 25, User: 10, Backoff: time.Hour}.Defaults())
	if err != nil || b.UserRemaining != 9 || b.GlobalRemaining != 24 {
		t.Fatalf("%+v %v", b, err)
	}
}

func TestProviderLedgerAndState(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	now := time.Now()
	for _, c := range []bool{true, true, false} {
		if err := s.RecordProviderRequest(ctx, "EULERPOOL", "alice", "f", now, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RecordProviderRequest(ctx, "ALPHAVANTAGE", "", "f", now, true); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ProviderUsage(ctx, "EULERPOOL", now.Add(-time.Hour)); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if v, err := s.ProviderState(ctx, "EULERPOOL", "remaining"); err != nil || v != "" {
		t.Fatal(v, err)
	}
	_ = s.SetProviderState(ctx, "EULERPOOL", "remaining", "5")
	_ = s.SetProviderState(ctx, "EULERPOOL", "remaining", "4")
	_ = s.SetProviderState(ctx, "ALPHAVANTAGE", "remaining", "9")
	if v, _ := s.ProviderState(ctx, "EULERPOOL", "remaining"); v != "4" {
		t.Fatal(v)
	}
}

func TestEulerpoolQuoteFreshnessByProvider(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	s.SetQuoteTTL(15 * time.Minute)
	fetched := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	// Instrument 1 is a stock, 10 an ETF (seed data).
	for _, id := range []int64{1, 10} {
		q := &md.Quote{InstrumentID: id, Price: "10.0000", OrigPrice: "10.0000", Currency: "EUR", QuoteDate: "2026-11-05", FetchedAt: fetched, Provider: md.ProviderEulerpool}
		if err := s.SaveQuote(ctx, q, ""); err != nil {
			t.Fatal(err)
		}
	}
	fresh := func(id int64, at time.Time) bool {
		q, err := s.FreshQuote(ctx, id, at)
		if err != nil {
			t.Fatal(err)
		}
		return q != nil
	}
	if !fresh(1, fetched.Add(14*time.Minute)) || fresh(1, fetched.Add(16*time.Minute)) {
		t.Fatal("stock quotes must be fresh for the TTL only")
	}
	if !fresh(10, fetched.Add(3*time.Hour)) || fresh(10, fetched.Add(20*time.Hour)) {
		t.Fatal("ETF closes must be fresh for the Berlin day")
	}
	// Eulerpool quotes need no linked AV symbol.
	if q, _ := s.FreshQuote(ctx, 1, fetched); q == nil || q.Provider != md.ProviderEulerpool {
		t.Fatal(q)
	}
}

func TestETFProfileStorageFillsWithoutOverwriting(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	if _, err := s.ETFProfile(ctx, 17); err != ErrNotFound {
		t.Fatal(err)
	}
	p := &md.ETFProfile{ISIN: "IE00B4X9L533", Name: "Other name", Symbol: "OTHER", ETFCompany: "HSBC", ExpenseRatio: "0.15", AUMEUR: "1000", Currency: "GBP", InceptionDate: "2010-12-08", Raw: []byte(`{}`), FetchedAt: time.Now()}
	if err := s.SaveETFProfile(ctx, 17, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.ETFProfile(ctx, 17)
	if err != nil || got.ETFCompany != "HSBC" || got.ExpenseRatio != "0.15" || got.DividendYield != "" || got.AUMEUR != "1000" || time.Since(got.FetchedAt) > time.Minute {
		t.Fatalf("%+v %v", got, err)
	}
	var name, ticker string
	if err = s.db.QueryRow("SELECT name,ticker FROM instruments WHERE id=17").Scan(&name, &ticker); err != nil || name != "HSBC MSCI World UCITS ETF USD" || ticker != "IE00B4X9L533" {
		t.Fatal("existing fields overwritten", name, ticker, err)
	}
	if err = s.SaveETFProfile(ctx, 9999, p); err != ErrNotFound {
		t.Fatal(err)
	}
	if AUMToEUR("14335960472", 92310000) != "13233525112" || AUMToEUR("x", 1) != "" {
		t.Fatal(AUMToEUR("14335960472", 92310000))
	}
}

func TestInstrumentRefExposesProfileNAV(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	p := &md.ETFProfile{ISIN: "IE00B8GKDB10", Currency: "GBP", Raw: []byte(`{"nav":68.41,"currency":"GBP"}`), FetchedAt: time.Now()}
	if err := s.SaveETFProfile(ctx, 18, p); err != nil {
		t.Fatal(err)
	}
	ref, err := s.InstrumentRef(ctx, 18)
	if err != nil || ref.NAV != "68.41" || ref.Currency != "GBP" {
		t.Fatalf("%+v %v", ref, err)
	}
	if ref, _ = s.InstrumentRef(ctx, 1); ref.NAV != "" {
		t.Fatal(ref)
	}
}

func TestYahooSymbolCacheAndHistoryStorage(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	ref, _ := s.InstrumentRef(ctx, 18)
	if ref.YahooSymbol != "" || !ref.YahooCheckedAt.IsZero() {
		t.Fatal(ref)
	}
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	if err := s.SetYahooSymbol(ctx, 18, "VHYD.L", at); err != nil {
		t.Fatal(err)
	}
	if err := s.SetYahooSymbol(ctx, 1, "", at); err != nil { // Yahoo does not know it
		t.Fatal(err)
	}
	if ref, _ = s.InstrumentRef(ctx, 18); ref.YahooSymbol != "VHYD.L" || !ref.YahooCheckedAt.Equal(at) {
		t.Fatalf("%+v", ref)
	}
	if ref, _ = s.InstrumentRef(ctx, 1); ref.YahooSymbol != "" || ref.YahooCheckedAt.IsZero() {
		t.Fatalf("negative result not remembered: %+v", ref)
	}
	if err := s.SetYahooSymbol(ctx, 9999, "X", at); err != ErrNotFound {
		t.Fatal(err)
	}

	pts := []md.YahooPoint{{Day: "2026-10-30", Close: "91.5000"}, {Day: "2026-11-02", Close: "92.2500"}, {Day: "2026-11-03", Close: "90.0000"}}
	if err := s.SaveHistory(ctx, 18, "VHYD.L", "USD", md.ProviderYahoo, pts, at); err != nil {
		t.Fatal(err)
	}
	// Upsert: a corrected close replaces the old one.
	if err := s.SaveHistory(ctx, 18, "VHYD.L", "USD", md.ProviderYahoo, []md.YahooPoint{{Day: "2026-11-03", Close: "90.5000"}}, at); err != nil {
		t.Fatal(err)
	}
	p, err := s.HistoryDay(ctx, 18, md.ProviderYahoo, "2026-11-03")
	if err != nil || p == nil || p.Close != "90.5000" || p.Currency != "USD" {
		t.Fatalf("%+v %v", p, err)
	}
	if p, _ = s.HistoryDay(ctx, 18, md.ProviderYahoo, "2026-11-01"); p != nil {
		t.Fatal("a weekend has no exact row")
	}
	all, err := s.History(ctx, 18, md.ProviderYahoo, "2026-10-31", "2026-11-03")
	if err != nil || len(all) != 2 || all[0].Day != "2026-11-02" || all[1].Close != "90.5000" {
		t.Fatalf("%+v %v", all, err)
	}
}

func TestYahooQuotesAreFreshOnlyForCurrentDates(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) // Thursday
	save := func(day string) {
		q := &md.Quote{InstrumentID: 7, Price: "10.0000", OrigPrice: "10.0000", Currency: "EUR", QuoteDate: day, FetchedAt: now, Provider: md.ProviderYahoo}
		if err := s.SaveQuote(ctx, q, ""); err != nil {
			t.Fatal(err)
		}
	}
	save("2026-03-02") // a historical close fetched today for an old transaction date
	if q, _ := s.FreshQuote(ctx, 7, now); q != nil {
		t.Fatal("historical Yahoo close reused as a current price")
	}
	save("2026-11-04")
	if q, _ := s.FreshQuote(ctx, 7, now); q == nil || q.Provider != md.ProviderYahoo {
		t.Fatal("quote for the previous trading day should be fresh today")
	}
	plan, _ := s.SavingPlan(ctx, "alice", 0)
	_ = plan
}
