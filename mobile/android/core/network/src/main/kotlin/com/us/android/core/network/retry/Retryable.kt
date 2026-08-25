package com.us.android.core.network.retry

/**
 * Marks a Retrofit endpoint as safe to retry automatically.
 *
 * Opt-IN, deliberately. `GET`/`HEAD` are idempotent by definition and retry
 * without this annotation; **everything else must declare it**, because the
 * platform's write endpoints are not idempotent unless they say so.
 *
 * The rule this enforces comes from the quality bar §2: "A retry policy is
 * attached only to idempotent operations." Before this existed the client set
 * `retryOnConnectionFailure(true)` globally, which meant OkHttp would re-send
 * a `POST /v1/auth/register` whose connection dropped mid-flight — a duplicate
 * account attempt whose outcome depended on a unique constraint rather than on
 * design.
 *
 * Do NOT add this to an endpoint just because a retry would be convenient. Add
 * it when the server genuinely produces the same result for a repeated call —
 * which for a write means it honours an idempotency key.
 */
@Target(AnnotationTarget.FUNCTION)
@Retention(AnnotationRetention.RUNTIME)
annotation class Retryable
