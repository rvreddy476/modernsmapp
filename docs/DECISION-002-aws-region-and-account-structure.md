# Decision 002 — AWS region and account structure

Date: 2026-08-14
Decided by: founder
Status: accepted
Supersedes nothing. Extends `prompt/decision-001-aws-canonical-cloud.md` (AWS is
the only cloud).

> **Why this lives in `docs/` and not `prompt/`:** `prompt/` is gitignored
> (`.gitignore:34`), so Decision 001 has no version history and exists only in
> one working directory. Infrastructure decisions have to outlive a laptop.

## Account

| | |
|---|---|
| AWS account ID | `215302750595` |
| Account name | `gowthambandi2005` |
| Identity | IAM Identity Center, portal `https://d-9f6756e727.awsapps.com/start` |
| CLI profile | `atpost` |

## Decision 1 — Region: `ap-south-1` (Mumbai)

All deployment infrastructure runs in `ap-south-1`: EKS, RDS/PostgreSQL, S3
media, Rekognition moderation, SES, and every ECR repository.

**Rationale.** The product is India-first, and every Helm values file already
assumed `ap-south-1`. Choosing anything else meant editing the deployment region
too, for no user benefit.

**What this settles.** Indian user media, and the automated moderation performed
on it, are stored and processed in India. Codex flagged data residency as an
unresolved founder/legal question; this closes it in the conservative direction —
data does not leave the country for routine processing.

**One exception, and it is not a residency exception.** The AWS Agent Toolkit
control plane is only available in `us-east-1` and is pinned there:

```
aws configure agent-toolkit --yes --region us-east-1
```

That endpoint carries the *development agent's* API calls. No user media, user
records, or moderation content passes through it. `~/.aws/config` remains
`ap-south-1` so all ordinary work targets the deployment region.

**Reopen if:** legal advice requires a specific Indian data-protection posture
beyond region placement, or the product expands to a market where in-region
processing is mandatory.

## Decision 2 — Single account for prod and staging

Both environments live in account `215302750595`, separated by Kubernetes
namespace and by the `atpost-prod-*` / `atpost-staging-*` IAM naming already used
throughout the manifests.

**Rationale.** One founder, a launch deadline, and no team boundary to enforce.
Multi-account adds AWS Organizations, cross-account IAM, and per-account billing
setup before a single user exists.

**What is being accepted, explicitly.** This was flagged as the riskier default
and chosen anyway, so the costs are recorded rather than discovered:

1. **Shared blast radius.** A mistake with broad IAM in staging can reach
   production data. There is no account boundary to stop it.
2. **No billing separation.** Staging and production spend are one line. A
   runaway transcode or Rekognition loop cannot be attributed by account.
3. **Shared service quotas.** Staging load can exhaust a quota production needs.
4. **Retrofitting is expensive.** Splitting later means migrating IAM roles, ECR
   images, S3 data and CloudFront distributions — hardest once real user data
   exists.

**Mitigations that are already in place.** These reduce (1) but do not remove it:

- Every service uses a distinct IRSA role per environment
  (`atpost-prod-<svc>-irsa` vs `atpost-staging-<svc>-irsa`), so a staging
  workload cannot assume a production role.
- No static AWS credentials anywhere; the media worker refuses to start if it
  finds `AWS_ACCESS_KEY_ID` in its environment.
- Launch gates (`OAUTH_NEW_ACCOUNT_ENABLED`, `REVIEWER_PUBLIC_ENABLED`) are
  default-off per environment.

**Reopen when:** a second person needs AWS access, real user data exists in
production, or spend needs to be attributed per environment. The natural moment
is before the first real users, not after.

## Follow-up not covered here

`AdministratorAccess` is currently assigned to the founder's Identity Center
user. Correct for setup. The Agent Toolkit's design assumes agents receive
read-only policies while humans hold write access, so agent-facing roles should
be narrowed before launch. Tracked as pre-launch hardening, not part of this
decision.
