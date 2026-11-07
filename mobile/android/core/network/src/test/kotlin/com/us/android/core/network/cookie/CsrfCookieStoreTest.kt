package com.us.android.core.network.cookie

import com.google.common.truth.Truth.assertThat
import okhttp3.Cookie
import okhttp3.HttpUrl.Companion.toHttpUrl
import org.junit.Test

/**
 * H1 (audit 2026-10-29): the jar keeps `csrf_token` and nothing else, and
 * never hands out an expired cookie. auth-service sets `access_token` and
 * `refresh_token` cookies on every login and refresh; a jar that kept the
 * access cookie sent the expired token to `/v1/auth/refresh`, the gateway
 * refused it with 401, and every live process was signed out.
 */
class CsrfCookieStoreTest {

    private val url = "https://api.example.test/v1/auth/refresh".toHttpUrl()
    private var now = 1_000_000_000_000L
    private val store = CsrfCookieStore().also { it.now = { now } }

    private fun cookie(name: String, value: String, expiresAt: Long? = null): Cookie =
        Cookie.Builder().name(name).value(value).domain("api.example.test").path("/")
            .apply { if (expiresAt != null) expiresAt(expiresAt) }
            .build()

    @Test
    fun `only csrf_token is kept - the access and refresh cookies are dropped on the way in`() {
        store.saveFromResponse(
            url,
            listOf(
                cookie("access_token", "expired-jwt", expiresAt = now + 60_000),
                cookie("refresh_token", "rt-secret", expiresAt = now + 86_400_000),
                cookie("csrf_token", "csrf-1", expiresAt = now + 86_400_000),
                cookie("oauth_binding", "b", expiresAt = now + 60_000),
            ),
        )

        val sent = store.loadForRequest(url)

        assertThat(sent.map { it.name }).containsExactly("csrf_token")
        assertThat(store.csrfToken()).isEqualTo("csrf-1")
    }

    @Test
    fun `an expired csrf cookie is not sent and not echoed`() {
        store.saveFromResponse(url, listOf(cookie("csrf_token", "csrf-1", expiresAt = now + 1_000)))
        assertThat(store.loadForRequest(url)).hasSize(1)

        // OkHttp's Cookie.matches() ignores expiry: the jar must check it on load.
        now += 1_001

        assertThat(store.loadForRequest(url)).isEmpty()
        assertThat(store.csrfToken()).isNull()
    }

    @Test
    fun `a csrf cookie set already expired deletes the kept one`() {
        store.saveFromResponse(url, listOf(cookie("csrf_token", "csrf-1", expiresAt = now + 86_400_000)))

        // clearAuthCookies on logout: Expires in the past.
        store.saveFromResponse(url, listOf(cookie("csrf_token", "", expiresAt = now - 86_400_000)))

        assertThat(store.loadForRequest(url)).isEmpty()
        assertThat(store.csrfToken()).isNull()
    }

    @Test
    fun `a session csrf cookie with no expiry is kept, and only for a matching host`() {
        store.saveFromResponse(url, listOf(cookie("csrf_token", "csrf-2")))

        assertThat(store.loadForRequest(url).map { it.value }).containsExactly("csrf-2")
        assertThat(store.loadForRequest("https://cdn.elsewhere.test/x".toHttpUrl())).isEmpty()
    }

    @Test
    fun `clear forgets the csrf token`() {
        store.saveFromResponse(url, listOf(cookie("csrf_token", "csrf-1")))

        store.clear()

        assertThat(store.loadForRequest(url)).isEmpty()
        assertThat(store.csrfToken()).isNull()
    }
}
