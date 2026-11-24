package marketdata

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scriptedYahoo is a transport the test controls: every call is counted, reported
// on entered, optionally held by gate, and answered by handle.
type scriptedYahoo struct {
	mu       sync.Mutex
	inflight int
	peak     int
	urls     []string
	starts   []time.Time
	entered  chan string
	gate     func(ctx context.Context, rawURL string)
	handle   func(rawURL string) (int, []byte)
}

func newScriptedYahoo(handle func(rawURL string) (int, []byte)) *scriptedYahoo {
	return &scriptedYahoo{entered: make(chan string, 256), handle: handle}
}

func (s *scriptedYahoo) Get(ctx context.Context, rawURL string) (int, http.Header, []byte, error) {
	s.mu.Lock()
	s.inflight++
	s.peak = max(s.peak, s.inflight)
	s.urls = append(s.urls, rawURL)
	s.starts = append(s.starts, time.Now())
	s.mu.Unlock()
	s.entered <- rawURL
	if s.gate != nil {
		s.gate(ctx, rawURL)
	}
	s.mu.Lock()
	s.inflight--
	s.mu.Unlock()
	if err := ctx.Err(); err != nil { // like net/http: a cancelled request fails, it never succeeds
		return 0, nil, nil, err
	}
	status, body := s.handle(rawURL)
	return status, http.Header{}, body, nil
}

func (s *scriptedYahoo) total() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.urls) }

// watchCtx counts how often Done is requested. A caller of buildSession asks for
// it exactly when it starts to wait for the session, so a test can tell when all
// callers are parked instead of sleeping.
type watchCtx struct {
	context.Context
	calls atomic.Int32
}

func (w *watchCtx) Done() <-chan struct{} {
	w.calls.Add(1)
	return w.Context.Done()
}

func awaitWaiting(t *testing.T, n int32, ctxs ...*watchCtx) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var total int32
		for _, c := range ctxs {
			total += c.calls.Load()
		}
		if total >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("callers did not reach their wait for the session")
}

func okBody(string) (int, []byte) { return 200, []byte(`{}`) }

// awaitEntered waits for n requests to reach the transport.
func awaitEntered(t *testing.T, s *scriptedYahoo, n int) []string {
	t.Helper()
	var got []string
	for len(got) < n {
		select {
		case u := <-s.entered:
			got = append(got, u)
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d requests reached the transport", len(got), n)
		}
	}
	return got
}

// expectQuiet fails when another request reaches the transport within d.
func expectQuiet(t *testing.T, s *scriptedYahoo, d time.Duration) {
	t.Helper()
	select {
	case u := <-s.entered:
		t.Fatalf("unexpected request while the cap was reached: %s", u)
	case <-time.After(d):
	}
}

func parallelGateway(store EPStore, tr YahooTransport, concurrency int, minInterval time.Duration) *YahooGateway {
	g := NewYahooGateway(store, tr, 1000, minInterval, time.Hour)
	g.MaxConcurrency = concurrency
	g.CookieURL, g.QueryURL, g.FallbackURL = "http://y.test", "http://y.test", "http://y.test"
	return g
}

// chart sends one session-less chart request, as Latest does.
func chart(ctx context.Context, g *YahooGateway, user, symbol string) error {
	_, err := g.callPublic(ctx, user, "latest", g.QueryURL, "/v8/finance/chart/"+symbol, url.Values{"interval": {"1d"}, "range": {"5d"}})
	return err
}

func TestYahooCapsRequestsInFlight(t *testing.T) {
	for _, limit := range []int{1, 3} {
		t.Run("cap="+strconv.Itoa(limit), func(t *testing.T) {
			release := make(chan struct{})
			tr := newScriptedYahoo(okBody)
			tr.gate = func(context.Context, string) { <-release }
			g := parallelGateway(&memStore{state: map[string]string{}}, tr, limit, 0)
			const requests = 8
			errs := make(chan error, requests)
			for i := range requests {
				go func() { errs <- chart(context.Background(), g, "u", "S"+strconv.Itoa(i)) }()
			}
			awaitEntered(t, tr, limit)
			expectQuiet(t, tr, 150*time.Millisecond) // nothing beyond the cap starts while all are held
			close(release)
			for range requests {
				if err := <-errs; err != nil {
					t.Fatal(err)
				}
			}
			tr.mu.Lock()
			defer tr.mu.Unlock()
			if tr.peak != limit || len(tr.urls) != requests {
				t.Fatalf("peak in flight %d (cap %d), %d requests (want %d)", tr.peak, limit, len(tr.urls), requests)
			}
		})
	}
}

func startGaps(tr *scriptedYahoo) []time.Duration {
	tr.mu.Lock()
	starts := append([]time.Time(nil), tr.starts...)
	tr.mu.Unlock()
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })
	var gaps []time.Duration
	for i := 1; i < len(starts); i++ {
		gaps = append(gaps, starts[i].Sub(starts[i-1]))
	}
	return gaps
}

func TestYahooSpacesRequestStarts(t *testing.T) {
	const gap = 60 * time.Millisecond
	tr := newScriptedYahoo(okBody)
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 10, gap)
	var wg sync.WaitGroup
	for i := range 5 {
		wg.Go(func() { chart(context.Background(), g, "u", "S"+strconv.Itoa(i)) })
	}
	wg.Wait()
	gaps := startGaps(tr)
	if len(gaps) != 4 {
		t.Fatal(gaps)
	}
	for i, d := range gaps {
		if d < gap-20*time.Millisecond { // the test's own timestamps jitter a little
			t.Fatalf("start %d only %v after the previous one (MinInterval %v)", i+1, d, gap)
		}
	}
}

// stallBudgetStore holds the first usage read, like a slow ledger query, and
// reports when it is inside it.
type stallBudgetStore struct {
	*memStore
	held    atomic.Bool
	inside  chan struct{}
	release chan struct{}
}

func (s *stallBudgetStore) ProviderUsage(ctx context.Context, p string, since time.Time) (int, error) {
	if s.held.CompareAndSwap(false, true) { // only the first read is held; later ones pass
		close(s.inside)
		<-s.release
	}
	return s.memStore.ProviderUsage(ctx, p, since)
}

// A request delayed in its budget check must not start right behind the next one:
// the start slot is booked after the check, so real transport starts stay apart.
func TestYahooSlowBudgetCheckDoesNotCompressStarts(t *testing.T) {
	const gap = 150 * time.Millisecond
	tr := newScriptedYahoo(okBody)
	store := &stallBudgetStore{memStore: &memStore{state: map[string]string{}}, inside: make(chan struct{}), release: make(chan struct{})}
	g := parallelGateway(store, tr, 10, gap)
	var wg sync.WaitGroup
	wg.Go(func() { chart(context.Background(), g, "u", "A") })
	<-store.inside // A is inside its budget check
	wg.Go(func() { chart(context.Background(), g, "u", "B") })
	awaitEntered(t, tr, 1) // B starts first and is sent
	time.Sleep(gap + 40*time.Millisecond)
	close(store.release) // A leaves the budget check late
	wg.Wait()
	gaps := startGaps(tr)
	if len(gaps) != 1 || gaps[0] < gap-30*time.Millisecond {
		t.Fatalf("the late request started %v after the other (MinInterval %v)", gaps, gap)
	}
}

func rateLimitedOn(marker string) func(string) (int, []byte) {
	return func(u string) (int, []byte) {
		if strings.Contains(u, marker) {
			return 429, []byte("Edge: Too Many Requests")
		}
		return 200, []byte(`{}`)
	}
}

// After a 429, requests that have not started are refused and the ones in flight
// keep their own result.
func TestYahooRateLimitParallelKeepsInFlightResults(t *testing.T) {
	released := map[string]chan struct{}{"BAD": make(chan struct{}), "GOOD": make(chan struct{})}
	tr := newScriptedYahoo(rateLimitedOn("/BAD"))
	tr.gate = func(_ context.Context, u string) {
		for k, ch := range released {
			if strings.Contains(u, "/"+k) {
				<-ch
			}
		}
	}
	store := &memStore{state: map[string]string{}}
	g := parallelGateway(store, tr, 2, 0)
	symbols := []string{"BAD", "GOOD", "Q1", "Q2", "Q3"}
	results := make(map[string]chan error, len(symbols))
	for _, sym := range symbols {
		results[sym] = make(chan error, 1) // all channels exist before any goroutine reads the map
	}
	run := func(sym string) {
		ch := results[sym]
		go func() { ch <- chart(context.Background(), g, "u", sym) }()
	}
	run("BAD")
	run("GOOD")
	awaitEntered(t, tr, 2)
	for _, sym := range symbols[2:] { // wait behind the cap
		run(sym)
	}
	close(released["BAD"])
	if err := <-results["BAD"]; !errors.Is(err, ErrYahooLimit) {
		t.Fatal("429 request:", err)
	}
	// GOOD was already in flight when the 429 landed and still succeeds.
	close(released["GOOD"])
	if err := <-results["GOOD"]; err != nil {
		t.Fatal("in-flight request lost its result:", err)
	}
	for _, sym := range symbols[2:] {
		if err := <-results[sym]; !errors.Is(err, ErrYahooLimit) {
			t.Fatal(sym, err)
		}
	}
	if tr.total() != 2 || len(store.rows) != 2 {
		t.Fatalf("sent %d, recorded %d: nothing may start after the 429", tr.total(), len(store.rows))
	}
}

// With one request at a time a 429 leaves exactly one request sent, as before.
func TestYahooRateLimitSequentialSendsOne(t *testing.T) {
	tr := newScriptedYahoo(rateLimitedOn("/S"))
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 1, 300*time.Millisecond)
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Go(func() { errs[i] = chart(context.Background(), g, "u", "S"+strconv.Itoa(i)) })
	}
	wg.Wait()
	for _, err := range errs {
		if !errors.Is(err, ErrYahooLimit) {
			t.Fatal(err)
		}
	}
	if tr.total() != 1 {
		t.Fatalf("%d requests sent", tr.total())
	}
}

// stallLedgerStore holds the ledger write of one user, like a slow database.
type stallLedgerStore struct {
	*memStore
	user    string
	stalled chan struct{} // closed when the write is held
	release chan struct{}
	failed  bool
}

func (s *stallLedgerStore) RecordProviderRequest(ctx context.Context, p, user, f string, at time.Time, counted bool) error {
	if user == s.user {
		close(s.stalled)
		<-s.release
		if s.failed {
			return errors.New("ledger down")
		}
	}
	return s.memStore.RecordProviderRequest(ctx, p, user, f, at, counted)
}

// The cool-down starts when the 429 arrives, not when its ledger write finishes,
// and survives a failing ledger write.
func TestYahooCooldownDoesNotWaitForTheLedger(t *testing.T) {
	for _, ledgerFails := range []bool{false, true} {
		t.Run("ledgerFails="+strconv.FormatBool(ledgerFails), func(t *testing.T) {
			badRelease, goodRelease := make(chan struct{}), make(chan struct{})
			tr := newScriptedYahoo(rateLimitedOn("/BAD"))
			tr.gate = func(_ context.Context, u string) {
				switch {
				case strings.Contains(u, "/BAD"):
					<-badRelease // BAD answers only once GOOD is in flight
				case strings.Contains(u, "/GOOD"):
					<-goodRelease
				}
			}
			store := &stallLedgerStore{memStore: &memStore{state: map[string]string{}}, user: "bad", stalled: make(chan struct{}), release: make(chan struct{}), failed: ledgerFails}
			g := parallelGateway(store, tr, 2, 0)
			bad, good, queued := make(chan error, 1), make(chan error, 1), make(chan error, 1)
			go func() { bad <- chart(context.Background(), g, "bad", "BAD") }()
			go func() { good <- chart(context.Background(), g, "good", "GOOD") }()
			awaitEntered(t, tr, 2)
			close(badRelease) // the 429 is received...
			<-store.stalled   // ...and its ledger write is held
			go func() { queued <- chart(context.Background(), g, "queued", "LATE") }()
			close(goodRelease) // GOOD frees its slot
			if err := <-good; err != nil {
				t.Fatal(err)
			}
			if err := <-queued; !errors.Is(err, ErrYahooLimit) {
				t.Fatalf("a request started after the 429 while its ledger write was pending: %v", err)
			}
			close(store.release)
			if err := <-bad; ledgerFails && err == nil || !ledgerFails && !errors.Is(err, ErrYahooLimit) {
				t.Fatal(err)
			}
			if tr.total() != 2 {
				t.Fatalf("%d requests sent", tr.total())
			}
			if b, _ := g.Budget(context.Background()); b.BlockedUntil == nil || b.Remaining != 0 {
				t.Fatalf("cool-down not visible in the budget: %+v", b)
			}
		})
	}
}

// crumbServer emulates Yahoo's session: the cookie request, a crumb per session
// and a quote endpoint that accepts only the crumb issued last.
type crumbServer struct {
	mu      sync.Mutex
	cookies int
	crumbs  int
	valid   string
	quotes  int
	// crumbStatus, when set, is the answer of every crumb request.
	crumbStatus int
}

func (c *crumbServer) handle(rawURL string) (int, []byte) {
	u, _ := url.Parse(rawURL)
	c.mu.Lock()
	defer c.mu.Unlock()
	switch u.Path {
	case "/":
		c.cookies++
		return 404, []byte("not found")
	case "/v1/test/getcrumb":
		c.crumbs++
		if c.crumbStatus != 0 {
			return c.crumbStatus, []byte(`{"finance":{"error":{"description":"Invalid Cookie"}}}`)
		}
		c.valid = "crumb" + strconv.Itoa(c.crumbs) + "xyz"
		return 200, []byte(c.valid)
	case "/v7/finance/quote":
		c.quotes++
		if u.Query().Get("crumb") != c.valid || c.valid == "" {
			return 401, []byte(`{"finance":{"error":{"code":"Unauthorized"}}}`)
		}
		return 200, []byte(`{}`)
	}
	return 200, []byte(`{}`)
}

func (c *crumbServer) counts() (cookies, crumbs int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cookies, c.crumbs
}

func quote(ctx context.Context, g *YahooGateway) error {
	_, err := g.call(ctx, "u", "quote", g.QueryURL, "/v7/finance/quote", url.Values{"symbols": {"SAP.DE"}})
	return err
}

func runParallel(n int, fn func() error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { errs[i] = fn() })
	}
	wg.Wait()
	return errs
}

func TestYahooSessionBuiltOnceUnderParallelCalls(t *testing.T) {
	srv := &crumbServer{}
	tr := newScriptedYahoo(srv.handle)
	// A slow cookie request makes the callers pile up behind the first one. The
	// count holds for any interleaving: a caller that arrives later reuses the crumb.
	tr.gate = func(_ context.Context, u string) {
		if strings.HasSuffix(u, "/") {
			time.Sleep(50 * time.Millisecond)
		}
	}
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 10, 0)
	for _, err := range runParallel(8, func() error { return quote(context.Background(), g) }) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if cookies, crumbs := srv.counts(); cookies != 1 || crumbs != 1 || srv.quotes != 8 {
		t.Fatalf("cookies %d, crumbs %d, quotes %d: want one session for 8 calls", cookies, crumbs, srv.quotes)
	}
}

// Every call is in flight with the old crumb (a barrier holds them) when Yahoo
// answers 401 to all of them: the session is rebuilt once, not once per call.
func TestYahooRejectedCrumbRebuiltOnceUnderParallelCalls(t *testing.T) {
	const calls = 8
	srv := &crumbServer{}
	tr := newScriptedYahoo(srv.handle)
	var oldCrumb atomic.Value
	var arrived atomic.Int32
	open := make(chan struct{})
	tr.gate = func(_ context.Context, u string) {
		if strings.Contains(u, "/v7/finance/quote") && strings.Contains(u, "crumb="+oldCrumb.Load().(string)) {
			if arrived.Add(1) == calls {
				close(open)
			}
			<-open
		}
	}
	oldCrumb.Store("none") // no call matches while the first session is built
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 16, 0)
	if err := quote(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	c, _ := g.session()
	oldCrumb.Store(c)
	// Yahoo invalidates the crumb: the quotes below all meet a 401.
	srv.mu.Lock()
	srv.valid = "rotated-elsewhere"
	srv.mu.Unlock()
	for _, err := range runParallel(calls, func() error { return quote(context.Background(), g) }) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if cookies, crumbs := srv.counts(); cookies != 2 || crumbs != 2 {
		t.Fatalf("cookies %d, crumbs %d: the rejected session must be rebuilt once, not once per call", cookies, crumbs)
	}
}

// When the session cannot be built, the callers waiting on that attempt share its
// failure instead of each repeating cookie and crumb requests.
func TestYahooFailedSessionAttemptIsSharedByWaiters(t *testing.T) {
	srv := &crumbServer{crumbStatus: 401}
	tr := newScriptedYahoo(srv.handle)
	cookieHeld := make(chan struct{})
	tr.gate = func(_ context.Context, u string) {
		if strings.HasSuffix(u, "/") {
			<-cookieHeld
		}
	}
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 10, 0)
	const calls = 5
	errs := make(chan error, calls)
	watchers := make([]*watchCtx, calls)
	for i := range watchers {
		watchers[i] = &watchCtx{Context: context.Background()}
	}
	for _, w := range watchers {
		go func() { errs <- quote(w, g) }()
	}
	awaitEntered(t, tr, 1)
	awaitWaiting(t, calls, watchers...) // all five are parked on the one attempt
	close(cookieHeld)
	for range calls {
		if err := <-errs; !errors.Is(err, ErrYahooAuth) {
			t.Fatal(err)
		}
	}
	// One shared attempt: one cookie request and one crumb request per crumb host.
	if cookies, crumbs := srv.counts(); cookies != 1 || crumbs != 2 {
		t.Fatalf("cookies %d, crumbs %d: waiters repeated a failed session build", cookies, crumbs)
	}
	// The next independent request tries again.
	if err := quote(context.Background(), g); !errors.Is(err, ErrYahooAuth) {
		t.Fatal(err)
	}
	if cookies, crumbs := srv.counts(); cookies != 2 || crumbs != 4 {
		t.Fatalf("after the failure a new request must retry: cookies %d, crumbs %d", cookies, crumbs)
	}
}

// A caller waiting for a session build stops waiting when its context ends.
func TestYahooSessionWaiterHonoursContext(t *testing.T) {
	srv := &crumbServer{}
	tr := newScriptedYahoo(srv.handle)
	cookieHeld := make(chan struct{})
	tr.gate = func(_ context.Context, u string) {
		if strings.HasSuffix(u, "/") {
			<-cookieHeld
		}
	}
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 10, 0)
	leader := make(chan error, 1)
	go func() { leader <- quote(context.Background(), g) }()
	awaitEntered(t, tr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	watch := &watchCtx{Context: ctx}
	waiter := make(chan error, 1)
	go func() { waiter <- quote(watch, g) }()
	awaitWaiting(t, 1, watch) // the waiter is parked on the leader's attempt
	cancel()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the waiter kept waiting for the session build after its context ended")
	}
	close(cookieHeld)
	if err := <-leader; err != nil {
		t.Fatal(err)
	}
}

// A caller that started the session build and then gives up does not fail the
// others waiting for it: the build runs on its own context.
func TestYahooCancelledBuilderDoesNotFailWaiters(t *testing.T) {
	srv := &crumbServer{}
	tr := newScriptedYahoo(srv.handle)
	cookieHeld := make(chan struct{})
	tr.gate = func(_ context.Context, u string) {
		if strings.HasSuffix(u, "/") {
			<-cookieHeld
		}
	}
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 10, 0)
	builderCtx, cancel := context.WithCancel(context.Background())
	builder, waiter := make(chan error, 1), make(chan error, 1)
	watchedBuilder := &watchCtx{Context: builderCtx}
	watchedWaiter := &watchCtx{Context: context.Background()}
	go func() { builder <- quote(watchedBuilder, g) }()
	awaitEntered(t, tr, 1) // the build's cookie request is in flight
	go func() { waiter <- quote(watchedWaiter, g) }()
	awaitWaiting(t, 1, watchedWaiter) // the waiter is parked on the build
	cancel()
	if err := <-builder; !errors.Is(err, context.Canceled) {
		t.Fatal("builder:", err)
	}
	close(cookieHeld)
	select {
	case err := <-waiter:
		if err != nil {
			t.Fatalf("the waiter was failed by the builder's cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiter never finished")
	}
	if cookies, crumbs := srv.counts(); cookies != 1 || crumbs != 1 {
		t.Fatalf("cookies %d, crumbs %d: one shared build expected", cookies, crumbs)
	}
}

// Close cancels a session build in progress and waits for it, so the store is not
// used afterwards, and refuses new builds.
func TestYahooCloseStopsAndWaitsForSessionBuild(t *testing.T) {
	srv := &crumbServer{}
	tr := newScriptedYahoo(srv.handle)
	var finished atomic.Bool
	tr.gate = func(ctx context.Context, u string) {
		if strings.HasSuffix(u, "/") {
			<-ctx.Done() // only Close can end the build
			time.Sleep(50 * time.Millisecond)
			finished.Store(true)
		}
	}
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 10, 0)
	caller := make(chan error, 1)
	go func() { caller <- quote(context.Background(), g) }()
	awaitEntered(t, tr, 1)
	closed := make(chan struct{})
	go func() { g.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not stop the session build")
	}
	if !finished.Load() {
		t.Fatal("Close returned before the build had finished")
	}
	if err := <-caller; err == nil {
		t.Fatal("the caller of a cancelled build must fail")
	}
	if err := quote(context.Background(), g); err == nil {
		t.Fatal("a closed gateway started a new session build")
	}
}

// A request rejected with an old crumb must not discard the session that another
// request has built since.
func TestYahooDropSessionIgnoresNewerSession(t *testing.T) {
	g := parallelGateway(&memStore{state: map[string]string{}}, newScriptedYahoo(okBody), 1, 0)
	g.crumb, g.gen = "new-crumb", 2
	g.dropSession(1)
	if c, gen := g.session(); c != "new-crumb" || gen != 2 {
		t.Fatal(c, gen)
	}
	g.dropSession(2)
	if c, _ := g.session(); c != "" {
		t.Fatal("the current session was not dropped")
	}
}

// A session-less request that was sent while a session was being built, and is
// rejected only after the build published its crumb, must not discard that crumb.
func TestYahooLateRejectionOfSessionlessRequestKeepsNewSession(t *testing.T) {
	srv := &crumbServer{}
	g := parallelGateway(&memStore{state: map[string]string{}}, newScriptedYahoo(srv.handle), 1, 0)
	none, gen := g.session() // snapshot taken before the build: no crumb
	if none != "" {
		t.Fatal(none)
	}
	crumb, built, err := g.buildSession(context.Background(), "u")
	if err != nil || crumb == "" || built == gen {
		t.Fatalf("crumb %q gen %d (before %d) %v", crumb, built, gen, err)
	}
	g.dropSession(gen) // the late 401 of the request that held the old snapshot
	if c, _ := g.session(); c != crumb {
		t.Fatalf("the late rejection discarded the new session: %q", c)
	}
	if cookies, crumbs := srv.counts(); cookies != 1 || crumbs != 1 {
		t.Fatal(cookies, crumbs)
	}
}

func TestYahooAcquireHonoursContext(t *testing.T) {
	release := make(chan struct{})
	tr := newScriptedYahoo(okBody)
	tr.gate = func(context.Context, string) { <-release }
	g := parallelGateway(&memStore{state: map[string]string{}}, tr, 1, 0)
	first := make(chan error, 1)
	go func() { first <- chart(context.Background(), g, "u", "A") }()
	awaitEntered(t, tr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan error, 1)
	go func() { waiting <- chart(ctx, g, "u", "B") }()
	cancel()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if tr.total() != 1 {
		t.Fatal(tr.total())
	}
}
