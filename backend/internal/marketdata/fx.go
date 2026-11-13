package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
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
	// Source is empty for ECB reference rates and FXSourceYahoo for a live rate.
	Source string `json:"-"`
	// PrevRate is the previous close of a live Yahoo pair (scale 10^8); 0 when unknown.
	PrevRate int64 `json:"-"`
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
	// ranges records the [from, to] series fetched by RatesOn per currency, so a
	// date without a usable publication is not requested again on every load.
	ranges map[string][][2]string
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
	if MinorUnit(currency) {
		currency = MajorUnit(currency)
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

// RatesOn returns, for each date, the latest publication on or before it
// (accepted when at most 7 days old). Publications that are not stored yet are
// loaded with one time-series request covering every missing date. Dates
// without a usable publication are absent from the result. Dates after today
// use the latest publication, so callers pass today for forecasts.
func (s *FXService) RatesOn(ctx context.Context, currency string, dates []string) (map[string]*FXRate, error) {
	out := map[string]*FXRate{}
	currency = MajorUnit(currency)
	if currency == "EUR" || len(dates) == 0 {
		return out, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.Now()
	today := BerlinDay(now)
	newest, fetched, err := s.Store.FXCoverage(ctx, currency, s.ProviderKey)
	if err != nil {
		return nil, err
	}
	if t := s.checked[currency]; t.After(fetched) {
		fetched = t
	}
	usable := func(r *FXRate, date string) bool {
		if r == nil {
			return false
		}
		d, _ := time.Parse("2006-01-02", date)
		published, _ := time.Parse("2006-01-02", r.Date)
		return d.Sub(published) <= 7*24*time.Hour
	}
	var missing []string
	for _, date := range dates {
		if _, err := time.Parse("2006-01-02", date); err != nil {
			continue
		}
		if _, done := out[date]; done {
			continue
		}
		at := min(date, today) // a future date uses the latest publication
		d, _ := time.Parse("2006-01-02", at)
		cached, err := s.Store.CachedFX(ctx, currency, at, s.ProviderKey)
		if err != nil {
			return nil, err
		}
		// Same rule as Rate: no later publication on or before d can be missing.
		// For today, a fetch within the last hour that already has the previous
		// publication counts too, so page loads do not refetch before 16:00.
		covered := usable(cached, at) && (cached.Date == at || newest > at || !fetched.Before(publicationCutoff(d)) ||
			(at == today && cached.Date >= previousWeekday(today) && now.Sub(fetched) < time.Hour))
		if covered {
			out[date] = cached
			continue
		}
		// Already fetched for this date (and its 7 days before) without a usable publication.
		known := false
		for _, r := range s.ranges[currency] {
			known = known || (at < today && r[0] <= d.AddDate(0, 0, -7).Format("2006-01-02") && r[1] >= at)
		}
		if !known {
			missing = append(missing, date)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	if newest == "" {
		currencies, err := s.Provider.Currencies(ctx)
		if err != nil {
			return nil, err
		}
		if !currencies[currency] {
			return nil, ErrFXUnsupported
		}
	}
	sort.Strings(missing)
	first, _ := time.Parse("2006-01-02", min(missing[0], today))
	from, to := first.AddDate(0, 0, -10).Format("2006-01-02"), min(missing[len(missing)-1], today)
	rates, err := s.Provider.Rates(ctx, currency, from, to)
	if err != nil {
		return nil, err
	}
	if err = s.Store.SaveFX(ctx, rates, s.ProviderKey, now); err != nil {
		return nil, err
	}
	if s.checked == nil {
		s.checked = map[string]time.Time{}
	}
	if s.ranges == nil {
		s.ranges = map[string][][2]string{}
	}
	s.checked[currency] = now
	s.ranges[currency] = append(s.ranges[currency], [2]string{from, to})
	for _, date := range missing {
		at := min(date, today)
		cached, err := s.Store.CachedFX(ctx, currency, at, s.ProviderKey)
		if err != nil {
			return nil, err
		}
		if usable(cached, at) {
			out[date] = cached
		}
	}
	return out, nil
}

// previousWeekday is the weekday before day (YYYY-MM-DD).
func previousWeekday(day string) string {
	d, _ := time.Parse("2006-01-02", day)
	d = d.AddDate(0, 0, -1)
	for d.Weekday() == time.Saturday || d.Weekday() == time.Sunday {
		d = d.AddDate(0, 0, -1)
	}
	return d.Format("2006-01-02")
}

type Frankfurter struct {
	HTTP              *http.Client
	BaseURL, Provider string
	Cache             *Cache
	Sleep             func(context.Context, time.Duration) error
	Log               *slog.Logger
}

func (f *Frankfurter) get(ctx context.Context, path string, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(f.BaseURL, "/")+path, nil)
		if err != nil {
			return ErrUpstream
		}
		started := time.Now()
		res, err := f.HTTP.Do(req)
		if err != nil {
			logUpstream(f.Log, slog.LevelError, "FRANKFURTER", "request failed", "path", path, "elapsed", time.Since(started).Round(time.Millisecond).String(), "error", cleanErr(err))
			return ErrUpstream
		}
		if res.StatusCode == 503 && attempt == 0 {
			delay, err := strconv.Atoi(res.Header.Get("Retry-After"))
			if err != nil {
				if t, e := http.ParseTime(res.Header.Get("Retry-After")); e == nil {
					delay = max(0, int(time.Until(t).Seconds()))
				} else {
					res.Body.Close()
					logUpstream(f.Log, slog.LevelError, "FRANKFURTER", "503 without usable Retry-After", "path", path)
					return ErrUpstream
				}
			}
			res.Body.Close()
			logUpstream(f.Log, slog.LevelWarn, "FRANKFURTER", "503; retrying once", "path", path, "retryAfterSeconds", delay)
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
			logUpstream(f.Log, slog.LevelWarn, "FRANKFURTER", "no rate available", "path", path, "status", res.StatusCode)
			return ErrFXUnavailable
		}
		if res.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
			res.Body.Close()
			logUpstream(f.Log, slog.LevelError, "FRANKFURTER", "error response", "path", path, "status", res.StatusCode, "body", snippet(body))
			return ErrUpstream
		}
		err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
		res.Body.Close()
		if err != nil {
			logUpstream(f.Log, slog.LevelError, "FRANKFURTER", "unreadable response", "path", path, "error", err.Error())
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
	reject := func(reason string, r any) ([]FXRate, error) {
		logUpstream(f.Log, slog.LevelError, "FRANKFURTER", "rejected rates response: "+reason, "currency", currency, "from", from, "to", to, "row", fmt.Sprintf("%+v", r))
		return nil, ErrUpstream
	}
	for _, r := range rows {
		if r.Base == r.Quote {
			continue
		}
		// A row before from is the latest earlier publication: Frankfurter returns
		// it when [from, to] has none yet (e.g. today before the ~16:00 ECB release).
		if r.Base != currency || r.Quote != "EUR" || r.Date > to {
			return reject("unexpected currency pair or date after the requested range", r)
		}
		if _, err := time.Parse("2006-01-02", r.Date); err != nil {
			return reject("invalid date", r)
		}
		n, err := Scaled(r.Rate.String(), 8)
		if err != nil {
			logUpstream(f.Log, slog.LevelError, "FRANKFURTER", "rejected rates response: invalid rate", "currency", currency, "row", fmt.Sprintf("%+v", r), "error", err.Error())
			return nil, err
		}
		out = append(out, FXRate{Date: r.Date, Base: r.Base, Quote: r.Quote, Rate: n})
	}
	dates := make([]string, len(out))
	for i, r := range out {
		dates[i] = r.Date
	}
	logUpstream(f.Log, slog.LevelInfo, "FRANKFURTER", "rates received", "currency", currency, "from", from, "to", to, "publications", strings.Join(dates, ","))
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
	// Like the time series: one publication per weekday in [from, to]. Today's
	// rate is published around 16:00 Berlin.
	d, err := time.Parse("2006-01-02", to)
	if err != nil {
		return nil, ErrFXUnavailable
	}
	start, err := time.Parse("2006-01-02", from)
	if err != nil || start.After(d) {
		start = d
	}
	if to == BerlinDay(time.Now()) && time.Now().In(berlin).Hour() < 16 {
		d = d.AddDate(0, 0, -1)
	}
	out := []FXRate{}
	for ; !d.Before(start); d = d.AddDate(0, 0, -1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			out = append(out, FXRate{Date: d.Format("2006-01-02"), Base: currency, Quote: "EUR", Rate: n})
		}
	}
	return out, nil
}

// MajorUnit maps a minor-unit code (GBp, GBX, ZAc) to its ISO currency.
func MajorUnit(c string) string {
	switch c {
	case "GBX", "GBp":
		return "GBP"
	case "ZAc":
		return "ZAR"
	}
	return c
}
