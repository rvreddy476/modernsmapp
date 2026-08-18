package com.us.android.core.network.interceptor

import com.us.android.core.network.ApiConfig
import com.us.android.core.network.TokenProvider
import com.us.android.core.network.cookie.CsrfCookieStore
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.Interceptor
import okhttp3.Response
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Headers every request carries.
 *
 * Note what is NOT here: `X-User-Id`. The Flutter client sends it
 * ([auth_interceptor.dart:19]) and it is harmless — auth middleware overwrites
 * it from the JWT claims before any handler reads it — but putting
 * client-asserted identity on the wire has no upside. Modification M5.
 */
@Singleton
class ClientHeadersInterceptor @Inject constructor(
    private val config: ApiConfig,
) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request().newBuilder()
            .header("Accept", "application/json")
            .header("X-Requested-With", "XMLHttpRequest")
            .header("X-Client-Platform", "android")
            .header("X-Client-Version", config.clientVersion)
            // Correlates with `meta.request_id` in the response envelope, so a
            // user-reported failure is traceable in backend logs.
            .header("X-Request-Id", UUID.randomUUID().toString())
            .build()
        return chain.proceed(request)
    }
}

/**
 * Bearer auth. Skips the endpoints that mint tokens — sending a stale (often
 * expired) Authorization header to `/login` or `/refresh` is at best noise and
 * at worst a 401 loop.
 */
@Singleton
class AuthInterceptor @Inject constructor(
    private val tokenProvider: TokenProvider,
    private val config: ApiConfig,
) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()

        // Only OUR origin gets the bearer token.
        //
        // Found on a device on 2026-08-18: media segment URLs are absolute,
        // pre-signed object-store links, and this client is shared with Media3
        // so it was attaching the bearer to them too. MinIO rejected every
        // segment with 400 "request has multiple authentication types, please
        // use one", and reels rendered a black frame.
        //
        // The playback break is the smaller half. Sending the access token to
        // an arbitrary host LEAKS IT — in production those URLs point at
        // CloudFront, so every segment fetch would have handed a third party a
        // credential that authenticates as the user. A bearer token belongs to
        // exactly one origin and must never travel anywhere else, redirects
        // included.
        if (!request.url.isApiOrigin()) {
            return chain.proceed(request)
        }

        val path = request.url.encodedPath
        if (NO_AUTH_PATHS.any { path.endsWith(it) }) {
            return chain.proceed(request)
        }
        val token = tokenProvider.currentAccessToken()
            ?: return chain.proceed(chain.request())

        return chain.proceed(
            chain.request().newBuilder()
                .header("Authorization", "Bearer $token")
                .build(),
        )
    }

    /**
     * Host AND port must match the configured API base URL.
     *
     * Comparing the host alone is not enough here: in local development the
     * gateway is 127.0.0.1:8080 and the object store is 127.0.0.1:9000, so a
     * host-only check would still leak the token to storage.
     */
    private fun HttpUrl.isApiOrigin(): Boolean {
        val api = config.baseUrl.toHttpUrlOrNull() ?: return false
        return host == api.host && port == api.port
    }

    private companion object {
        val NO_AUTH_PATHS = listOf(
            "/v1/auth/login",
            "/v1/auth/register",
            "/v1/auth/refresh",
        )
    }
}

/**
 * Double-submit CSRF, matching the server's check in
 * identity-platform/.../internal/http/middleware.go:256 — it compares the
 * `X-CSRF-Token` header against the `csrf_token` cookie.
 *
 * The Flutter client *fabricates* a random token client-side
 * ([csrf_interceptor.dart:11]) and only later picks up the real one from
 * Set-Cookie. That is not CSRF protection; it is a value that happens to be
 * ignored. Here, when no cookie has been issued yet we send no header at all —
 * a clean 403 is far easier to diagnose than a silent mismatch. Modification M3.
 */
@Singleton
class CsrfInterceptor @Inject constructor(
    private val cookieStore: CsrfCookieStore,
) : Interceptor {
    override fun intercept(chain: Interceptor.Chain): Response {
        val request = chain.request()
        if (request.method !in MUTATING_METHODS) {
            return chain.proceed(request)
        }
        val token = cookieStore.csrfToken() ?: return chain.proceed(request)
        return chain.proceed(
            request.newBuilder().header("X-CSRF-Token", token).build(),
        )
    }

    private companion object {
        val MUTATING_METHODS = setOf("POST", "PUT", "PATCH", "DELETE")
    }
}
