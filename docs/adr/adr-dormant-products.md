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
