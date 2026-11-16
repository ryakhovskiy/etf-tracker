# Responsive theme smoke tests

From `frontend/`, install dependencies with `npm ci`, then install Chromium with
`npx playwright install chromium` and its system libraries with
`npx playwright install-deps chromium`. On Ubuntu, the missing runtime packages
may instead be installed by the machine owner with
`apt install libnspr4 libnss3 libasound2t64`.
If your Node installation needs the system CA
store, prefix the browser install with
`NODE_EXTRA_CA_CERTS=/etc/ssl/certs/ca-certificates.crt`.
This harness does not invoke sudo or install system packages. An optional local
alternative is to extract the platform's library packages and set
`LD_LIBRARY_PATH` in your shell; machine-specific paths do not belong in the
configuration or npm scripts.

Run `npm run test:e2e`. It starts its own Next dev server on `127.0.0.1:3100`.
Use `PLAYWRIGHT_PORT=3200 npm run test:e2e` to choose another port, or
`PLAYWRIGHT_BASE_URL=http://127.0.0.1:3000 npm run test:e2e` to test an existing
Next dev/start server. A production server must already have been built.

The suite covers login, the populated portfolios list, and all five portfolio
tabs in every explicit theme, at 375×812 and 1280×800. The dashboard, transactions,
and populated CSV review also run at the 360px acceptance boundary. Focused flows
exercise keyboard selection, reload persistence without re-seeding, live System
changes, and denied localStorage. API and auth calls are fulfilled
in-browser, including logged-out refresh attempts: neither a backend nor Google
login is needed. Unhandled API/auth calls fail instead of reaching a backend.

Each test sets `holdings.theme` before page scripts run, verifies the resolved
HTML theme and visible switcher, checks for horizontal page overflow, browser
exceptions and console errors (including hydration errors), and saves a full-page
screenshot. Only expected auth-resource 401 console errors are filtered. Find screenshots/traces under
`test-results/` and the HTML report under `playwright-report/`; both are ignored
by Git. Use `npm run test:e2e -- --project=mobile --grep='tech-green'` to narrow
a run. `npm run test:e2e -- --list` checks discovery without launching a server
or browser. Theme assertions intentionally require the phase-1 theme engine.

Additional checks measure populated picker geometry and reduced motion,
responsive month-card layout, and actual rendered dividend contrast (including
fill opacity and ancestor opacity). Token contrast alone does not establish AA
for translucent rendered content. The rendered-contrast helper measures solid
surfaces; gradients, images, pseudo-elements and overlapping siblings are outside
its measurement model. Overflow guards compare against the configured
device width, then scan both left and right bounds; content inside a fitting horizontal
scroll container is allowed. Oversized or clipped content reports selectors.

Portfolio-list summaries additionally cover fresh, partial, loading, existing-job,
automatic-refresh and cooldown states in all five themes at 360px, 375px and
1280px. Deferred route fixtures control summary/job completion; browser clocks
exercise cooldown expiry without real waits. These cases retain stored values
while updating, check partial-reason touch targets and explanation bounds, and
verify safe manual recovery without repeated automatic job starts. Run only this
matrix with `npm run test:e2e -- e2e/portfolio-summaries.spec.mjs`.

PWA acceptance specs in `e2e/pwa/` require a **production** server. Build with
`NEXT_PUBLIC_API_URL="" npm run build`, then in another terminal run
`npm run start -- --hostname 127.0.0.1 --port 3200`. Run the PWA matrix with:

```sh
PLAYWRIGHT_BASE_URL=http://127.0.0.1:3200 npm run test:e2e -- --project=pwa-360 --project=pwa-375 --project=pwa-1280
```

After the Docker stack is rebuilt, repeat with
`PLAYWRIGHT_BASE_URL=http://localhost:9443` and the same arguments. HTTPS or a
trustworthy loopback origin is required by Chromium. `PLAYWRIGHT_BASE_URL` opts
these projects into discovery; the default dev run never runs PWA tests.
Only the PWA projects allow service workers. The other projects continue to block
them, even when pointed at a production server. Omitting project arguments with
an external production URL runs both the existing suite and the PWA matrix.

The PWA cases check Chromium installability via CDP, served manifest/icon bytes
(including opaque Apple icons and the maskable glyph safe zone), worker headers
and first-load control, offline recovery on three routes in all five themes at
360/375/1280px, rendered text contrast and touch targets, theme-color, install UI,
and cache privacy through mocked OAuth, portfolio use and sign-out. Private
fixtures use context routing, assert they are actually hit after SW control, and
reject worker-owned private requests; a worker cannot conceal a broken mock by
serving a cached response. Additional probes exercise non-GET, cross-origin,
RSC/prefetch/data bypasses and a non-200 static response.

The update case starts an isolated ephemeral loopback proxy forwarding the real
production server, including Docker. It serves two build versions of the real
worker by changing its build-id literal and appending a diagnostic MessagePort
reply. Production install/activate/fetch/update UI logic is used unchanged. This
avoids Playwright's inability to reliably intercept worker update-script fetches,
never changes app source or the shared server, and proves that an actual waiting
worker preserves a transaction draft until Reload, then activates, removes old
caches and reloads exactly once. The test runner must be allowed to bind a local
port. Browser storage and the proxy are isolated per test. The iOS and standalone
cases simulate platform signals in Chromium; they do not claim native Safari or
OS home-screen installation coverage.

Further regression cases serve malformed precache and runtime responses through
that proxy
(201 PNG, HTML/JSON/RSC masquerading as a PNG, real redirects to auth/API or
another image). They inspect Cache Storage after the actual worker's install
finishes or is rejected, and after controlled runtime fetches. A positive case
also proves valid PNGs enter both caches. Runtime cases inject open/match/put
failures into the
actual controlling worker and prove a successful network asset stays usable.
A shared-context two-tab case activates an update from tab A, keeps tab B's draft
and document intact, then proves B's later explicit Reload still works once.
An iPhone WKWebView user agent without Safari/Version tokens must not offer the
Safari-only Share hint.

The asset test rejects empty/development worker identities. For a known build,
set `BUILD_ID=<unique-id>` when building and
`PLAYWRIGHT_EXPECTED_BUILD_ID=<unique-id>` when testing. The lifecycle proxy does
not prove that two *real* builds ship different worker bytes: during deployment
verification, compare the unmodified `/sw.js` bytes and embedded identities from
the separate local production and Docker builds as well.

Baseline before redesign, commit `0fbbbb715885653e90490d25848e1db85d62d067`:
`npm test` passed (2 library files + 9 component files, zero failures),
`npm run typecheck` passed, and `npm run build` passed (verified by the lead
outside the Codex sandbox). Codex's own build reported a sandbox subprocess
artifact, `Could not parse output from TypeScript's --showConfig`, which is not
a baseline product failure.
