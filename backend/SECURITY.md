# Backend security checklist

## Implemented

- OAuth `state` is random, bound to HttpOnly SameSite cookies, and cleared after the callback.
- OAuth authorization-code exchange uses PKCE (`S256`).
- Google profile data is accepted only when the identity has a non-empty OpenID `sub`, email, and `email_verified=true`.
- Access tokens are short-lived JWTs tied to server-side sessions; refresh tokens are random, hashed before storage, HttpOnly/SameSite cookies with server-side expiry and rotation. Sessions have an absolute 30-day lifetime.
- Logout and reuse of rotated refresh tokens revoke the session family, including access tokens. Other sessions for the user remain active. Revocation failures return 503 and preserve cookies for retry.
- Production configuration requires HTTPS URLs, a secure cookie, Google credentials, and an explicitly configured random JWT secret of at least 32 characters; development/example secrets are rejected.
- External Google requests share a ten-second context deadline, propagate request cancellation, and limit decoded response bodies.
- SQLite files are restricted to 0600 before opening the database so newly created WAL/SHM files inherit private permissions; existing sidecars are repaired. Use a trusted parent directory.
- Schema changes run through embedded, versioned Goose SQL migrations before serving requests. Legacy sessions are invalidated during migration while users are retained.
- The module requires Go 1.27.3 or newer, including the certificate-verification security fix.
- The API sets request timeouts, a one-megabyte header limit, CORS allowlisting, and graceful shutdown.
- API responses include browser security headers and `Cache-Control: no-store`.

## Before production

- Run the API behind TLS and set `APP_ENV=production` and `SECURE_SESSION_COOKIE=true`.
- Protect the SQLite database file with filesystem permissions, backups, encryption at rest where required, and a single-writer deployment strategy. Move to a shared database if multiple API instances are deployed.
- Keep Google redirect URIs exact and registered only for the real HTTPS origin.
- Store `GOOGLE_CLIENT_SECRET` and `JWT_SECRET` in a secret manager; do not put them in images, logs, or source control.
- Add rate limiting and monitoring for OAuth start/callback, login failures, logout, and unusual session activity.
- Use dependency and container scanning, `govulncheck`, `go vet`, race tests, and regular patching.
- Add authorization checks for every future resource endpoint; authentication alone is not authorization.
- Validate request bodies with size limits and strict schemas before adding write endpoints.
- Set the frontend's own CSP. The API CSP protects API responses; it does not configure the Next.js document.

## Header verification

Check the deployed HTTPS endpoint, not only localhost:

```sh
curl -sS -D - -o /dev/null https://api.example.com/health
```

Verify every API response has:

- `Strict-Transport-Security` in production, only over HTTPS.
- `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'; base-uri 'none'`.
- `X-Content-Type-Options: nosniff`.
- `X-Frame-Options: DENY`.
- `Referrer-Policy: no-referrer`.
- `Permissions-Policy` disabling unused browser capabilities.
- `X-Permitted-Cross-Domain-Policies: none`.
- `Cache-Control: no-store, max-age=0` for authentication and user data.

Also verify:

- `Access-Control-Allow-Origin` is the exact frontend origin, never `*` when credentials are enabled.
- `Access-Control-Allow-Credentials: true` is present only for the allowlisted frontend.
- Access and refresh `Set-Cookie` headers include `HttpOnly`, `SameSite=Strict`, `Secure` in production, scoped `Path`, and bounded `Max-Age`; refresh tokens are never returned in JSON.
- OAuth callback rejects missing or mismatched `state`, missing PKCE verifier, missing authorization code, and unverified Google email.
- Invalid CORS preflight requests return `403`.
