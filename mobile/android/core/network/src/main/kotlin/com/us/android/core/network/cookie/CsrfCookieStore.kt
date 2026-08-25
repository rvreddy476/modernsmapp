package com.us.android.core.network.cookie

import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton

/**
 * In-memory cookie jar whose only real job is holding `csrf_token`.
 *
 * Bearer is authoritative for session state (blocker B4, resolved
 * 2026-08-16): the server also sets `access_token` / `refresh_token` cookies
 * on login, and those are deliberately ignored. Session state lives in
 * `:core:auth`, never in a cookie.
 *
 * Not persisted across process death, and that is fine: the CSRF cookie is
 * re-issued by the auth endpoints, and `/v1/auth/refresh` is not behind the
 * CSRF middleware (which guards only the `protected` route group), so a cold
 * start can always re-establish one.
 */
@Singleton
class CsrfCookieStore @Inject constructor() : CookieJar {

    private val cookies = ConcurrentHashMap<String, Cookie>()

    override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
        cookies.forEach { cookie ->
            if (cookie.expiresAt < System.currentTimeMillis()) {
                this.cookies.remove(cookie.name)
            } else {
                this.cookies[cookie.name] = cookie
            }
        }
    }

    override fun loadForRequest(url: HttpUrl): List<Cookie> =
        cookies.values.filter { it.matches(url) }

    /** The server-issued CSRF token, or null if none has been issued yet. */
    fun csrfToken(): String? = cookies[CSRF_COOKIE_NAME]?.value?.takeIf { it.isNotBlank() }

    fun clear() = cookies.clear()

    companion object {
        const val CSRF_COOKIE_NAME = "csrf_token"
    }
}
