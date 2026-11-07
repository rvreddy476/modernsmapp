package com.us.android.core.network.cookie

import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl
import java.util.concurrent.atomic.AtomicReference
import javax.inject.Inject
import javax.inject.Singleton

/**
 * In-memory cookie jar that holds `csrf_token` and NOTHING else.
 *
 * Bearer is authoritative for session state (blocker B4, resolved
 * 2026-08-16). auth-service also sets `access_token` and `refresh_token`
 * cookies on every login and refresh (`setAuthCookies`), and this jar must
 * never keep or send them (H1, audit 2026-10-29):
 *
 *  - The gateway falls back to the `access_token` cookie when a request has
 *    no Bearer header, and `/v1/auth/refresh` carries none. A kept cookie
 *    is, by the time anyone refreshes, the very token that just expired. The
 *    gateway refused it with 401, and `SessionManager` read that as the
 *    refresh token being rejected: every live process was signed out once
 *    per access-token lifetime.
 *  - OkHttp's [Cookie.matches] checks domain, path and `secure` but not
 *    expiry; a jar has to check expiry itself, on the way OUT as well as in.
 *  - A `refresh_token` cookie would ride on every API call to every service.
 *    The refresh token travels only in the JSON body of `/refresh` and
 *    `/logout`, read from the encrypted token store.
 *
 * Not persisted across process death, and that is fine: the CSRF cookie is
 * re-issued by the auth endpoints, and `/v1/auth/refresh` is not behind the
 * CSRF middleware (which guards only the `protected` route group), so a cold
 * start can always re-establish one.
 */
@Singleton
class CsrfCookieStore @Inject constructor() : CookieJar {

    private val csrf = AtomicReference<Cookie?>(null)

    /** Test seam for expiry; production reads the wall clock. */
    internal var now: () -> Long = System::currentTimeMillis

    override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
        // A cookie set already expired is how a server deletes one
        // (clearAuthCookies on logout). Storing it is enough: the expiry check
        // on the way out treats it as absent and drops it.
        cookies.filter { it.name == CSRF_COOKIE_NAME }.forEach { csrf.set(it) }
    }

    override fun loadForRequest(url: HttpUrl): List<Cookie> {
        val cookie = liveCsrfCookie() ?: return emptyList()
        return if (cookie.matches(url)) listOf(cookie) else emptyList()
    }

    /** The server-issued CSRF token, or null if none has been issued or it has expired. */
    fun csrfToken(): String? = liveCsrfCookie()?.value?.takeIf { it.isNotBlank() }

    fun clear() = csrf.set(null)

    private fun liveCsrfCookie(): Cookie? {
        val cookie = csrf.get() ?: return null
        if (!cookie.isExpired()) return cookie
        csrf.compareAndSet(cookie, null)
        return null
    }

    private fun Cookie.isExpired(): Boolean = expiresAt <= now()

    companion object {
        const val CSRF_COOKIE_NAME = "csrf_token"
    }
}
