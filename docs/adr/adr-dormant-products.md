# ADR: groups and communities are dormant until a client exists

**Date:** 12 September 2026. **Status:** accepted (founder: "go ahead with 3, take best decision").

## Context

`group-service` (about sixty `/v1/groups` routes: posts, engagement, comments, join requests, rules, wiki, channels, events, moderation) and `community-service` (`/v1/communities`: spaces, join requests, modlog, events, bans) are complete server products. An audit on 12 September 2026 found no client for either on Android, iOS or the web. Android "groups" are chat group conversations and "communities" are broadcast channels in `channel-service`, both different products.

## Decision

- The code and the deployments stay. Deleting two built products is not reversible in practice, and `dating-service` reads `COMMUNITY_SERVICE_URL` internally.
- The **public** prefixes `/v1/groups` and `/v1/communities` are closed at the gateway by default (`serveDormantProductGate`, 404 so the edge does not confirm the product exists). `DORMANT_PRODUCTS_ENABLED=true` opens them without a code change.
- Internal service-to-service calls are unaffected: they do not pass through the gateway.

## Consequences

- No client can reach unreviewed surface by accident; the cost of keeping the services is compute only.
- The day a client is built, the flag flips per environment and the routes are back, with the route table untouched.
- If the founder later decides these products are not on the roadmap, deletion is a separate decision: remove the deploy manifests and compose entries first, then the code.

## Amendment, 13 September 2026: Mopedu (`/v1/rider`)

**Context.** A security audit found `rider-service` (about eighty routes: rides, partner KYC and Aadhaar, subscription payment proof, SOS, admin) trusting `X-User-Id` and `X-Scopes` with no internal-key check, and publishing port 8116 on the compose host. Android and the web have no client. iOS has one screen, `RideBookingView`, reachable from the services hub. It posts `{pickup, drop}` as strings to `POST /v1/rider/rides`, but the server binds them as objects, so every call is a 400. The app discards the result (`try?`) and simulates a driver match. Nothing that works depends on the route.

**Decision.**

- `/v1/rider` is closed at the gateway by the same 404 gate, on its **own** flag, `RIDER_PUBLIC_ENABLED` (default `false`). It is deliberately not added to `DORMANT_PRODUCTS_ENABLED`: that flag is all-or-nothing, and `docs/runbooks/communities-invite-only-pilot.md` already records that sharing it couples launches. Launching Mopedu must not open groups and communities, or the reverse. The gate is now a table with one flag per product.
- On the service side, independently of the gate, every `/v1/rider` route requires `X-Internal-Service-Key` when a key is configured. The process refuses to start in production without a key, or with `DIGILOCKER_MODE` set to the mock (or unset, or unrecognised). The compose port is no longer published.

**Consequences.** Closing the gate changes nothing visible on iOS. Before opening it, the iOS request body must be fixed and prod `rider-service` needs `DIGILOCKER_MODE=http` with partner credentials, or it will not start.

## Amendment, 15 September 2026: Dating (`/v1/dating`) and its pilot allowlist

**Context.** `/v1/dating` was routed at the gateway and was not in this gate. The gateway checks admin scope only on paths containing `/internal/`, and it stamps `X-Internal-Service-Key` on every proxied request, so dating-service's internal-key check passed for anyone. Every dating route, including `/v1/dating/admin/*`, was callable anonymously. The gateway also forwarded a client's `X-Admin-Id`, which dating-service records as the audit actor on admin actions, so that actor could be forged. Dating is being rebuilt under an approved plan, and the team needs to use it on real devices before anyone else can.

**Decision.**

- `/v1/dating` joins the gate on its **own** flag, `DATING_PUBLIC_ENABLED` (default `false`). It is not part of `DORMANT_PRODUCTS_ENABLED` or `RIDER_PUBLIC_ENABLED`, for the reason in the Mopedu amendment.
- While the flag is false, a closed product can carry a **pilot allowlist**. Dating's is `DATING_PILOT_USER_IDS`: comma-separated UUIDs, parsed once at boot. The gate lets a request through only when the user id the gateway got from a **verified token** is on the list. It reads that id from the request context, which only `jwtExtractMiddleware` sets after verification. It never reads `X-User-Id` or any other client-controllable header. Anonymous requests always get 404. A token that fails verification gets the edge's existing 401 and never reaches the gate.
- The allowlist fails closed. Unset or empty means nobody. An invalid entry refuses boot in production (the same `APP_ENV`/`ENVIRONMENT`/`ENV` detection the token policy uses). Outside production it is dropped with a WARN, and the valid entries still apply.
- Every dating route is covered, `/v1/dating/admin/*` and the Razorpay webhook `/v1/dating/premium/webhook` included. The webhook is not special-cased because a later lane deletes it.
- Separately from the gate, the gateway strips a client's `X-Admin-Id` and `X-Internal-Key` along with the other identity headers. It sets neither one, so any client copy is a forgery.
- Prod and staging set `DATING_PUBLIC_ENABLED: "false"` with an empty `DATING_PILOT_USER_IDS`. Dev compose defaults the allowlist to the two dev test accounts, `call_a` and `call_b`.

**Consequences.** Outside the pilot, dating looks like a route that does not exist. Pilot users still get the service's full surface, admin routes included, because the gateway does not yet enforce admin scope on `/v1/dating/admin`. The allowlist must stay limited to trusted team accounts until dating-service enforces admin authorisation itself. Opening dating to everyone means flipping `DATING_PUBLIC_ENABLED` per environment, and that needs its own review once the rebuild lands.
