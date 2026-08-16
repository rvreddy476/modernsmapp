package com.us.android.core.network.retry

import okhttp3.Interceptor
import okhttp3.Response
import retrofit2.Invocation
import java.io.IOException
import java.io.InterruptedIOException
import java.util.concurrent.ThreadLocalRandom
import kotlin.math.min
import kotlin.math.pow

/**
 * Explicit, per-endpoint retry with bounded exponential backoff and jitter.
 *
 * This replaces OkHttp's `retryOnConnectionFailure(true)`, which is global and
 * therefore applied to non-idempotent writes as well as safe reads. Turning
 * that off costs OkHttp's automatic route failover — but only for calls that
 * are not retried here, and those are exactly the calls that must not be
 * repeated blindly.
 *
 * Eligibility:
 *  - `GET` / `HEAD` — idempotent by definition, retried by default.
 *  - Anything else — retried only when the Retrofit method carries
 *    [Retryable].
 *
 * Retries cover **transport** failures (connection refused, reset, DNS) and
 * `503`/`504`. A `500` is NOT retried: it usually means the request was
 * received and failed, so repeating it risks duplicating whatever partial
 * effect it had.
 */
// Not @Inject-constructed: the default arguments generate a second synthetic
// constructor, and Dagger rejects a type with two injected constructors.
// NetworkModule provides it, which is also where the policy belongs.
class RetryInterceptor(
    private val config: RetryConfig = RetryConfig(),
    private val sleeper: Sleeper = Sleeper.Default,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        if (!request.isRetryEligible()) {
            return chain.proceed(request)
        }

        val deadline = System.nanoTime() + config.totalBudgetMillis * NANOS_PER_MILLI
        var attempt = 0
        var lastFailure: IOException? = null

        while (true) {
            try {
                val response = chain.proceed(request)
                if (!response.isRetryableStatus()) return response

                // A retryable status still holds a body and a socket; closing
                // it before looping is what prevents connection-pool leaks.
                response.close()
                lastFailure = null
            } catch (e: InterruptedIOException) {
                // Timeouts and thread interruption are not "try again harder".
                throw e
            } catch (e: IOException) {
                lastFailure = e
            }

            attempt++
            val wait = waitBeforeNextAttempt(attempt, deadline) ?: break
            sleeper.sleep(wait)
        }

        // Attempts or budget exhausted. Surface the real failure, or re-issue
        // once so the caller gets a real response rather than a synthesised one.
        lastFailure?.let { throw it }
        return chain.proceed(request)
    }

    /**
     * How long to wait before [attempt], or null when there must not be another.
     *
     * Folding both stop conditions into one decision keeps the retry loop to a
     * single exit and puts the budget arithmetic in one place. Starting an
     * attempt that cannot finish inside the budget would turn a fast failure
     * into a slow one.
     */
    private fun waitBeforeNextAttempt(attempt: Int, deadlineNanos: Long): Long? {
        if (attempt >= config.maxAttempts) return null
        val wait = backoffMillis(attempt)
        if (System.nanoTime() + wait * NANOS_PER_MILLI >= deadlineNanos) return null
        return wait
    }

    /**
     * Full jitter: a uniform draw from `[0, exponential)`.
     *
     * Equal-spaced retries from many clients re-synchronise into a thundering
     * herd against a service that is already struggling — which is when
     * retries happen. Jitter is what breaks that lockstep.
     */
    private fun backoffMillis(attempt: Int): Long {
        val exponential = config.baseDelayMillis * 2.0.pow(attempt - 1)
        val capped = min(exponential, config.maxDelayMillis.toDouble()).toLong()
        return ThreadLocalRandom.current().nextLong(1, capped.coerceAtLeast(2))
    }

    private fun okhttp3.Request.isRetryEligible(): Boolean {
        if (method == "GET" || method == "HEAD") return true
        val invocation = tag(Invocation::class.java) ?: return false
        return invocation.method().isAnnotationPresent(Retryable::class.java)
    }

    private fun Response.isRetryableStatus(): Boolean =
        code == HTTP_SERVICE_UNAVAILABLE || code == HTTP_GATEWAY_TIMEOUT

    private companion object {
        const val HTTP_SERVICE_UNAVAILABLE = 503
        const val HTTP_GATEWAY_TIMEOUT = 504
        const val NANOS_PER_MILLI = 1_000_000L
    }
}

/**
 * Retry bounds.
 *
 * [totalBudgetMillis] must stay below the OkHttp `callTimeout`, or the call is
 * killed mid-backoff and the budget means nothing.
 */
data class RetryConfig(
    val maxAttempts: Int = 3,
    val baseDelayMillis: Long = 300,
    val maxDelayMillis: Long = 3_000,
    val totalBudgetMillis: Long = 10_000,
)

/** Indirection so tests do not spend real seconds asleep. */
fun interface Sleeper {
    fun sleep(millis: Long)

    companion object {
        val Default = Sleeper { millis ->
            try {
                Thread.sleep(millis)
            } catch (e: InterruptedException) {
                Thread.currentThread().interrupt()
                throw InterruptedIOException("retry interrupted").initCause(e) as InterruptedIOException
            }
        }
    }
}
