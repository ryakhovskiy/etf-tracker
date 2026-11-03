# Holdings

A small Go + Next.js app with Google login/signup, SQLite-backed users and refresh tokens, JWT access tokens, and an empty authenticated workspace.

## Setup

Requires Go 1.27.3 or newer and Node.js 22.18 or newer (including native TypeScript support for the frontend tests).

1. Create OAuth credentials in Google Cloud Console. Add `http://localhost:8080/auth/google/callback` as an authorized redirect URI.
2. Copy `.env.example` to `.env` and fill in the Google credentials.
3. Start the API:

   ```sh
   cd backend && set -a && source ../.env && set +a && go run ./cmd/api
   ```

4. Start the web app in a second terminal:

   ```sh
   cd frontend && npm install && npm run dev
   ```

Open http://localhost:3000. Google OAuth handles both new-user signup and returning-user login. Users and hashed refresh tokens are persisted in `DATABASE_PATH` (default: `data/holdings.db`); access and refresh tokens are issued only as HttpOnly cookies.

## Database migrations

The API applies pending [Goose](https://pressly.github.io/goose/documentation/provider/) migrations before accepting requests. SQL migrations live in [backend/internal/database/migrations](./backend/internal/database/migrations) and are embedded into the binary. Goose records applied versions in `goose_db_version`; restarting the API does not reapply them. Add a new numbered SQL file with `-- +goose Up` and `-- +goose Down` sections for each schema change, then rebuild the API. Do not edit already applied migrations.

The initial migration adopts the previous schema without deleting users. The session migration invalidates existing sessions because legacy refresh tokens do not record their family history; users must sign in again once after upgrading. Back up the database before upgrading. Run only one API instance against a database file and keep its directory writable only by the service owner.

## Sessions and production

Set `APP_ENV=production`, `SECURE_SESSION_COOKIE=true`, HTTPS frontend/callback URLs, Google credentials, and an explicit random `JWT_SECRET` of at least 32 characters. Generate a secret with `openssl rand -hex 32`; the development fallback and example placeholder are rejected in production. Frontend and API must share the same site (for example `app.example.com` and `api.example.com`) because session cookies use `SameSite=Strict`.

Access tokens expire after 15 minutes and are checked against server-side sessions on every request. Sessions expire at most 30 days after login. Logout and refresh-token replay revoke the whole session immediately, including its access tokens. Storage failures return 503; failed logout preserves cookies so the user can retry. The frontend serializes refresh and logout across tabs with Web Locks where available, with a same-tab fallback queue.

Run backend checks from [backend](./backend): `go test ./...`, `go vet ./...`, and `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`. Run `CGO_ENABLED=1 go test -race ./...` where a C compiler is available.

Run frontend checks from [frontend](./frontend): `npm ci`, `npm test`, `npm run typecheck`, `npm run build`, and `npm audit`. The frontend uses patched Next.js 16 and React 19; the obsolete `next lint` command was replaced with a type-check script. See the [Next.js upgrade guide](https://nextjs.org/docs/app/guides/upgrading/version-16) for framework changes.
