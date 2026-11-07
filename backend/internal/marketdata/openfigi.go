package marketdata

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ErrRateLimited = errors.New("FIGI_RATE_LIMITED")
var ErrUpstream = errors.New("UPSTREAM_ERROR")
var GermanCodes = map[string]bool{"GR": true, "GY": true, "GF": true, "GD": true, "GH": true, "GI": true, "GM": true, "GS": true, "GT": true, "GZ": true}

func Normalize(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

var isinPattern = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{9}[0-9]$`)
var wknPattern = regexp.MustCompile(`^[A-Z0-9]{6}$`)
var digitPattern = regexp.MustCompile(`[0-9]`)

func ValidISIN(s string) bool {
	if !isinPattern.MatchString(s) {
		return false
	}
	digits := ""
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			digits += fmt.Sprint(int(c-'A') + 10)
		} else {
			digits += string(c)
		}
	}
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		n := int(digits[i] - '0')
		if double {
			n *= 2
		}
		sum += n/10 + n%10
		double = !double
	}
	return sum%10 == 0
}
func Classify(q string) string {
	q = Normalize(q)
	if ValidISIN(q) {
		return "ID_ISIN"
	}
	if wknPattern.MatchString(q) && digitPattern.MatchString(q) {
		return "ID_WERTPAPIER"
	}
	return "KEYWORD"
}

type Listing struct {
	FIGI          string `json:"figi"`
	Ticker        string `json:"ticker"`
	ExchCode      string `json:"exchCode"`
	Name          string `json:"name"`
	SecurityType  string `json:"securityType"`
	SecurityType2 string `json:"securityType2"`
}
type SearchRequest struct {
	Query    string `json:"query"`
	ExchCode string `json:"exchCode"`
}
type MappingJob struct {
	IDType   string `json:"idType"`
	IDValue  string `json:"idValue"`
	ExchCode string `json:"exchCode"`
}
type Response struct {
	Data    []Listing `json:"data"`
	Warning string    `json:"warning,omitempty"`
	Error   string    `json:"error,omitempty"`
}
type FigiSearcher interface {
	Search(context.Context, SearchRequest) (Response, error)
}
type FigiMapper interface {
	Map(context.Context, []MappingJob) ([]Response, error)
}
type Provider interface {
	FigiSearcher
	FigiMapper
}
type Client struct {
	HTTP    *http.Client
	BaseURL string
	APIKey  string
	Cache   *Cache
	Log     *slog.Logger
}

func NewLive(key string, cache *Cache) *Client {
	return &Client{HTTP: &http.Client{Timeout: 10 * time.Second}, BaseURL: "https://api.openfigi.com/v3", APIKey: key, Cache: cache}
}
func successful(r Response) bool {
	return r.Error == "" && (r.Warning == "" || r.Warning == "No identifier found.")
}
func (c *Client) post(ctx context.Context, endpoint string, request, out any) error {
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	key := endpoint + ":" + string(body)
	if c.Cache != nil {
		if b, ok := c.Cache.Get(key); ok {
			return json.Unmarshal(b, out)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return ErrUpstream
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("X-OPENFIGI-APIKEY", c.APIKey)
	}
	started := time.Now()
	res, err := c.HTTP.Do(req)
	if err != nil {
		logUpstream(c.Log, slog.LevelError, "OPENFIGI", "request failed", "endpoint", endpoint, "elapsed", time.Since(started).Round(time.Millisecond).String(), "error", cleanErr(err))
		return ErrUpstream
	}
	defer res.Body.Close()
	if res.StatusCode == 429 {
		logUpstream(c.Log, slog.LevelWarn, "OPENFIGI", "rate limited (429)", "endpoint", endpoint, "retryAfter", res.Header.Get("Retry-After"))
		return ErrRateLimited
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		logUpstream(c.Log, slog.LevelError, "OPENFIGI", "error response", "endpoint", endpoint, "status", res.StatusCode, "body", snippet(body))
		return ErrUpstream
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil || json.Unmarshal(b, out) != nil {
		logUpstream(c.Log, slog.LevelError, "OPENFIGI", "unreadable response", "endpoint", endpoint, "body", snippet(b))
		return ErrUpstream
	}
	valid := true
	switch v := out.(type) {
	case *Response:
		valid = successful(*v)
	case *[]Response:
		for _, r := range *v {
			valid = valid && successful(r)
		}
	}
	if !valid {
		logUpstream(c.Log, slog.LevelWarn, "OPENFIGI", "response carried an error or warning", "endpoint", endpoint, "body", snippet(b))
		return ErrUpstream
	}
	if c.Cache != nil {
		c.Cache.Set(key, b)
	}
	return nil
}
func (c *Client) Search(ctx context.Context, r SearchRequest) (Response, error) {
	r.Query = Normalize(r.Query)
	r.ExchCode = Normalize(r.ExchCode)
	var out Response
	err := c.post(ctx, "/search", r, &out)
	return out, err
}
func (c *Client) Map(ctx context.Context, jobs []MappingJob) ([]Response, error) {
	jobs = append([]MappingJob(nil), jobs...)
	for i := range jobs {
		jobs[i].IDType = Normalize(jobs[i].IDType)
		jobs[i].IDValue = Normalize(jobs[i].IDValue)
		jobs[i].ExchCode = Normalize(jobs[i].ExchCode)
	}
	var out []Response
	err := c.post(ctx, "/mapping", jobs, &out)
	if err == nil && len(out) != len(jobs) {
		return nil, ErrUpstream
	}
	return out, err
}

//go:embed testdata/marketdata/*.json testdata/marketdata/eulerpool/*.json testdata/marketdata/yahoo/*.json
var fixtures embed.FS

type Mock struct{}

func (Mock) Search(ctx context.Context, r SearchRequest) (Response, error) {
	var rows []Listing
	b, _ := fixtures.ReadFile("testdata/marketdata/listings.json")
	if json.Unmarshal(b, &rows) != nil {
		return Response{}, ErrUpstream
	}
	out := Response{Data: []Listing{}}
	q := Normalize(r.Query)
	for _, l := range rows {
		if l.ExchCode == Normalize(r.ExchCode) && (strings.Contains(Normalize(l.Name), q) || strings.Contains(l.Ticker, q)) {
			out.Data = append(out.Data, l)
		}
	}
	return out, nil
}
func (Mock) Map(ctx context.Context, jobs []MappingJob) ([]Response, error) {
	var records map[string][]Response
	b, _ := fixtures.ReadFile("testdata/marketdata/mapping.json")
	if json.Unmarshal(b, &records) != nil {
		return nil, ErrUpstream
	}
	out := make([]Response, len(jobs))
	for i, j := range jobs {
		out[i].Warning = "No identifier found."
		for _, r := range records[Normalize(j.IDValue)] {
			if len(r.Data) > 0 && r.Data[0].ExchCode == Normalize(j.ExchCode) {
				out[i] = r
				break
			}
		}
	}
	return out, nil
}

type Result struct {
	AVSymbol             string `json:"avSymbol,omitempty"`
	Region               string `json:"region,omitempty"`
	MatchScore           string `json:"matchScore,omitempty"`
	Source               string `json:"source,omitempty"`
	FIGI                 string `json:"figi"`
	Ticker               string `json:"ticker"`
	ExchCode             string `json:"exchCode"`
	Name                 string `json:"name"`
	Type                 string `json:"type"`
	Currency             string `json:"currency"`
	ISIN                 string `json:"isin,omitempty"`
	WKN                  string `json:"wkn,omitempty"`
	ExistingInstrumentID int64  `json:"existingInstrumentId,omitempty"`
	InPortfolio          bool   `json:"inPortfolio"`
}
type Results struct {
	Provider string   `json:"provider,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Results  []Result `json:"results"`
	Reason   string   `json:"reason,omitempty"`
	Warning  string   `json:"warning,omitempty"`
}
type Service struct {
	Provider Provider
	Codes    []string
}

func (s Service) Lookup(ctx context.Context, q string) (Results, error) {
	q = Normalize(q)
	kind := Classify(q)
	responses := make([]Response, len(s.Codes))
	errs := make([]error, len(s.Codes))
	out := Results{Results: []Result{}}
	if kind != "KEYWORD" {
		jobs := make([]MappingJob, len(s.Codes))
		for i, code := range s.Codes {
			jobs[i] = MappingJob{kind, q, code}
		}
		var err error
		responses, err = s.Provider.Map(ctx, jobs)
		if err != nil {
			return out, err
		}
		if len(responses) != len(s.Codes) {
			return out, ErrUpstream
		}
	} else {
		var wg sync.WaitGroup
		for i, code := range s.Codes {
			wg.Add(1)
			go func(i int, code string) {
				defer wg.Done()
				responses[i], errs[i] = s.Provider.Search(ctx, SearchRequest{q, code})
			}(i, code)
		}
		wg.Wait()
	}
	successes := 0
	var failure error
	seen := map[string]bool{}
	for i, r := range responses {
		if errs[i] != nil {
			failure = errs[i]
			if errors.Is(errs[i], ErrRateLimited) {
				out.Warning = ErrRateLimited.Error()
			} else if out.Warning == "" {
				out.Warning = ErrUpstream.Error()
			}
			continue
		}
		successes++
		for _, l := range r.Data {
			kindType := ""
			if l.SecurityType == "ETP" {
				kindType = "ETF"
			} else if l.SecurityType2 == "Common Stock" {
				kindType = "STOCK"
			}
			ticker := Normalize(l.Ticker)
			if kindType == "" || ticker == "" || l.FIGI == "" || l.ExchCode != s.Codes[i] || seen[ticker] || len(out.Results) >= 10 {
				continue
			}
			seen[ticker] = true
			row := Result{FIGI: l.FIGI, Ticker: l.Ticker, ExchCode: l.ExchCode, Name: l.Name, Type: kindType, Currency: "EUR"}
			if kind == "ID_ISIN" {
				row.ISIN = q
			}
			if kind == "ID_WERTPAPIER" {
				row.WKN = q
			}
			out.Results = append(out.Results, row)
		}
	}
	if successes == 0 && failure != nil {
		if out.Warning == ErrRateLimited.Error() {
			return out, ErrRateLimited
		}
		return out, failure
	}
	if len(out.Results) == 0 {
		out.Reason = "NOT_FOUND"
	}
	return out, nil
}
