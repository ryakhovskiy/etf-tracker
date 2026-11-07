package marketdata

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPickISINNeedsAClearMatch(t *testing.T) {
	hits := []EPHit{
		{ISIN: "IE00B8GKDB10", Name: "Vanguard FTSE All-World High Dividend Yield UCITS ETF", Type: "ETF"},
		{ISIN: "IE00B3RBWM25", Name: "Vanguard FTSE All-World UCITS ETF", Type: "ETF"},
	}
	// Share-class noise (USD Distributing) does not matter; the high-dividend fund is not the plain one.
	if got := PickISIN(hits, "Vanguard FTSE All-World High Dividend Yield UCITS ETF USD Distributing", "ETF"); got != "IE00B8GKDB10" {
		t.Fatal(got)
	}
	if got := PickISIN(hits, "Vanguard FTSE All-World UCITS ETF USD Distributing", "ETF"); got != "IE00B3RBWM25" {
		t.Fatal(got)
	}
	// Lookalikes with the same score are ambiguous: no ISIN rather than a wrong one.
	twin := []EPHit{{ISIN: "IE00AAAAAAA7", Name: "Foo World ETF", Type: "ETF"}, {ISIN: "IE00BBBBBBB5", Name: "Foo World ETF", Type: "ETF"}}
	if got := PickISIN(twin, "Foo World ETF", "ETF"); got != "" {
		t.Fatal("ambiguous match accepted:", got)
	}
	// A partial overlap and a wrong type are refused.
	if got := PickISIN(hits, "Vanguard Emerging Markets", "ETF"); got != "" {
		t.Fatal(got)
	}
	if got := PickISIN([]EPHit{{ISIN: "DE0007164600", Name: "SAP SE", Type: "STOCK"}}, "SAP SE", "ETF"); got != "" {
		t.Fatal("type mismatch accepted:", got)
	}
	if got := PickISIN([]EPHit{{ISIN: "DE0007164600", Name: "SAP SE", Type: "STOCK"}}, "SAP SE", "STOCK"); got != "DE0007164600" {
		t.Fatal(got)
	}
	if PickISIN(nil, "x", "ETF") != "" || PickISIN(hits, "", "ETF") != "" || PickISIN(hits, "UCITS ETF", "ETF") != "" {
		t.Fatal("empty or noise-only names must not match")
	}
}

func TestParseEPPointsReadsBothHistoryShapes(t *testing.T) {
	etf, err := ParseEPPoints([]byte(`[[1793692800000,36.2625],[1793779200000,36.6575]]`))
	if err != nil || len(etf) != 2 || etf[0] != (EPPoint{"2026-11-03", "36.2625"}) {
		t.Fatalf("%+v %v", etf, err)
	}
	b, _ := os.ReadFile("../../../testdata/marketdata/eulerpool/equity-quotes-sap.json")
	stock, err := ParseEPPoints(b)
	if err != nil || len(stock) != 9 || stock[0] != (EPPoint{"2026-10-26", "213.0000"}) {
		t.Fatalf("%+v %v", stock, err)
	}
	if p, ok := LastEPPointOnOrBefore(stock, "2026-11-01"); !ok || p.Day != "2026-10-30" {
		t.Fatalf("weekend must resolve to Friday: %+v", p)
	}
	if _, ok := LastEPPointOnOrBefore(stock, "2026-01-01"); ok {
		t.Fatal("no price before the series")
	}
	if _, err = ParseEPPoints([]byte(`{"error":"Security not found"}`)); !errors.Is(err, ErrUpstream) {
		t.Fatal(err)
	}
	if pts, err := ParseEPPoints([]byte(`[]`)); err != nil || len(pts) != 0 {
		t.Fatal(pts, err)
	}
}

func TestEulerpoolHistoryAndStockProfileViaMock(t *testing.T) {
	g := NewEPGateway(&memStore{state: map[string]string{}}, MockEP{}, 100)
	ctx := context.Background()
	from := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	points, err := g.History(ctx, "u", "equity", "DE0007164600", from, from.AddDate(0, 0, 10))
	if err != nil || len(points) != 9 {
		t.Fatal(points, err)
	}
	if points, err = g.History(ctx, "u", "etf", "IE00B4X9L533", from, from.AddDate(0, 0, 10)); err != nil || len(points) == 0 {
		t.Fatal(points, err)
	}
	if _, err = g.History(ctx, "u", "equity", "XX0000000000", from, from); !errors.Is(err, ErrEPNotFound) {
		t.Fatal(err)
	}
	currency, wkn, err := g.StockProfile(ctx, "u", "DE0007164600")
	if err != nil || currency != "EUR" || wkn != "716460" {
		t.Fatal(currency, wkn, err)
	}
}

func TestYahooHitsCarryExchangeAndSector(t *testing.T) {
	b, _ := os.ReadFile("../../../testdata/marketdata/yahoo/search-isin-stock.json")
	hits, err := ParseYahooSearch(b)
	if err != nil || len(hits) != 1 || hits[0].Exchange != "XETRA" || hits[0].Sector != "Technology" || hits[0].Name != "SAP SE" {
		t.Fatalf("%+v %v", hits, err)
	}
}
