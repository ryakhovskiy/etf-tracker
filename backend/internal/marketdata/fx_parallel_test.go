package marketdata_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	md "holdings/backend/internal/marketdata"
)

// Different currencies must not wait for each other's Frankfurter request: the
// provider holds each request until all three are in flight, which a service-wide
// lock across the request could never satisfy.
func TestFXRatesOfDifferentCurrenciesAreLoadedInParallel(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, berlin)
	var arrived atomic.Int32
	open := make(chan struct{})
	var timedOut atomic.Bool
	provider := fxFunc{rates: func(_ context.Context, c, _, to string) ([]md.FXRate, error) {
		if arrived.Add(1) == 3 {
			close(open)
		}
		select {
		case <-open:
		case <-time.After(2 * time.Second):
			timedOut.Store(true)
		}
		return []md.FXRate{{Date: to, Base: c, Quote: "EUR", Rate: 90000000}}, nil
	}}
	fx := &md.FXService{Store: store(t), Provider: provider, ProviderKey: "ECB", Now: func() time.Time { return now }}
	var wg sync.WaitGroup
	for _, c := range []string{"USD", "GBP", "CHF"} {
		wg.Go(func() {
			if r, err := fx.Rate(context.Background(), c, "2026-11-05"); err != nil || r == nil || r.Base != c {
				t.Errorf("%s: %+v %v", c, r, err)
			}
		})
	}
	wg.Wait()
	if timedOut.Load() {
		t.Fatal("the currencies were looked up one after another")
	}
}

// Concurrent calls for one currency and a final (past) date still cost one request:
// the per-currency lock makes the others find the stored rate.
func TestFXSameCurrencyAndDateIsLoadedOnce(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, berlin)
	var calls atomic.Int32
	release := make(chan struct{})
	provider := fxFunc{rates: func(_ context.Context, c, _, to string) ([]md.FXRate, error) {
		calls.Add(1)
		<-release
		return []md.FXRate{{Date: to, Base: c, Quote: "EUR", Rate: 90000000}}, nil
	}}
	fx := &md.FXService{Store: store(t), Provider: provider, ProviderKey: "ECB", Now: func() time.Time { return now }}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if r, err := fx.Rate(context.Background(), "USD", "2026-11-05"); err != nil || r == nil {
				t.Errorf("%+v %v", r, err)
			}
		})
	}
	time.Sleep(30 * time.Millisecond) // the callers pile up behind the one request; the count holds either way
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("%d Frankfurter requests for one currency and date", calls.Load())
	}
	// A later independent call is a cache hit, not a new request.
	if r, err := fx.Rate(context.Background(), "USD", "2026-11-05"); err != nil || r == nil || calls.Load() != 1 {
		t.Fatal(r, err, calls.Load())
	}
}
