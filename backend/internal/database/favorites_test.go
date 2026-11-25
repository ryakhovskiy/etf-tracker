package database

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// favoritePortfolio creates a portfolio for user holding the instruments.
func favoritePortfolio(t *testing.T, s *Store, user, name string, ids ...int64) int64 {
	t.Helper()
	ctx := context.Background()
	p, err := s.SavePortfolio(ctx, user, 0, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddItems(ctx, user, p.ID, ids); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func star(t *testing.T, s *Store, user string, ids ...int64) {
	t.Helper()
	for _, id := range ids {
		if _, err := s.StarFavorite(context.Background(), user, id); err != nil {
			t.Fatalf("star %d: %v", id, err)
		}
	}
}

// favoriteOrder returns the favorite ids in order and fails unless the positions
// are exactly 0..n-1.
func favoriteOrder(t *testing.T, s *Store, user string) []int64 {
	t.Helper()
	list, err := s.Favorites(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	ids := []int64{}
	for i, f := range list {
		if f.Position != i {
			t.Fatalf("positions not contiguous from 0: %+v", list)
		}
		ids = append(ids, f.InstrumentID)
	}
	return ids
}

func TestFavoritesStarOrderUnstarAndCompaction(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	favoritePortfolio(t, s, "alice", "Main", 1, 2, 3, 4)
	for i, id := range []int64{3, 1, 2} {
		if pos, err := s.StarFavorite(ctx, "alice", id); err != nil || pos != i {
			t.Fatalf("star %d: position %d err %v", id, pos, err)
		}
	}
	if pos, err := s.StarFavorite(ctx, "alice", 1); err != nil || pos != 1 {
		t.Fatalf("starring again must keep the place: %d %v", pos, err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{3, 1, 2}) {
		t.Fatalf("star order %v", got)
	}
	if err := s.ReorderFavorites(ctx, "alice", []int64{2, 3, 1}); err != nil {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{2, 3, 1}) {
		t.Fatalf("reordered %v", got)
	}
	// Unstar the first: the rest move up without gaps.
	if err := s.UnstarFavorite(ctx, "alice", 2); err != nil {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{3, 1}) {
		t.Fatalf("compacted %v", got)
	}
	if err := s.UnstarFavorite(ctx, "alice", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unstar twice: %v", err)
	}
	// A new star appends after the compacted tail.
	star(t, s, "alice", 4)
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{3, 1, 4}) {
		t.Fatalf("append after compaction %v", got)
	}
	// An instrument outside the user's portfolios cannot be starred.
	if _, err := s.StarFavorite(ctx, "alice", 5); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unheld star: %v", err)
	}
	if _, err := s.StarFavorite(ctx, "alice", 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown instrument star: %v", err)
	}
}

func TestFavoritesStaleOrderChangesNothing(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	favoritePortfolio(t, s, "alice", "Main", 1, 2, 3)
	star(t, s, "alice", 1, 2, 3)
	for name, ids := range map[string][]int64{
		"missing":   {3, 1},
		"extra":     {3, 1, 2, 4},
		"duplicate": {3, 3, 1},
		"foreign":   {3, 1, 9},
		"empty":     {},
	} {
		if err := s.ReorderFavorites(ctx, "alice", ids); !errors.Is(err, ErrFavoritesStale) {
			t.Fatalf("%s: %v", name, err)
		}
		if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{1, 2, 3}) {
			t.Fatalf("%s changed the order to %v", name, got)
		}
	}
	// Same set in the current order is a valid no-op.
	if err := s.ReorderFavorites(ctx, "alice", []int64{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	// No favorites at all: only the empty list is current.
	if err := s.ReorderFavorites(ctx, "bob", []int64{}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReorderFavorites(ctx, "bob", []int64{1}); !errors.Is(err, ErrFavoritesStale) {
		t.Fatal(err)
	}
}

func TestFavoritesCascadeOnItemAndPortfolioDelete(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	a := favoritePortfolio(t, s, "alice", "A", 1, 2, 3)
	b := favoritePortfolio(t, s, "alice", "B", 2, 4)
	star(t, s, "alice", 1, 2, 3, 4)

	// 2 is also in B: removing it from A keeps the favorite.
	if err := s.RemoveItem(ctx, "alice", a, 2); err != nil {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("still held elsewhere %v", got)
	}
	// 1 is in no other portfolio: removed and positions compacted.
	if err := s.RemoveItem(ctx, "alice", a, 1); err != nil {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{2, 3, 4}) {
		t.Fatalf("last holder removed %v", got)
	}
	// A refused removal (item has transactions) changes nothing.
	if _, err := s.CreateTransaction(ctx, "alice", a, TransactionInput{Date: "2026-11-02", Lines: []TransactionLine{{InstrumentID: 3, Amount: "100", Price: "100", Quantity: "1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveItem(ctx, "alice", a, 3); !errors.Is(err, ErrItemHasTransactions) {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{2, 3, 4}) {
		t.Fatalf("refused removal changed favorites %v", got)
	}
	// Deleting B drops 2 and 4 (2 left A earlier, so only B held it); 3 stays via A.
	if err := s.DeletePortfolio(ctx, "alice", b); err != nil {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{3}) {
		t.Fatalf("portfolio delete %v", got)
	}
	if err := s.DeletePortfolio(ctx, "alice", a); err != nil {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); len(got) != 0 {
		t.Fatalf("all portfolios gone %v", got)
	}
	// A refused delete (unknown/foreign portfolio) leaves favorites alone.
	c := favoritePortfolio(t, s, "bob", "C", 1)
	star(t, s, "bob", 1)
	if err := s.DeletePortfolio(ctx, "alice", c); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "bob"); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("foreign delete changed favorites %v", got)
	}
}

func TestFavoritesPortfolioDeleteKeepsFavoriteHeldElsewhere(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	a := favoritePortfolio(t, s, "alice", "A", 1, 2)
	favoritePortfolio(t, s, "alice", "B", 2)
	star(t, s, "alice", 1, 2)
	if err := s.DeletePortfolio(ctx, "alice", a); err != nil {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("%v", got)
	}
}

func TestFavoritesUserIsolation(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	a := favoritePortfolio(t, s, "alice", "A", 1, 2)
	favoritePortfolio(t, s, "bob", "B", 2, 3)
	star(t, s, "alice", 1, 2)
	// bob holds 2 but not 1.
	if _, err := s.StarFavorite(ctx, "bob", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("starred another user's title: %v", err)
	}
	if got := favoriteOrder(t, s, "bob"); len(got) != 0 {
		t.Fatalf("bob sees %v", got)
	}
	if err := s.UnstarFavorite(ctx, "bob", 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unstarred another user's favorite: %v", err)
	}
	if err := s.ReorderFavorites(ctx, "bob", []int64{2, 1}); !errors.Is(err, ErrFavoritesStale) {
		t.Fatal(err)
	}
	star(t, s, "bob", 3, 2)
	if err := s.ReorderFavorites(ctx, "bob", []int64{2, 3}); err != nil {
		t.Fatal(err)
	}
	if got := favoriteOrder(t, s, "alice"); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("alice affected by bob: %v", got)
	}
	if got := favoriteOrder(t, s, "bob"); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatalf("bob %v", got)
	}
	// Item listings carry only the caller's own star.
	items, err := s.PortfolioItems(ctx, "alice", a)
	if err != nil || len(items) != 2 || !items[0].Favorite || !items[1].Favorite {
		t.Fatalf("alice items %+v %v", items, err)
	}
	if err := s.UnstarFavorite(ctx, "alice", 2); err != nil {
		t.Fatal(err)
	}
	items, _ = s.PortfolioItems(ctx, "alice", a)
	byID := map[int64]bool{}
	for _, it := range items {
		byID[it.InstrumentID] = it.Favorite
	}
	if !byID[1] || byID[2] {
		t.Fatalf("favorite flag per item %v", byID)
	}
	if got := favoriteOrder(t, s, "bob"); !reflect.DeepEqual(got, []int64{2, 3}) {
		t.Fatalf("bob lost a star when alice unstarred: %v", got)
	}
}

func TestFavoritesQuoteFiguresAndReasons(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	favoritePortfolio(t, s, "alice", "A", 1)
	star(t, s, "alice", 1)

	list, _ := s.Favorites(ctx, "alice")
	if len(list) != 1 || list[0].PriceEUR != nil || list[0].Change != nil || list[0].AsOf != nil || !reflect.DeepEqual(list[0].Reasons, []string{"NO_QUOTE"}) || list[0].Name == "" || list[0].Ticker == "" {
		t.Fatalf("no quote: %+v", list)
	}

	for _, tc := range []struct {
		name, currency, price, original, previous, rate, previousRate string
		price4, change, percent                                       string
		reasons                                                       []string
	}{
		{"EUR", "EUR", "42.0400", "42.0400", "42.6800", "", "", "42.0400", "-0.64", "-1.50", []string{}},
		{"USD with the previous day's rate", "USD", "108.0000", "120.0000", "100.0000", "0.90000000", "0.80000000", "108.0000", "28.00", "35.00", []string{}},
		{"USD without the previous rate gives no change", "USD", "108.0000", "120.0000", "100.0000", "0.90000000", "", "108.0000", "", "", []string{"FX_PREVIOUS_UNAVAILABLE"}},
		{"pence convert through GBP once", "GBp", "58.9086", "4930.0000", "4900.0000", "1.19490000", "1.19700000", "58.9086", "0.26", "0.44", []string{}},
		{"South African cents convert through ZAR", "ZAc", "0.5000", "1000.0000", "900.0000", "0.05000000", "0.04000000", "0.5000", "0.14", "38.89", []string{}},
		{"no previous close", "EUR", "10.0000", "10.0000", "", "", "", "10.0000", "", "", []string{"NO_PREVIOUS_CLOSE"}},
		{"foreign without any rate", "USD", "9.0000", "10.0000", "10.0000", "", "", "9.0000", "", "", []string{"NO_PREVIOUS_CLOSE"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := summaryTestQuote(1, tc.price, tc.previous)
			q.Currency, q.OrigPrice, q.FXRate, q.PrevFXRate = tc.currency, tc.original, tc.rate, tc.previousRate
			q.QuoteDate = "2026-11-13"
			q.FetchedAt = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC).Add(time.Duration(len(tc.name)) * time.Minute)
			if err := s.SaveQuote(ctx, q, ""); err != nil {
				t.Fatal(err)
			}
			// Make this the newest quote regardless of insertion order.
			if _, err := s.db.ExecContext(ctx, "DELETE FROM price_quotes WHERE instrument_id=1 AND id<?", lastQuoteID(t, s)); err != nil {
				t.Fatal(err)
			}
			list, err := s.Favorites(ctx, "alice")
			if err != nil || len(list) != 1 {
				t.Fatalf("%v %+v", err, list)
			}
			f := list[0]
			if f.PriceEUR == nil || *f.PriceEUR != tc.price4 {
				t.Fatalf("price %v want %s", f.PriceEUR, tc.price4)
			}
			if f.AsOf == nil || f.AsOf.QuoteDate != "2026-11-13" || f.AsOf.QuoteTime == nil || !f.AsOf.QuoteTime.Equal(q.QuoteTime) || !f.AsOf.FetchedAt.Equal(q.FetchedAt) || !f.AsOf.Realtime || !reflect.DeepEqual(f.AsOf.Providers, []string{"YAHOO"}) {
				t.Fatalf("a quoted favorite must carry asOf: %+v", f.AsOf)
			}
			if tc.change == "" {
				if f.Change != nil {
					t.Fatalf("change %+v", f.Change)
				}
			} else if f.Change == nil || f.Change.Amount != tc.change || f.Change.Percent == nil || *f.Change.Percent != tc.percent {
				t.Fatalf("change %+v want %s (%s%%)", f.Change, tc.change, tc.percent)
			}
			if !reflect.DeepEqual(f.Reasons, tc.reasons) {
				t.Fatalf("reasons %v want %v", f.Reasons, tc.reasons)
			}
		})
	}
}

func lastQuoteID(t *testing.T, s *Store) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRow("SELECT MAX(id) FROM price_quotes WHERE instrument_id=1").Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFavoritesJoinTheRefreshSet(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	favoritePortfolio(t, s, "alice", "A", 1, 2, 3)
	// Hold 1 only; star 1 (held and favorite), 2 (not held) and nothing else.
	b := favoritePortfolio(t, s, "alice", "B", 1)
	if _, err := s.CreateTransaction(ctx, "alice", b, TransactionInput{Date: "2026-11-02", Lines: []TransactionLine{{InstrumentID: 1, Amount: "100", Price: "100", Quantity: "1"}}}); err != nil {
		t.Fatal(err)
	}
	ids := func(user string) []int64 {
		rows, err := s.UserPricedInstruments(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		out := []int64{}
		for _, r := range rows {
			out = append(out, r.InstrumentID)
		}
		return out
	}
	if got := ids("alice"); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("held only %v", got)
	}
	star(t, s, "alice", 2, 1)
	if got := ids("alice"); !reflect.DeepEqual(got, []int64{1, 2}) {
		t.Fatalf("held + favorites, de-duplicated and sorted: %v", got)
	}
	if got := ids("bob"); len(got) != 0 {
		t.Fatalf("another user's favorites leaked into the refresh set: %v", got)
	}
	// Freshness considers the favorite's quote even though it is not held.
	if _, ok, err := s.NewestPricedFetchedAt(ctx, "alice"); err != nil || ok {
		t.Fatalf("no quotes yet: %v %v", ok, err)
	}
	q := summaryTestQuote(2, "50", "49")
	if err := s.SaveQuote(ctx, q, ""); err != nil {
		t.Fatal(err)
	}
	newest, ok, err := s.NewestPricedFetchedAt(ctx, "alice")
	if err != nil || !ok || !newest.Equal(q.FetchedAt) {
		t.Fatalf("favorite quote ignored: %v %v %v", newest, ok, err)
	}
	// Unstarring leaves the set (still-held titles stay).
	if err := s.UnstarFavorite(ctx, "alice", 2); err != nil {
		t.Fatal(err)
	}
	if got := ids("alice"); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("after unstar %v", got)
	}
}
