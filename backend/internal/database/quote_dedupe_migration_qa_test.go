package database

import (
	"context"
	"reflect"
	"testing"
)

// Clone the raw stored values so these fixtures remain independent of SaveQuote's
// forward deduplication. Each test moves Goose back to 18 before seeding history.
func qaCloneQuote(t *testing.T, s *Store, id int64, changes map[string]any) int64 {
	t.Helper()
	const columns = "instrument_id,price_eur_e4,orig_price_e4,orig_currency,fx_rate_e8,fx_rate_date,quote_date,fetched_at,provider,quote_time,fx_source,prev_close_e4,prev_fx_rate_e8,prev_close_date"
	res, err := s.db.Exec("INSERT INTO price_quotes("+columns+") SELECT "+columns+" FROM price_quotes WHERE id=?", id)
	if err != nil {
		t.Fatal(err)
	}
	cloneID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	for column, value := range changes {
		if _, err := s.db.Exec("UPDATE price_quotes SET "+column+"=? WHERE id=?", value, cloneID); err != nil {
			t.Fatal(err)
		}
	}
	return cloneID
}

func qaQuoteIDs(t *testing.T, s *Store) []int64 {
	t.Helper()
	rows, err := s.db.Query("SELECT id FROM price_quotes ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestQuoteDedupeMigrationEachFieldAndNullDistinctionsQA(t *testing.T) {
	for _, tc := range []struct {
		column string
		null   bool
		other  any
	}{
		{"provider", false, "EULERPOOL"}, {"orig_currency", false, "GBP"},
		{"orig_price_e4", false, int64(1000001)}, {"price_eur_e4", false, int64(900001)},
		{"fx_rate_e8", false, int64(91000000)}, {"fx_rate_date", false, "2026-11-12"},
		{"fx_source", false, "ECB"}, {"quote_date", false, "2026-11-12"},
		{"quote_time", false, "2026-11-13T12:01:00Z"},
		{"prev_close_e4", false, int64(980000)}, {"prev_fx_rate_e8", false, int64(88000000)},
		{"prev_close_date", false, "2026-11-11"},
		{"fx_rate_e8", true, int64(0)}, {"prev_close_e4", true, int64(0)}, {"prev_fx_rate_e8", true, int64(0)},
		{"quote_time", true, ""}, {"fx_source", true, ""}, {"fx_rate_date", true, ""}, {"prev_close_date", true, ""},
	} {
		name := tc.column
		if tc.null {
			name += "/NULL versus zero or empty"
		}
		t.Run(name, func(t *testing.T) {
			s := featureStore(t)
			ctx := context.Background()
			provider, err := s.migrationProvider()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provider.DownTo(ctx, 18); err != nil {
				t.Fatal(err)
			}
			q := qaDedupeQuote()
			if err := s.SaveQuote(ctx, &q, ""); err != nil {
				t.Fatal(err)
			}
			if tc.null {
				if _, err := s.db.Exec("UPDATE price_quotes SET "+tc.column+"=NULL WHERE id=?", q.ID); err != nil {
					t.Fatal(err)
				}
			}
			b := qaCloneQuote(t, s, q.ID, map[string]any{tc.column: tc.other})
			qaCloneQuote(t, s, q.ID, nil)
			last := qaCloneQuote(t, s, q.ID, nil)
			// Equal fetch timestamps force id to break ties. A B A A retains the
			// first A and B, then only the last A. Quote dates form separate runs.
			want := []int64{q.ID, b, last}
			if tc.column == "quote_date" {
				want = []int64{b, last}
			}
			if applied, err := provider.Up(ctx); err != nil || len(applied) != 1 || applied[0].Source.Version != 19 {
				t.Fatalf("migration applied=%v err=%v", applied, err)
			}
			if got := qaQuoteIDs(t, s); !reflect.DeepEqual(got, want) {
				t.Fatalf("migration retained IDs %v, want %v", got, want)
			}
			if applied, err := provider.Up(ctx); err != nil || len(applied) != 0 {
				t.Fatalf("migration reapplied: %v, err=%v", applied, err)
			}
		})
	}
}

func TestQuoteDedupeMigrationFetchOrderPrecedesIDQA(t *testing.T) {
	s := featureStore(t)
	ctx := context.Background()
	provider, err := s.migrationProvider()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 18); err != nil {
		t.Fatal(err)
	}
	q := qaDedupeQuote()
	if err := s.SaveQuote(ctx, &q, ""); err != nil {
		t.Fatal(err)
	}
	// The lower ID has the newer fetch. In insertion order these are A B A;
	// fetch order is B A A, so the first A must be removed and the lower-ID
	// second A must survive.
	newestA := qaCloneQuote(t, s, q.ID, map[string]any{"fetched_at": "2026-11-13T14:00:00.000000000Z"})
	b := qaCloneQuote(t, s, q.ID, map[string]any{"fetched_at": "2026-11-13T11:00:00.000000000Z", "price_eur_e4": int64(910000)})
	qaCloneQuote(t, s, q.ID, map[string]any{"fetched_at": "2026-11-13T13:00:00.000000000Z"})
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if got, want := qaQuoteIDs(t, s), []int64{newestA, b}; !reflect.DeepEqual(got, want) {
		t.Fatalf("migration retained IDs %v, want newest-fetch IDs %v", got, want)
	}
}
