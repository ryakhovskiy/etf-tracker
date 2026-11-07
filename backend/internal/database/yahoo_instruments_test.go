package database

import (
	"context"
	"errors"
	"testing"
	"time"

	md "holdings/backend/internal/marketdata"
)

func TestSaveYahooInstrumentIdentifiesBySymbolThenISIN(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	hit := md.Result{YahooSymbol: "zzz.l", Name: "Zzz Fund", Ticker: "ZZZ.L", Type: "ETF"}
	created, err := s.SaveYahooInstrument(ctx, hit, "")
	if err != nil || created.ID == 0 || created.ISIN != "" || created.Name != "Zzz Fund" {
		t.Fatalf("%+v %v", created, err)
	}
	if src, _ := s.InstrumentSource(ctx, created.ID); src != "YAHOO" {
		t.Fatal(src)
	}
	ref, _ := s.InstrumentRef(ctx, created.ID)
	if ref.YahooSymbol != "ZZZ.L" || ref.ISIN != "" {
		t.Fatalf("symbol must be stored upper-case: %+v", ref)
	}
	// The same symbol again is the same instrument, and its fields are not overwritten.
	again, err := s.SaveYahooInstrument(ctx, md.Result{YahooSymbol: "ZZZ.L", Name: "Renamed", Type: "ETF"}, "")
	if err != nil || again.ID != created.ID || again.Name != "Zzz Fund" {
		t.Fatalf("%+v %v", again, err)
	}
	// A known ISIN reuses the existing instrument (seed SAP, id 7) and adds the symbol to it.
	sap, err := s.SaveYahooInstrument(ctx, md.Result{YahooSymbol: "SAP.DE", Name: "SAP SE", Type: "STOCK"}, "DE0007164600")
	if err != nil || sap.ID != 7 || sap.Name != "SAP SE" {
		t.Fatalf("%+v %v", sap, err)
	}
	if ref, _ = s.InstrumentRef(ctx, 7); ref.YahooSymbol != "SAP.DE" {
		t.Fatalf("%+v", ref)
	}
	// A second listing of the same security keeps the first symbol.
	if _, err = s.SaveYahooInstrument(ctx, md.Result{YahooSymbol: "SAP", Name: "SAP SE ADR", Type: "STOCK"}, "DE0007164600"); err != nil {
		t.Fatal(err)
	}
	if ref, _ = s.InstrumentRef(ctx, 7); ref.YahooSymbol != "SAP.DE" {
		t.Fatalf("first symbol replaced: %+v", ref)
	}
	// Invalid input is refused.
	for _, bad := range []struct {
		r    md.Result
		isin string
	}{{md.Result{YahooSymbol: "", Name: "x", Type: "ETF"}, ""}, {md.Result{YahooSymbol: "A B", Name: "x", Type: "ETF"}, ""}, {md.Result{YahooSymbol: "OK", Name: "", Type: "ETF"}, ""}, {md.Result{YahooSymbol: "OK", Name: "x", Type: "BOND"}, ""}, {md.Result{YahooSymbol: "OK", Name: "x", Type: "ETF"}, "BADISIN"}} {
		if _, err = s.SaveYahooInstrument(ctx, bad.r, bad.isin); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if ok, _ := s.YahooSymbolExists(ctx, "zzz.l"); !ok {
		t.Fatal("exists check is case-insensitive")
	}
}

func TestYahooSymbolsAreUniqueAndLinkingReplacesHistory(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	at := time.Now()
	if err := s.SetYahooSymbol(ctx, 7, "SAP.DE", at); err != nil {
		t.Fatal(err)
	}
	// Two instruments can never share a symbol, however it gets set.
	if err := s.SetYahooSymbol(ctx, 8, "SAP.DE", at); !errors.Is(err, ErrSymbolInUse) {
		t.Fatal(err)
	}
	if err := s.LinkYahooSymbol(ctx, 8, "sap.de"); !errors.Is(err, ErrSymbolInUse) {
		t.Fatal(err)
	}
	if err := s.LinkYahooSymbol(ctx, 8, "bad symbol"); err == nil {
		t.Fatal("invalid symbol accepted")
	}
	if err := s.LinkYahooSymbol(ctx, 9999, "NOPE.DE"); err != ErrNotFound {
		t.Fatal(err)
	}
	// Replacing a symbol drops the stored history of the old listing.
	if err := s.SaveHistory(ctx, 7, "SAP.DE", "EUR", md.ProviderYahoo, []md.YahooPoint{{Day: "2026-11-03", Close: "206.6500"}}, at); err != nil {
		t.Fatal(err)
	}
	if err := s.LinkYahooSymbol(ctx, 7, "SAP.F"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.HistoryDay(ctx, 7, md.ProviderYahoo, "2026-11-03"); p != nil {
		t.Fatal("history of the old listing kept")
	}
	if ref, _ := s.InstrumentRef(ctx, 7); ref.YahooSymbol != "SAP.F" || ref.YahooCheckedAt.IsZero() {
		t.Fatalf("%+v", ref)
	}
	// The freed symbol can be used by another instrument.
	if err := s.LinkYahooSymbol(ctx, 8, "SAP.DE"); err != nil {
		t.Fatal(err)
	}
}

func TestFillISINAndEulerpoolCurrencyCache(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	hit, _ := s.SaveYahooInstrument(ctx, md.Result{YahooSymbol: "NEW.L", Name: "New Fund", Type: "ETF"}, "")
	// An ISIN owned by another instrument is not taken, an invalid one is ignored.
	if ok, err := s.FillISIN(ctx, hit.ID, "DE0007164600"); ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, _ := s.FillISIN(ctx, hit.ID, "nope"); ok {
		t.Fatal("invalid ISIN filled")
	}
	if ok, err := s.FillISIN(ctx, hit.ID, "IE00NEWFUND9"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	// Filling never overwrites.
	if ok, _ := s.FillISIN(ctx, hit.ID, "US0378331005"); ok {
		t.Fatal("existing ISIN overwritten")
	}
	if ref, _ := s.InstrumentRef(ctx, hit.ID); ref.ISIN != "IE00NEWFUND9" {
		t.Fatal(ref)
	}
	if err := s.SetEPCurrency(ctx, 7, "EUR"); err != nil {
		t.Fatal(err)
	}
	if ref, _ := s.InstrumentRef(ctx, 7); ref.EPCurrency != "EUR" {
		t.Fatal(ref)
	}
	if name, err := s.InstrumentName(ctx, 7); err != nil || name != "SAP SE" {
		t.Fatal(name, err)
	}
	if _, err := s.InstrumentName(ctx, 9999); err != ErrNotFound {
		t.Fatal(err)
	}
}
