package marketdata

import (
	"encoding/json"
	"testing"
	"time"
)

// Yahoo's 5d chartPreviousClose predates the first bar. Only the dated bars can
// identify the previous completed trading session.
func summaryChart(t *testing.T, at time.Time, timezone string, days []time.Time, closes []any) []byte {
	t.Helper()
	stamps := make([]int64, len(days))
	for i, day := range days {
		stamps[i] = day.Unix()
	}
	result := map[string]any{"meta": map[string]any{"symbol": "TEST", "currency": "USD", "regularMarketPrice": json.Number("120"), "regularMarketTime": at.Unix(), "exchangeTimezoneName": timezone, "chartPreviousClose": json.Number("999")}, "timestamp": stamps, "indicators": map[string]any{"quote": []any{map[string]any{"close": closes}}}}
	b, err := json.Marshal(map[string]any{"chart": map[string]any{"result": []any{result}, "error": nil}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSummaryPreviousCloseUsesDatedCompletedSessions(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 11, d, 21, 0, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name, timezone     string
		at                 time.Time
		days               []time.Time
		closes             []any
		wantDay, wantClose string
	}{
		{"missing bars never use undated chart meta", "UTC", day(13), nil, nil, "", ""},
		{"length mismatch is unavailable", "UTC", day(13), []time.Time{day(12)}, []any{json.Number("100"), json.Number("120")}, "", ""},
		{"current session bar is excluded", "UTC", day(13), []time.Time{day(12), day(13)}, []any{json.Number("100"), json.Number("120")}, "2026-11-12", "100.0000"},
		{"holiday null uses earlier completed session", "UTC", day(13), []time.Time{day(11), day(12), day(13)}, []any{json.Number("100"), nil, json.Number("120")}, "2026-11-11", "100.0000"},
		{"unsorted bars still choose newest completed day", "UTC", day(13), []time.Time{day(12), day(13), day(11)}, []any{json.Number("101"), json.Number("120"), json.Number("100")}, "2026-11-12", "101.0000"},
		{"exchange session date wins over UTC midnight", "America/New_York", time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC), []time.Time{day(12), day(13)}, []any{json.Number("100"), json.Number("120")}, "2026-11-12", "100.0000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, err := ParseYahooLatest(summaryChart(t, tc.at, tc.timezone, tc.days, tc.closes))
			if err != nil {
				t.Fatal(err)
			}
			if q.Price != "120.0000" || q.PrevDay != tc.wantDay || q.PrevClose != tc.wantClose {
				t.Fatalf("latest %+v", q)
			}
		})
	}
}

func TestSummaryPreviousFXCloseNeverSubstitutesANeighbour(t *testing.T) {
	rate := &FXRate{Date: "2026-11-13", Rate: 90_000_000, Closes: map[string]int64{"2026-11-11": 80_000_000, "2026-11-13": 90_000_000}}
	if got, ok := rate.CloseOn("2026-11-11"); !ok || got != 80_000_000 {
		t.Fatalf("exact %d %v", got, ok)
	}
	for _, day := range []string{"2026-11-10", "2026-11-12", "2026-11-14"} {
		if got, ok := rate.CloseOn(day); ok || got != 0 {
			t.Fatalf("neighbour for %s = %d %v", day, got, ok)
		}
	}
	for _, r := range []*FXRate{nil, {Rate: 90_000_000, Source: "ECB"}} {
		if got, ok := r.CloseOn("2026-11-11"); ok || got != 0 {
			t.Fatalf("undated/ECB %d %v", got, ok)
		}
	}
}
