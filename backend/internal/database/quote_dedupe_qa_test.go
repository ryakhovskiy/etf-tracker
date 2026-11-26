package database

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

func qaDedupeQuote() marketdata.Quote {
	return marketdata.Quote{InstrumentID: 1, Provider: marketdata.ProviderYahoo,
		Price: "90.0000", OrigPrice: "100.0000", Currency: "USD",
		FXRate: "0.90000000", FXRateDate: "2026-11-13", FXSource: "YAHOO",
		QuoteDate: "2026-11-13", QuoteTime: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC),
		PrevClose: "99.0000", PrevFXRate: "0.89000000", PrevCloseDate: "2026-11-12",
		FetchedAt: time.Date(2026, 11, 13, 12, 1, 0, 123456789, time.UTC)}
}

func qaQuoteCount(t *testing.T, s *Store, want int) {
	t.Helper()
	var got int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM price_quotes").Scan(&got); err != nil || got != want {
		t.Fatalf("quote count = %d, want %d; err = %v", got, want, err)
	}
}

func TestSaveQuoteNewestOrderingQA(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(first, second *marketdata.Quote)
		reuse bool
	}{
		{"quote date precedes fetch time and id", func(a, b *marketdata.Quote) {
			b.QuoteDate = "2026-11-12"
			b.FetchedAt = a.FetchedAt.Add(time.Hour)
		}, true},
		{"fetch time precedes id", func(a, b *marketdata.Quote) {
			b.FetchedAt = a.FetchedAt.Add(-time.Hour)
		}, true},
		{"id breaks equal date and fetch ties", func(a, b *marketdata.Quote) {}, false},
		{"newer provider prevents matching an older row", func(a, b *marketdata.Quote) {
			b.Provider = marketdata.ProviderEulerpool
			b.FetchedAt = a.FetchedAt.Add(time.Hour)
		}, false},
		{"A B A retains the intervening change", func(a, b *marketdata.Quote) {
			b.FetchedAt = a.FetchedAt.Add(time.Hour)
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := featureStore(t)
			ctx := context.Background()
			a := qaDedupeQuote()
			b := a
			b.Price = "91.0000"
			tc.setup(&a, &b)
			for _, q := range []*marketdata.Quote{&a, &b} {
				if err := s.SaveQuote(ctx, q, ""); err != nil {
					t.Fatal(err)
				}
			}
			incoming := a
			incoming.ID = 0 // Cached/ForDate quotes also enter SaveQuote without an ID.
			incoming.FetchedAt = a.FetchedAt.Add(2 * time.Hour)
			if err := s.SaveQuote(ctx, &incoming, ""); err != nil {
				t.Fatal(err)
			}
			wantCount := 3
			if tc.reuse {
				wantCount = 2
				if incoming.ID != a.ID {
					t.Fatalf("duplicate ID = %d, want existing ID %d", incoming.ID, a.ID)
				}
			} else if incoming.ID == a.ID || incoming.ID == b.ID {
				t.Fatalf("non-consecutive repeat reused historical ID %d", incoming.ID)
			}
			qaQuoteCount(t, s, wantCount)
			latest, err := s.latestQuotes(ctx, []int64{a.InstrumentID})
			if err != nil || latest[a.InstrumentID] == nil || latest[a.InstrumentID].ID != incoming.ID {
				t.Fatalf("latest quote = %+v, err = %v", latest, err)
			}
			var unchanged string
			if err := s.db.QueryRow("SELECT fetched_at FROM price_quotes WHERE id=?", b.ID).Scan(&unchanged); err != nil || unchanged != b.FetchedAt.UTC().Format("2006-01-02T15:04:05.000000000Z07:00") {
				t.Fatalf("intervening row was changed: fetched_at=%q err=%v", unchanged, err)
			}
		})
	}
}

func TestSaveQuoteSQLNullDistinctionsQA(t *testing.T) {
	for _, tc := range []struct {
		column string
		stored any
	}{
		{"fx_rate_e8", int64(0)}, {"prev_close_e4", int64(0)}, {"prev_fx_rate_e8", int64(0)},
		{"quote_time", ""}, {"fx_source", ""}, {"prev_close_date", ""}, {"fx_rate_date", nil},
	} {
		t.Run(tc.column, func(t *testing.T) {
			s := featureStore(t)
			ctx := context.Background()
			q := qaDedupeQuote()
			q.FXRate, q.PrevClose, q.PrevFXRate, q.FXSource, q.PrevCloseDate = "", "", "", "", ""
			q.FXRateDate, q.QuoteTime = "", time.Time{}
			if err := s.SaveQuote(ctx, &q, ""); err != nil {
				t.Fatal(err)
			}
			oldID := q.ID
			// Legacy rows can contain empty strings/zero where incoming quotes store
			// NULL, or NULL where SaveQuote stores an empty FX date. IS distinguishes them.
			if _, err := s.db.Exec("UPDATE price_quotes SET "+tc.column+"=? WHERE id=?", tc.stored, oldID); err != nil {
				t.Fatal(err)
			}
			q.FetchedAt = q.FetchedAt.Add(time.Minute)
			if err := s.SaveQuote(ctx, &q, ""); err != nil {
				t.Fatal(err)
			}
			if q.ID == oldID {
				t.Fatalf("collapsed distinct SQL NULL and %#v in %s", tc.stored, tc.column)
			}
			newID := q.ID
			q.ID = 0
			q.FetchedAt = q.FetchedAt.Add(time.Minute)
			if err := s.SaveQuote(ctx, &q, ""); err != nil {
				t.Fatal(err)
			}
			if q.ID != newID {
				t.Fatalf("equal nullable quote ID = %d, want %d", q.ID, newID)
			}
			qaQuoteCount(t, s, 2)
		})
	}
}

func TestSaveQuoteChangesEitherPriceIndependentlyQA(t *testing.T) {
	for _, field := range []string{"EUR", "original"} {
		t.Run(field, func(t *testing.T) {
			s := featureStore(t)
			q := qaDedupeQuote()
			if err := s.SaveQuote(context.Background(), &q, ""); err != nil {
				t.Fatal(err)
			}
			oldID := q.ID
			if field == "EUR" {
				q.Price = "91.0000"
			} else {
				q.OrigPrice = "101.0000"
			}
			q.FetchedAt = q.FetchedAt.Add(time.Minute)
			if err := s.SaveQuote(context.Background(), &q, ""); err != nil {
				t.Fatal(err)
			}
			if q.ID == oldID {
				t.Fatalf("changed %s price reused ID %d", field, oldID)
			}
			qaQuoteCount(t, s, 2)
		})
	}
}

func TestSaveQuoteConcurrentDuplicatesQA(t *testing.T) {
	s := featureStore(t)
	const workers = 12
	quotes := make([]marketdata.Quote, workers)
	errs := make([]error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range quotes {
		quotes[i] = qaDedupeQuote()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = s.SaveQuote(context.Background(), &quotes[i], "")
		}()
	}
	close(start)
	wg.Wait()
	for i := range quotes {
		if errs[i] != nil || quotes[i].ID <= 0 || quotes[i].ID != quotes[0].ID {
			t.Fatalf("worker %d: ID=%d first=%d err=%v", i, quotes[i].ID, quotes[0].ID, errs[i])
		}
	}
	qaQuoteCount(t, s, 1)
}

func TestSaveQuoteDuplicateFreshnessAndReusedIDQA(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	portfolioID := favoritePortfolio(t, s, "alice", "Dedupe", 1)
	q := qaDedupeQuote()
	if err := s.SaveQuote(ctx, &q, ""); err != nil {
		t.Fatal(err)
	}
	firstID := q.ID
	if _, err := s.CreateTransaction(ctx, "alice", portfolioID, TransactionInput{Date: "2026-11-02", Lines: []TransactionLine{{InstrumentID: 1, Amount: "90", Quantity: "1", Price: "90", QuoteID: firstID}}}); err != nil {
		t.Fatal(err)
	}
	before, err := s.Summaries(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	q.ID = 0
	q.FetchedAt = q.FetchedAt.Add(30 * time.Minute)
	if err := s.SaveQuote(ctx, &q, ""); err != nil {
		t.Fatal(err)
	}
	if q.ID != firstID {
		t.Fatalf("duplicate ID = %d, want %d", q.ID, firstID)
	}
	qaQuoteCount(t, s, 1)
	newest, ok, err := s.NewestPricedFetchedAt(ctx, "alice")
	if err != nil || !ok || !newest.Equal(q.FetchedAt) {
		t.Fatalf("freshness = %v, %v, %v; want %v", newest, ok, err, q.FetchedAt)
	}
	after, err := s.Summaries(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range []*Summary{&after.Summaries[0].Summary, &after.Total.Summary} {
		if summary.AsOf == nil || !summary.AsOf.FetchedAt.Equal(q.FetchedAt) {
			t.Fatalf("summary fetch time not updated: %+v", summary.AsOf)
		}
		// Only freshness changes; all price and performance figures stay identical.
		summary.AsOf.FetchedAt = before.Total.AsOf.FetchedAt
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("duplicate refresh changed summary figures: before=%+v after=%+v", before, after)
	}
	if _, err := s.CreateTransaction(ctx, "alice", portfolioID, TransactionInput{Date: "2026-11-03", Lines: []TransactionLine{{InstrumentID: 1, Amount: "180", Quantity: "2", Price: "90", QuoteID: q.ID}}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Transactions(ctx, "alice", portfolioID, 1, 50)
	if err != nil || len(rows.Lines) != 2 {
		t.Fatalf("transactions=%+v err=%v", rows, err)
	}
	for _, line := range rows.Lines {
		if line.OrigPrice != q.OrigPrice || line.FXRate != q.FXRate || line.FXRateDate != q.FXRateDate {
			t.Fatalf("reused quote ID lost transaction provenance: %+v", line)
		}
	}
}

func TestSaveQuoteOlderDuplicateCannotRegressFreshnessQA(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	favoritePortfolio(t, s, "alice", "Favorite quote", 1)
	star(t, s, "alice", 1)
	newer := qaDedupeQuote()
	if err := s.SaveQuote(ctx, &newer, ""); err != nil {
		t.Fatal(err)
	}
	older := newer
	older.ID = 0
	older.FetchedAt = newer.FetchedAt.Add(-time.Hour)
	if err := s.SaveQuote(ctx, &older, ""); err != nil {
		t.Fatal(err)
	}
	if older.ID != newer.ID {
		t.Fatalf("older identical fetch ID=%d, want reused ID=%d", older.ID, newer.ID)
	}
	qaQuoteCount(t, s, 1)
	latest, err := s.latestQuotes(ctx, []int64{1})
	if err != nil || latest[1] == nil || !latest[1].FetchedAt.Equal(newer.FetchedAt) {
		t.Errorf("older duplicate regressed latest quote: %+v, err=%v; want fetch %v", latest[1], err, newer.FetchedAt)
	}
	fetched, ok, err := s.NewestPricedFetchedAt(ctx, "alice")
	if err != nil || !ok || !fetched.Equal(newer.FetchedAt) {
		t.Errorf("older duplicate regressed user freshness: %v, %v, %v; want %v", fetched, ok, err, newer.FetchedAt)
	}
}

func TestTransactionMissingDraftQuoteRollsBackQA(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		for _, missingFirst := range []bool{false, true} {
			name := "unknown"
			if deleted {
				name = "pruned"
			}
			if missingFirst {
				name += "/first line"
			} else {
				name += "/last line"
			}
			t.Run(name, func(t *testing.T) {
				s := featureStore(t)
				ctx := context.Background()
				portfolioID := favoritePortfolio(t, s, "alice", "Draft", 1, 2)
				valid := qaDedupeQuote()
				if err := s.SaveQuote(ctx, &valid, ""); err != nil {
					t.Fatal(err)
				}
				missingID := int64(1000000)
				if deleted {
					pruned := valid
					pruned.InstrumentID = 2
					if err := s.SaveQuote(ctx, &pruned, ""); err != nil {
						t.Fatal(err)
					}
					missingID = pruned.ID
					if _, err := s.db.Exec("DELETE FROM price_quotes WHERE id=?", missingID); err != nil {
						t.Fatal(err)
					}
				}
				lines := []TransactionLine{
					{InstrumentID: 1, Amount: "90", Price: "90", Quantity: "1", QuoteID: valid.ID},
					{InstrumentID: 2, Amount: "90", Price: "90", Quantity: "1", QuoteID: missingID},
				}
				if missingFirst {
					lines[0], lines[1] = lines[1], lines[0]
				}
				_, err := s.CreateTransaction(ctx, "alice", portfolioID, TransactionInput{Date: "2026-11-02", Lines: lines})
				var validation *ValidationError
				missingIndex := 1
				if missingFirst {
					missingIndex = 0
				}
				if !errors.As(err, &validation) || validation.Code != "VALIDATION_ERROR" || validation.Fields[fmt.Sprintf("lines.%d.quoteId", missingIndex)] == "" {
					t.Errorf("missing nonzero quoteId must return a validation error, got %v", err)
				}
				for _, table := range []string{"transactions", "transaction_lines"} {
					var count int
					if err := s.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
						t.Errorf("missing draft quote left %d rows in %s, want rollback; err=%v", count, table, err)
					}
				}
			})
		}
	}
}
