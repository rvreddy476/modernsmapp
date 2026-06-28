# Passkeys / WebAuthn (auth-service)

Phishing-resistant credentials: passwordless login + a strong second factor,
alongside the existing password + TOTP 2FA.

## Status
- **Foundation (in the default build, verified):** `auth.webauthn_credentials`
  table + store CRUD (`internal/store/webauthn.go`) + the `IssueSessionForUser`
  service method (passwordless session minting) + a no-op route registrar.
- **Ceremony (behind `//go:build webauthn`):** the registration/authentication
  handlers (`internal/http/webauthn_routes_on.go`) using `go-webauthn`. Excluded
  from the default build so it needs no new dependency until you enable it.

## Enable
```bash
cd identity-platform/services/auth-service
go get github.com/go-webauthn/webauthn@latest && go mod tidy
go build -tags webauthn ./...            # verify the ceremony compiles
# run / build the image with the tag:
go run -tags webauthn ./cmd/server
```
To make it the default, drop the build tags from `webauthn_routes_on.go` /
`webauthn_routes_off.go` (keep exactly one `RegisterWebAuthnRoutes`).

## Config (env)
| var | default | meaning |
|---|---|---|
| `WEBAUTHN_RP_ID` | `localhost` | registrable domain, e.g. `cleestudio.com` |
| `WEBAUTHN_RP_NAME` | `atPost` | display name shown by the authenticator |
| `WEBAUTHN_RP_ORIGINS` | `http://localhost:3000` | comma-sep allowed origins, e.g. `https://app.cleestudio.com` |

## Endpoints (`/v1/auth/passkeys`)
| method | path | auth | purpose |
|---|---|---|---|
| POST | `/register/begin` | session | creation options |
| POST | `/register/finish?name=` | session | store the new passkey |
| GET | `` | session | list the caller's passkeys |
| DELETE | `/:id` | session | remove a passkey |
| POST | `/login/begin` | public | discoverable assertion options (+ `session` token) |
| POST | `/login/finish?session=` | public | verify → mint a session (passwordless) |

Challenges are single-use, stored in Redis (5-min TTL). Login is **discoverable**
(usernameless): the authenticator's userHandle is the user id, resolved server-side.

## Notes / caveats
- Targets go-webauthn v0.10+; if its API shifted, fixes are localized to
  `webauthn_routes_on.go` (the only file importing it).
- The sign-counter is advanced on each assertion (clone/replay defense); a
  counter regression sets `clone_warning`.
- Not yet live-verified end-to-end (needs the library fetched + a browser/
  authenticator).
