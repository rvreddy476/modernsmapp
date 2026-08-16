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
 * `runBlocking` is correct here. OkHttp's Authenticator contract is
 * synchronous and this already runs on a background dispatcher thread, never
 * the main thread. Single-flight collapsing lives in the [TokenRefresher]
 * implementation, so N concurrent 401s produce one refresh.
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

        val newToken = runBlocking { tokenRefresher.refresh() } ?: return null

        // Another thread may already have refreshed while this one waited. If
        // the token changed, retry; if it did not, the refresh genuinely did
        // not help and retrying would just 401 again.
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
