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

Baseline before redesign, commit `0fbbbb715885653e90490d25848e1db85d62d067`:
`npm test` passed (2 library files + 9 component files, zero failures),
`npm run typecheck` passed, and `npm run build` passed (verified by the lead
outside the Codex sandbox). Codex's own build reported a sandbox subprocess
artifact, `Could not parse output from TypeScript's --showConfig`, which is not
a baseline product failure.
