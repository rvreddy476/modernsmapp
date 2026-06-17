# Resume after Ubuntu reinstall — session continuity notes

> **Running the local Docker stack?**  See [`docs/docker-bringup.md`](docker-bringup.md) — one script handles everything (IPv4 fix, image pulls, sequential builds, health checks).

You're formatting Windows → installing Ubuntu. This doc captures
**where the work stopped** so the next session (fresh Claude Code
on Ubuntu) can pick up without source-diving.

Pushed + safe: every commit listed below is on GitHub. Re-clone
the two repos on Ubuntu and you have everything.

## Repos to re-clone

```bash
mkdir -p ~/workspace && cd ~/workspace
git clone -b feat/vchat-rebrand-realtime-ui \
  https://github.com/rvreddy476/modernsmapp.git atpost
git clone -b feat/vchat-rebrand-realtime-ui \
  https://github.com/rvreddy476/postbook-ui.git
cd atpost
git remote rename origin modernsmapp   # match Windows-side convention
```

Active branch on both: **`feat/vchat-rebrand-realtime-ui`**.

## What was done in the last sprint (chronological, most recent last)

The branch `feat/vchat-rebrand-realtime-ui` carries ~60 commits
across atpost + 22 commits across postbook-ui. The high-level
groupings, ordered by when we worked on them:

1. **AWS-prep code-only fixes** — C4/C5/C6/C7/H1-H7 from the
   readiness audit. Schema-drift migration runner, kid-aware JWT
   verify, per-upstream HTTP clients with circuit breaker, auth
   rate limits, commerce idempotency + race recovery, profile-view
   privacy gate, CHECK constraints on status enums.
2. **Phase-0 IaC scaffold** at `infra/terraform/` — VPC + ECR +
   IAM (GitHub OIDC) + Route 53 + ACM. Two-apply bootstrap dance.
3. **Phase-2 AWS data plane** (six modules) — EKS, Aurora PG
   Multi-AZ, MSK Serverless, ElastiCache Valkey, OpenSearch, S3 +
   CloudFront.
4. **Phase-2 cluster tooling** — External Secrets Operator, AWS
   Load Balancer Controller, Scylla Operator + StatefulSet,
   ArgoCD HA + AppProject, Aurora bootstrap Job, kube-prometheus-
   stack, Tempo, Loki, Karpenter.
5. **Stage 3 — GitOps + CI** — shared `charts/atpost-service`
   Helm chart, ApplicationSet (one Application per service per
   env), `.github/workflows/build-push.yml` + `promote.yml`.
6. **Per-service values fanout** — 30 services × 2 envs at
   `deploy/services/`. Validator + IRSA wired.
7. **In-video product tags (TikTok-style affiliate overlay)** —
   complete feature, backend + web + mobile. Latest work.
   Detailed doc at `docs/features/in_video_product_tags.md`.
8. **AWS staging deploy runbook** at
   `docs/deploy/aws_staging_runbook.md`. 12-step first-time
   bring-up procedure.

## What's NOT in git (will be lost when Windows wipes)

These are tracked here so the next session knows they're gone +
can reconstitute if needed.

### Auto-memory (Claude's persistent notes)

```
C:\Users\hp\.claude\projects\c--workspace-atpost\memory\
```

Mostly stale audit summaries that are now superseded by the
in-repo docs. **Safe to lose** — re-derive from `git log` if
needed.

### `.claude/settings.json`

Permission allowlist in the project. Modified locally with sed
commands the user approved. Loss is minor — the next Claude
Code session will re-prompt for the same commands and you can
re-approve.

### `.claude/scheduled_tasks.lock`

Runtime lock file. Worthless after reboot.

### `Architecture/docker/.env.backup-1779786758`

**Possibly sensitive** — Docker compose env backup from an old
session. Has dev defaults but check before exposing. **DO NOT
commit; copy contents to a password manager if anything in there
matters.**

### `mobile/atpost_app/android/build/reports/problems/problems-report.html`

Build artifact — Gradle regenerates it.

## Pick-up tasks for the next session

Ordered by what makes the most sense first.

### Immediate (Ubuntu setup)

1. Install tools per the staging runbook §0:
   ```bash
   sudo apt update
   # terraform, awscli v2, kubectl, helm, gh, jq, go 1.25, docker
   sudo snap install terraform helm kubectl
   sudo apt install awscli jq gh
   # Go 1.25 via official tarball — Ubuntu apt is usually one minor
   # behind. https://go.dev/dl/
   curl -fsSL https://get.docker.com | sh
   sudo usermod -aG docker $USER
   # log out + back in for docker group
   ```
2. Clone the two repos (commands above).
3. `gh auth login` to authenticate the GitHub CLI.
4. `aws configure` with staging-account credentials.
5. **Bring up the local Docker stack:**
   ```bash
   cd ~/code/Vchat/modernsmapp
   bash scripts/docker-bringup.sh   # handles IPv4 fix, pulls, builds, health checks
   ```
   Full details: `docs/docker-bringup.md`

### Deploy track (was paused at "ready to apply terraform")

The branch is ready to ship to AWS. Resume at runbook §1 (replace
placeholders) once you have:

- Staging AWS account ID
- GitHub `org/repo` (presumably `rvreddy476/modernsmapp`)
- Cloudflare access to `cleestudio.com`

The whole runbook is `docs/deploy/aws_staging_runbook.md`.
Estimated wall-clock: 4–6 hours.

### Code track follow-ups (captured in commit bodies)

- Drag-to-place placement UI on web + mobile composers (current is
  numeric inputs / sliders).
- Order-create wiring to forward `?via=<affiliate_code>` into the
  commerce checkout payload. Attribution layer is ready on both
  surfaces; commerce-service `Checkout` needs the field added.
- `monetization.affiliate.link_changed` Kafka event consumer in
  post-service to invalidate the validator cache.
- Product-update event consumer to refresh cached label/image_url
  on tags when the underlying listing changes.
- ProductTagComposer wiring on the standalone PostTube watch
  screen (web + mobile) — only reels has the trigger today.
- Redis-batched counter flush worker for impression/click counters
  at scale (currently per-event UPDATE; fine through ~10k/min).
- `docs/deploy/secret_shapes.md` — per-service Secrets Manager
  JSON schema doc.

## How to brief Claude Code on Ubuntu

When you start a fresh Claude Code session on Ubuntu in this repo:

```
I just reinstalled my OS. Read docs/session_resume_ubuntu.md
to see where we left off. Continue from [pick a track from
"Pick-up tasks" above].
```

That's all. Claude will read this doc, the runbook, the feature
doc, and the recent commit bodies — enough context to resume.

## Sanity check before formatting

Run on Windows before wiping:

```bash
# Confirm both repos are fully pushed
git -C /c/workspace/atpost log @{u}..HEAD --oneline    # expect empty
git -C /c/workspace/postbook-ui log @{u}..HEAD --oneline  # expect empty

# Confirm working trees only have the IDE/build noise we already
# accepted as "don't commit"
git -C /c/workspace/atpost status -s
git -C /c/workspace/postbook-ui status -s
```

If either `log @{u}..HEAD` shows commits, push them first:

```bash
git push origin feat/vchat-rebrand-realtime-ui
# or for atpost: git push modernsmapp feat/vchat-rebrand-realtime-ui
```

That's the only checklist that matters. Everything else is in git.
