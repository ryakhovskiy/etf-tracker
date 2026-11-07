package marketdata_test

import (
	"context"
	"errors"
	"testing"
	"time"

	md "holdings/backend/internal/marketdata"
)

// weekdaySeries publishes a rate every weekday except the listed ECB holidays,
// each available from 16:00 Berlin on its day.
func weekdaySeries(t *testing.T, now *time.Time, calls *[]string, holidays ...string) fxFunc {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	skip := map[string]bool{}
	for _, h := range holidays {
		skip[h] = true
	}
	return fxFunc{rates: func(_ context.Context, c, from, to string) ([]md.FXRate, error) {
		*calls = append(*calls, c+" "+from+".."+to)
		out := []md.FXRate{}
		for d := mustDay(t, from); !d.After(mustDay(t, to)); d = d.AddDate(0, 0, 1) {
			day := d.Format("2006-01-02")
			if d.Weekday() == time.Saturday || d.Weekday() == time.Sunday || skip[day] || now.Before(time.Date(d.Year(), d.Month(), d.Day(), 16, 0, 0, 0, berlin)) {
				continue
			}
			out = append(out, md.FXRate{Date: day, Base: c, Quote: "EUR", Rate: 90000000 + int64(d.YearDay())})
		}
		return out, nil
	}}
}

func TestRatesOnLoadsOneSeriesPerCurrency(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, berlin) // Saturday
	var calls []string
	fx := &md.FXService{Store: s, Provider: weekdaySeries(t, &now, &calls, "2026-05-01"), ProviderKey: "ECB", Now: func() time.Time { return now }}

	dates := []string{"2026-05-01", "2026-03-07", "2026-11-07", "2027-01-15", "2026-03-07"}
	rates, err := fx.RatesOn(ctx, "USD", dates)
	if err != nil || len(calls) != 1 || calls[0] != "USD 2026-02-25..2026-11-07" {
		t.Fatal(err, calls)
	}
	for date, want := range map[string]string{
		"2026-05-01": "2026-04-30", // ECB holiday: previous publication
		"2026-03-07": "2026-03-06", // weekend: Friday's publication
		"2026-11-07": "2026-11-06", // today (Saturday): latest publication
		"2027-01-15": "2026-11-06", // future dates use the latest publication
	} {
		if r := rates[date]; r == nil || r.Date != want {
			t.Fatalf("%s: %+v", date, r)
		}
	}
	// Everything is stored: a second load sends nothing.
	if _, err = fx.RatesOn(ctx, "USD", dates); err != nil || len(calls) != 1 {
		t.Fatal(err, calls)
	}
	// Pence use GBP, with one request of its own. EUR needs none.
	if r, err := fx.RatesOn(ctx, "GBp", []string{"2026-08-06"}); err != nil || r["2026-08-06"] == nil || len(calls) != 2 || calls[1][:3] != "GBP" {
		t.Fatal(r, err, calls)
	}
	if r, err := fx.RatesOn(ctx, "EUR", []string{"2026-08-06"}); err != nil || len(r) != 0 || len(calls) != 2 {
		t.Fatal(r, err, calls)
	}
	if _, err = fx.RatesOn(ctx, "BTC", []string{"2026-08-06"}); !errors.Is(err, md.ErrFXUnsupported) {
		t.Fatal(err)
	}
}

func TestRatesOnTodayIsRefetchedAtMostHourly(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 11, 5, 13, 0, 0, 0, berlin) // Thursday, before today's publication
	var calls []string
	fx := &md.FXService{Store: s, Provider: weekdaySeries(t, &now, &calls), ProviderKey: "ECB", Now: func() time.Time { return now }}
	r, err := fx.RatesOn(ctx, "USD", []string{"2026-11-05"})
	if err != nil || r["2026-11-05"].Date != "2026-11-04" || len(calls) != 1 {
		t.Fatal(r, err, calls)
	}
	now = now.Add(30 * time.Minute)
	if _, err = fx.RatesOn(ctx, "USD", []string{"2026-11-05"}); err != nil || len(calls) != 1 {
		t.Fatal("refetched within the hour", calls)
	}
	now = now.Add(3 * time.Hour) // 16:30: today's publication exists
	if r, err = fx.RatesOn(ctx, "USD", []string{"2026-11-05"}); err != nil || r["2026-11-05"].Date != "2026-11-05" || len(calls) != 2 {
		t.Fatal(r, err, calls)
	}
}

func TestRatesOnRejectsStalePublications(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	var calls []string
	// Nothing published for three weeks in August: those dates have no usable rate.
	var gap []string
	for d := mustDay(t, "2026-08-03"); d.Before(mustDay(t, "2026-08-24")); d = d.AddDate(0, 0, 1) {
		gap = append(gap, d.Format("2006-01-02"))
	}
	fx := &md.FXService{Store: s, Provider: weekdaySeries(t, &now, &calls, gap...), ProviderKey: "ECB", Now: func() time.Time { return now }}
	r, err := fx.RatesOn(ctx, "USD", []string{"2026-08-05", "2026-08-20", "2026-08-25"})
	if err != nil || r["2026-08-05"] == nil || r["2026-08-05"].Date != "2026-07-31" || r["2026-08-20"] != nil || r["2026-08-25"] == nil || len(calls) != 1 {
		t.Fatal(r, err)
	}
	// The gap is known now: loading the page again sends no request.
	if r, err = fx.RatesOn(ctx, "USD", []string{"2026-08-20"}); err != nil || r["2026-08-20"] != nil || len(calls) != 1 {
		t.Fatal(r, err, calls)
	}
}
