# Cloud agent brief — CONSOLIDATION of the three audits

Run this only after `FEED_AUDIT.md`, `REELS_AUDIT.md` and `POSTTUBE_AUDIT.md` exist (on branches `audit/feed-production-readiness`, `audit/reels-production-readiness`, `audit/posttube-production-readiness` — merge or read all three). Read `docs/production-readiness/agents/00_COMMON_RULES.md`. No application code changes.

Write `docs/production-readiness/FEED_REELS_POSTTUBE_AUDIT.md` on branch `audit/production-readiness-consolidated` with:
1. **Executive summary** — overall readiness of Feed, Reels, PostTube (High/Medium/Low + %), top 3 blockers, immediate next steps.
2. **Status by module** — merged feature matrices (link to the module files for detail).
3. **Verified issues & architectural risks** — de-duplicated across modules (shared services: feed-service, media-service, post-service, analytics-service, notification-service, gateway). Where two module audits disagree, re-check the code and state which is right with evidence.
4. **Missing functionality** — mapped to TikTok (Reels) and YouTube (PostTube).
5. **One prioritised backlog P0–P3** across modules, de-duplicated, each item with Module | Issue | Evidence | Impact | Solution | Dependencies | Acceptance criteria | Required tests | Complexity.
6. **Recommended architecture changes** — only with evidence (CDN, caching, DB, queues).
7. **Database & API gaps**.
8. **Test coverage & outstanding validation** — what was actually run (from the three AUDIT_LOG files) vs not run; load-test plan with targets (Reels 500K concurrent / < 500 ms feed / 99.9%; PostTube 100K per video / < 100 ms recommendations / 99.95%).
9. **Go/no-go checklist** per module.
10. **Implementation roadmap** — phased by dependency: critical defects → complete user journeys → media delivery reliability → engagement correctness → scalability → operational readiness; each phase with objectives, work items, files, DB/API changes, tests, perf/security validation, definition of done, rollback.
11. **Risks, dependencies, open questions** for the founder.
12. **Work packages for implementation agents** — split the roadmap into independent packages (one service tree each, contracts first), each with a ready-to-paste prompt for a future implementation agent.

Final chat summary: overall readiness, top 5 issues, important missing features (TikTok/YouTube references), implementation order, immediate next action, what is production-ready vs unverified.
