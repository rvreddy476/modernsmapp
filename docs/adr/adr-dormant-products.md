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
