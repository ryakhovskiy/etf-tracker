package marketdata

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FXRate struct {
	Date  string `json:"date"`
	Base  string `json:"base"`
	Quote string `json:"quote"`
	Rate  int64  `json:"-"`
}
type FXProvider interface {
	Currencies(context.Context) (map[string]bool, error)
	Rates(context.Context, string, string, string) ([]FXRate, error)
}
type FXStore interface {
	CachedFX(context.Context, string, string, string) (*FXRate, error)
	FXCoverage(context.Context, string, string) (string, time.Time, error)
	SaveFX(context.Context, []FXRate, string, time.Time) error
}
type FXService struct {
	Store       FXStore
	Provider    FXProvider
	ProviderKey string
	Now         func() time.Time
	mu          sync.Mutex
	// checked records successful fetches that returned no new rows (e.g. holidays).
	checked map[string]time.Time
}

// publicationCutoff is when the ECB publication relevant for date d is
// certainly available: 17:00 Berlin on d, or on the preceding weekday.
func publicationCutoff(d time.Time) time.Time {
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 17, 0, 0, 0, berlin)
}

func (s *FXService) Rate(ctx context.Context, currency, date string) (*FXRate, error) {
	if currency == "EUR" {
		return nil, nil
	}
	if currency == "GBX" || currency == "GBp" {
		currency = "GBP"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return nil, ErrFXUnavailable
	}
	cached, err := s.Store.CachedFX(ctx, currency, date, s.ProviderKey)
	if err != nil {
		return nil, err
	}
	newest, fetched, err := s.Store.FXCoverage(ctx, currency, s.ProviderKey)
	if err != nil {
		return nil, err
	}
	if t := s.checked[currency]; t.After(fetched) {
		fetched = t
	}
	// The cache answers for d only when no later publication on or before d can
	// exist: d's own row is stored, later data is stored, or the last fetch ran
	// after d's publication. Today's publication may still change, so it is refreshed.
	covered := cached != nil && (cached.Date == date || newest > date || !fetched.Before(publicationCutoff(d)))
	if covered && cached.Date != BerlinDay(s.Now()) {
		published, _ := time.Parse("2006-01-02", cached.Date)
		if d.Sub(published) <= 7*24*time.Hour {
			return cached, nil
		}
	}
	// A persisted publication already establishes support for this currency.
	// In particular, cache hits remain usable offline after a restart.
	if newest == "" {
		currencies, err := s.Provider.Currencies(ctx)
		if err != nil {
			return nil, err
		}
		if !currencies[currency] {
			return nil, ErrFXUnsupported
		}
	}
	// Continue after the newest stored publication; refetch d itself when it is
	// the newest (today's row); start at d-10 when nothing usable is stored.
	from := d.AddDate(0, 0, -10).Format("2006-01-02")
	if newest != "" && newest <= date {
		last, _ := time.Parse("2006-01-02", newest)
		from = last.AddDate(0, 0, 1).Format("2006-01-02")
		if newest == date {
			from = date
		}
	}
	rates, err := s.Provider.Rates(ctx, currency, from, date)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	if err = s.Store.SaveFX(ctx, rates, s.ProviderKey, now); err != nil {
		return nil, err
	}
	if s.checked == nil {
		s.checked = map[string]time.Time{}
	}
	s.checked[currency] = now
	cached, err = s.Store.CachedFX(ctx, currency, date, s.ProviderKey)
	if err != nil {
		return nil, err
	}
	if cached == nil {
		return nil, ErrFXUnavailable
	}
	published, _ := time.Parse("2006-01-02", cached.Date)
	if d.Sub(published) > 7*24*time.Hour {
		return nil, ErrFXUnavailable
	}
	return cached, nil
}

type Frankfurter struct {
	HTTP              *http.Client
	BaseURL, Provider string
	Cache             *Cache
	Sleep             func(context.Context, time.Duration) error
}

func (f *Frankfurter) get(ctx context.Context, path string, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(f.BaseURL, "/")+path, nil)
		if err != nil {
			return ErrUpstream
		}
		res, err := f.HTTP.Do(req)
		if err != nil {
			return ErrUpstream
		}
		if res.StatusCode == 503 && attempt == 0 {
			delay, err := strconv.Atoi(res.Header.Get("Retry-After"))
			if err != nil {
				if t, e := http.ParseTime(res.Header.Get("Retry-After")); e == nil {
					delay = max(0, int(time.Until(t).Seconds()))
				} else {
					res.Body.Close()
					return ErrUpstream
				}
			}
			res.Body.Close()
			if delay < 0 {
				delay = 0
			}
			if delay > 30 {
				delay = 30
			}
			sleep := f.Sleep
			if sleep == nil {
				sleep = waitContext
			}
			if err = sleep(ctx, time.Duration(delay)*time.Second); err != nil {
				return err
			}
			continue
		}
		if res.StatusCode == 404 || res.StatusCode == 422 {
			res.Body.Close()
			return ErrFXUnavailable
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			return ErrUpstream
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
		res.Body.Close()
		if err != nil {
			return ErrUpstream
		}
		return nil
	}
	return ErrUpstream
}
func (f *Frankfurter) Currencies(ctx context.Context) (map[string]bool, error) {
	key := "fx-currencies:" + f.Provider
	var rows []struct {
		ISOCode string `json:"iso_code"`
		Code    string `json:"code"`
	}
	if b, ok := f.Cache.Get(key); ok {
		if json.Unmarshal(b, &rows) != nil {
			return nil, ErrUpstream
		}
	} else {
		if err := f.get(ctx, "/currencies?providers="+url.QueryEscape(f.Provider), &rows); err != nil {
			return nil, err
		}
		b, _ := json.Marshal(rows)
		f.Cache.Set(key, b)
	}
	out := map[string]bool{}
	for _, r := range rows {
		code := r.ISOCode
		if code == "" {
			code = r.Code
		}
		out[code] = true
	}
	return out, nil
}
func (f *Frankfurter) Rates(ctx context.Context, currency, from, to string) ([]FXRate, error) {
	var rows []struct {
		Date, Base, Quote string
		Rate              json.Number
	}
	path := "/providers/" + url.PathEscape(f.Provider) + "/rates?" + url.Values{"base": {currency}, "quotes": {"EUR"}, "from": {from}, "to": {to}}.Encode()
	if err := f.get(ctx, path, &rows); err != nil {
		return nil, err
	}
	out := []FXRate{}
	for _, r := range rows {
		if r.Base == r.Quote {
			continue
		}
		if r.Base != currency || r.Quote != "EUR" || r.Date < from || r.Date > to {
			return nil, ErrUpstream
		}
		if _, err := time.Parse("2006-01-02", r.Date); err != nil {
			return nil, ErrUpstream
		}
		n, err := Scaled(r.Rate.String(), 8)
		if err != nil {
			return nil, err
		}
		out = append(out, FXRate{r.Date, r.Base, r.Quote, n})
	}
	return out, nil
}

type MockFX struct{}

func (MockFX) Currencies(context.Context) (map[string]bool, error) {
	return map[string]bool{"USD": true, "GBP": true, "CHF": true, "JPY": true, "EUR": true}, nil
}
func (MockFX) Rates(ctx context.Context, currency, from, to string) ([]FXRate, error) {
	var values map[string]string
	b, err := fixtures.ReadFile("testdata/marketdata/fx.json")
	if err != nil || json.Unmarshal(b, &values) != nil {
		return nil, ErrUpstream
	}
	value, ok := values[currency]
	if !ok {
		return nil, ErrFXUnsupported
	}
	n, err := Scaled(value, 8)
	if err != nil {
		return nil, err
	}
	d, _ := time.Parse("2006-01-02", to)
	if to == BerlinDay(time.Now()) && time.Now().In(berlin).Hour() < 16 {
		d = d.AddDate(0, 0, -1)
	}
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return []FXRate{{d.Format("2006-01-02"), currency, "EUR", n}}, nil
}
