# Runbook — minimal AWS media smoke

Purpose: prove S3 presigning, bucket CORS, CloudFront delivery and IAM work,
using a real phone, **without** standing up the 23-module staging stack.

Status: not yet run. Nothing is deployed to AWS.

---

## 1. What this proves, and what it does not

**Proves** — the things MinIO structurally cannot:

- S3 presigned PUT accepted by real AWS (SigV4, expiry, region)
- bucket CORS as actually configured
- CloudFront delivery of reads from the edge
- the IAM policy is narrow enough to be safe and wide enough to work
- behaviour of a multi-megabyte upload over a real mobile radio

**Does NOT prove:**

- **IRSA.** See §2 — the production credential path cannot run outside EKS, so
  this smoke uses the non-production storage path. Bucket/CORS/CloudFront/IAM
  are real; the *credential mechanism* is not the production one.
- Rekognition moderation (`MEDIA_SCANNER_BACKEND=rekognition` has the same
  IRSA-only guard).
- Anything about EKS, Aurora, MSK, OpenSearch or Scylla.

That is a deliberate trade. IRSA is provable only once a cluster exists, and it
is not what is most likely to be wrong.

---

## 2. The constraint that shapes this

`blob.NewS3IRSA` (`media-service/internal/store/blob/store.go:51`) **refuses to
start** unless it is running under EKS IRSA:

```go
if os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
    return nil, fmt.Errorf("s3/irsa: static AWS credentials are forbidden")
}
if os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE") == "" || os.Getenv("AWS_ROLE_ARN") == "" {
    return nil, fmt.Errorf("s3/irsa: ... node-role fallback is forbidden")
}
```

That rules out EC2 instance roles and ECS task roles too — it is web-identity or
nothing. **This is good design and should not be weakened for a smoke test.**

The way through: the *other* constructor, `NewWithPublicEndpoint`, takes an
arbitrary endpoint plus static SigV4 credentials, and the client it uses speaks
S3. Pointing it at `s3.ap-south-1.amazonaws.com` with an IAM user's keys talks
to real S3 and produces real presigned URLs.

That path is refused when `isProductionEnv()` is true, so it stays impossible in
production and available in a smoke environment. **No code change is required.**

`MEDIA_CDN_BASE_URL` is read by *both* constructors (`store.go:92` and `:155`),
so CloudFront is exercised on this path as well.

### The upload really does leave the phone

The API can stay on `adb reverse` loopback. The presigned URL the server returns
points at **AWS**, not localhost, so the bytes travel over the real mobile
network to real S3, and reads come back through CloudFront. The expensive-to-
prove parts are exercised even though the API is local.

---

## 3. Cost

Provisioned: one S3 bucket, one KMS key, one CloudFront distribution, one IAM
policy. For a smoke test this is small — a KMS key has a fixed monthly charge,
CloudFront and S3 are effectively usage-priced at this volume.

The reason to do it this way is what is **avoided**: EKS control plane, Aurora,
MSK, OpenSearch, ElastiCache, Scylla, WAF and NAT gateways all run whether or
not anyone uses them.

`terraform destroy` when finished. Apply for
[AWS Activate](https://aws.amazon.com/startups/credits/) first — it is still an
open founder action in the strategy doc and costs nothing to request.

---

## 4. Steps

### 4.1 Credentials

```bash
aws login
```

```bash
aws sts get-caller-identity
```

Expect account `215302750595` (Decision 002). Nothing is configured on the
current workstation — `aws sts get-caller-identity` currently returns
`NoCredentials`.

### 4.2 Provision

```bash
cd infra/terraform/envs/smoke
```

```bash
terraform init
```

```bash
terraform plan
```

Read the plan before applying. Expect roughly: S3 bucket + versioning + public
access block + SSE + lifecycle + CORS, a KMS key and alias, a CloudFront
distribution + OAC + response headers policy, a bucket policy, and one IAM
policy. No VPC, no cluster.

```bash
terraform apply
```

```bash
terraform output
```

### 4.3 A least-privilege user for the smoke

```bash
aws iam create-user --user-name atpost-media-smoke
```

```bash
aws iam attach-user-policy --user-name atpost-media-smoke --policy-arn "$(terraform output -raw client_iam_policy_arn)"
```

The module already defines the client policy; do not hand-write one.

```bash
aws iam create-access-key --user-name atpost-media-smoke
```

> Treat the output as a credential. Put it in `Architecture/docker/.env`, which
> is gitignored — never in `docker-compose.yml`.

The client policy may not include `s3:GetBucketLocation`, which the S3-compatible
client calls to resolve the region when no region is passed. If media-service
fails to start with a region or location error, that permission is the first
thing to check.

### 4.4 Point the local stack at AWS

In `Architecture/docker/.env`:

```bash
MINIO_ENDPOINT=s3.ap-south-1.amazonaws.com
MINIO_USE_SSL=true
MINIO_ACCESS_KEY=<access key id>
MINIO_SECRET_KEY=<secret access key>
MINIO_BUCKET=<terraform output s3_bucket>
MINIO_PUBLIC_ENDPOINT=https://s3.ap-south-1.amazonaws.com
MEDIA_CDN_BASE_URL=https://<terraform output cloudfront_domain>
DEPLOY_ENV=smoke
```

Leave `MEDIA_STORAGE_BACKEND` **unset**. Setting it to `s3` selects the
IRSA-only path, which will refuse to start (§2).

`DEPLOY_ENV` must not be `production` or `prod`, or the static-credential path
is refused — correctly.

```bash
docker compose -f Architecture/docker/docker-compose.yml up -d --build media-service
```

```bash
docker logs atpost_stack-media-service-1 --tail 30
```

A failure to start here is the test doing its job: it means bucket, credentials
or region are wrong, and it is far better to learn that now than from a phone.

### 4.5 Device

The handset carries a live SIM. Coordinate before driving it; do not inject
blind input.

```bash
adb reverse tcp:8080 tcp:8080
```

```bash
cd mobile/android && ./gradlew.bat installDevDebug
```

The dev flavour permits cleartext to loopback only, which is what `adb reverse`
provides. The media bytes still go to AWS over the mobile network.

### 4.6 What to observe

Compose a post with a photo, and confirm:

1. `/v1/media/init` returns an `upload_url` on `s3.ap-south-1.amazonaws.com`
2. the PUT succeeds from the device — **this is the CORS and SigV4 test**
3. `/v1/media/confirm` returns 200
4. status reaches exactly `processing_status=ready` and `moderation_status=passed`
5. the post renders and its image URL is the **CloudFront** domain, not S3
6. the image loads over mobile data with WiFi off
7. push arrives on the lock screen — the last unproven link in the notification
   chain (backlog D-D1)

Record the result, redacting presigned signatures and credentials.

### 4.7 Tear down

```bash
cd infra/terraform/envs/smoke && terraform destroy
```

```bash
aws iam delete-user --user-name atpost-media-smoke
```

Delete the access key first; IAM refuses to remove a user that still has one.

---

## 5. If you want the full staging environment instead

Different job, and larger:

1. `infra/terraform/bootstrap` — creates the state bucket and lock table.
   Everything else depends on it.
2. Fill in the commented-out `backend.tf` blocks in `envs/staging` and
   `envs/prod`; both are still `TODO`.
3. Replace every `CHANGEME` in the `.tfvars.example` files.
4. A real domain and TLS for the `dns` module. iOS now enforces ATS, so HTTPS
   is no longer optional.
5. Fill in `AppEnvironment.STAGING` in
   `build-logic/convention/.../ProjectExtensions.kt` — `apiBaseUrl` is
   deliberately empty with a TODO, so a staging build currently fails at its
   first request by design.
6. Decide what happens to `infra/azure/`. Decision 001 says AWS is the only
   cloud, yet a complete Azure staging and prod tree exists alongside CI
   workflows for it. Two unapplied infrastructures is worse than one.
