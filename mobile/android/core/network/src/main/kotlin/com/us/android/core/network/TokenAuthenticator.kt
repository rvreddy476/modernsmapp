package com.us.android.core.network

import kotlinx.coroutines.runBlocking
import okhttp3.Authenticator
import okhttp3.Request
import okhttp3.Response
import okhttp3.Route
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Refreshes the access token on 401 and replays the original request.
 *
 * An [Authenticator] rather than an interceptor: OkHttp calls it only after a
 * 401, hands back the response chain, and performs the retry itself. An
 * interceptor would have to re-issue the call manually and get the
 * retry/redirect bookkeeping right by hand.
 *
 * `runBlocking` is safe here ONLY because the refresh does not run on the
 * client this authenticator serves (H2, audit 2026-10-29). Each 401'd call
 * blocks in here while holding one of its dispatcher's per-host slots (five
 * by default). A refresh queued on that same dispatcher waits for a slot that
 * none of the blocked calls will ever release, and the API host is dead until
 * the process dies. `:core:auth` therefore sends `/v1/auth/refresh` through
 * the separate `@SessionClient` (its own dispatcher, its own timeouts, no
 * authenticator). Single-flight collapsing lives in the [TokenRefresher], so
 * N concurrent 401s produce one refresh.
 */
@Singleton
class TokenAuthenticator @Inject constructor(
    private val tokenRefresher: TokenRefresher,
) : Authenticator {

    override fun authenticate(route: Route?, response: Response): Request? {
        // Never try to refresh a failed refresh. Without this the 401 from an
        // expired or revoked refresh token drives an infinite loop.
        if (response.request.url.encodedPath.endsWith(REFRESH_PATH)) return null

        // Give up after a couple of attempts on the same request. OkHttp will
        // otherwise keep calling us as long as we keep returning a request.
        if (responseCount(response) >= MAX_ATTEMPTS) return null

        val failedToken = response.request.header("Authorization")
            ?.removePrefix("Bearer ")
            ?.trim()

        // If another call already refreshed, this returns that token without a
        // second refresh (L4).
        val newToken = runBlocking { tokenRefresher.refreshIfStale(failedToken) } ?: return null

        // The token did not change: the refresh genuinely did not help and
        // retrying would just 401 again.
        if (newToken == failedToken) return null

        return response.request.newBuilder()
            .header("Authorization", "Bearer $newToken")
            .build()
    }

    private fun responseCount(response: Response): Int {
        var count = 1
        var prior = response.priorResponse
        while (prior != null) {
            count++
            prior = prior.priorResponse
        }
        return count
    }

    private companion object {
        const val REFRESH_PATH = "/v1/auth/refresh"
        const val MAX_ATTEMPTS = 2
    }
}
