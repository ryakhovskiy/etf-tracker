package marketdata

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestParseYahooDividendsReadsRecordedFixtures(t *testing.T) {
	// Quarterly distributing ETF in EUR.
	h, err := ParseYahooDividends(yfixture(t, "chart-div-ispa"), "2025-01-01", "2026-11-07")
	if err != nil || h.Symbol != "ISPA.DE" || h.Currency != "EUR" || len(h.Events) != 8 {
		t.Fatalf("%+v %v", h, err)
	}
	if e := h.Events[7]; e.ExDate != "2026-10-15" || e.AmountPerShare != "0.417165" {
		t.Fatalf("%+v", e)
	}
	if h.Events[0].ExDate != "2025-01-15" || h.Events[0].AmountPerShare != "0.370252" {
		t.Fatalf("events must be ascending and inside the range: %+v", h.Events[0])
	}
	// The close of the trading day before the ex-date, from the same response.
	if h.CloseBefore["2026-10-15"] != "39.9650" || h.CloseBefore["2025-01-15"] != "29.8600" {
		t.Fatal(h.CloseBefore)
	}
	if h.LatestClose != "39.9950" || h.LatestCloseDate != "2026-11-06" {
		t.Fatal(h.LatestClose, h.LatestCloseDate)
	}

	// Accumulating ETF: closes, but no events.
	h, err = ParseYahooDividends(yfixture(t, "chart-div-iwda"), "2023-01-01", "2026-11-07")
	if err != nil || len(h.Events) != 0 || h.Currency != "USD" || h.LatestCloseDate != "2026-11-06" {
		t.Fatalf("%+v %v", h, err)
	}

	// Stock in USD: ex-dates use the exchange time zone (09:30 New York).
	h, err = ParseYahooDividends(yfixture(t, "chart-div-aapl"), "2026-01-01", "2026-11-07")
	if err != nil || h.Currency != "USD" || len(h.Events) != 3 || h.Events[2].ExDate != "2026-08-10" || h.Events[2].AmountPerShare != "0.270000" {
		t.Fatalf("%+v %v", h, err)
	}
	// Monday ex-date: the close before is Friday's.
	if h.CloseBefore["2026-08-10"] != "313.3300" {
		t.Fatal(h.CloseBefore)
	}

	// London stock in pence: amounts and closes both in GBp.
	h, err = ParseYahooDividends(yfixture(t, "chart-div-ulvr"), "2026-08-01", "2026-08-31")
	if err != nil || h.Currency != "GBp" || len(h.Events) != 1 || h.Events[0].AmountPerShare != "39.820000" || h.CloseBefore["2026-08-06"] != "4774.0000" {
		t.Fatalf("%+v %v", h, err)
	}

	if _, err = ParseYahooDividends(yfixture(t, "chart-not-found"), "2026-01-01", "2026-11-07"); !errors.Is(err, ErrYahooNotFound) {
		t.Fatal(err)
	}
}

func TestParseYahooDividendsAddsSameDayEventsAndSkipsZero(t *testing.T) {
	body := `{"chart":{"result":[{"meta":{"currency":"EUR","symbol":"X.DE","exchangeTimezoneName":"Europe/Berlin"},
"timestamp":[1767254400,1767340800],"indicators":{"quote":[{"close":[10.0,null]}]},
"events":{"dividends":{"1767340800":{"amount":0.1,"date":1767340800},"1767344400":{"amount":0.0500004,"date":1767344400},"1767351600":{"amount":0,"date":1767351600}}}}],"error":null}}`
	h, err := ParseYahooDividends([]byte(body), "2026-01-01", "2026-12-31")
	if err != nil || len(h.Events) != 1 || h.Events[0].ExDate != "2026-01-02" || h.Events[0].AmountPerShare != "0.150000" {
		t.Fatalf("%+v %v", h, err)
	}
	// The null bar is skipped: the latest close is the last real one.
	if h.CloseBefore["2026-01-02"] != "10.0000" || h.LatestCloseDate != "2026-01-01" {
		t.Fatalf("%+v", h)
	}
}

type urlRecorder struct{ urls []string }

func (r *urlRecorder) Get(ctx context.Context, raw string) (int, http.Header, []byte, error) {
	r.urls = append(r.urls, raw)
	return MockYahoo{}.Get(ctx, raw)
}

func TestYahooDividendsRequestsDailyChartWithEvents(t *testing.T) {
	rec := &urlRecorder{}
	g := NewYahooGateway(&memStore{state: map[string]string{}}, rec, 100, 0, time.Hour)
	from, to := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	h, err := g.Dividends(context.Background(), "u", "ISPA.DE", from, to)
	if err != nil || len(h.Events) != 8 {
		t.Fatalf("%+v %v", h, err)
	}
	u, _ := url.Parse(rec.urls[len(rec.urls)-1])
	q := u.Query()
	// period1 starts 10 days early so the first ex-date has a close before it; period2 is exclusive.
	if u.Path != "/v8/finance/chart/ISPA.DE" || q.Get("events") != "div" || q.Get("interval") != "1d" || q.Get("crumb") != mockCrumb ||
		q.Get("period1") != "1734825600" || q.Get("period2") != "1794096000" {
		t.Fatal(rec.urls)
	}
	if _, err = g.Dividends(context.Background(), "u", "NOPE.XX", from, to); !errors.Is(err, ErrYahooNotFound) {
		t.Fatal(err)
	}
}
