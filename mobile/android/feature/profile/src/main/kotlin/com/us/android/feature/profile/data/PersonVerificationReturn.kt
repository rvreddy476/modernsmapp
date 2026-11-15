package com.us.android.feature.profile.data

import com.us.android.core.model.WebLinks
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.net.URI
import java.net.URLDecoder
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What DigiLocker hands back on the return page. Both are credentials: never logged,
 * never shown, never put in a navigation route. [toString] says nothing.
 */
class PersonReturn(val code: String, val state: String) {
    override fun equals(other: Any?): Boolean =
        other is PersonReturn && other.code == code && other.state == state
    override fun hashCode(): Int = 31 * code.hashCode() + state.hashCode()
    override fun toString(): String = "PersonReturn(redacted)"
}

/** The web page DigiLocker redirects to (the server's PERSON_VERIFICATION_REDIRECT_URI). */
const val PERSON_RETURN_PATH = "/settings/verification/person/return"

/**
 * Pure rules for the DigiLocker leg's return link, so they are JVM-tested.
 * Parsed with [java.net.URI]; `android.net.Uri` is stubbed in unit tests.
 */
object PersonReturnLinks {

    /**
     * Local test mode (`DIGILOCKER_MODE=mock`): the server's authorize_url IS already the return
     * URL (`http://localhost:3000/settings/verification/person/return?code=mock-…&state=…`). When
     * [authorizeUrl]'s path ends with [PERSON_RETURN_PATH] and carries both a code and a state,
     * the app skips the browser and posts the callback directly. Any host: the server chose it.
     */
    fun mockReturnOf(authorizeUrl: String?): PersonReturn? {
        val uri = parse(authorizeUrl) ?: return null
        val scheme = uri.scheme?.lowercase()
        if (scheme != "http" && scheme != "https") return null
        if (!uri.rawPath.orEmpty().trimEnd('/').endsWith(PERSON_RETURN_PATH)) return null
        return returnOf(uri)
    }

    /**
     * The verified App Link (`https://cleestudio.com` or `https://app.cleestudio.com` +
     * [PERSON_RETURN_PATH]) with both a code and a state; null for every other link.
     */
    fun appLinkReturnOf(link: String?): PersonReturn? {
        val uri = parse(link) ?: return null
        if (!uri.scheme.equals("https", ignoreCase = true)) return null
        if (uri.port != -1 || uri.rawUserInfo != null) return null
        if (WebLinks.WEB_HOSTS.none { it.equals(uri.host, ignoreCase = true) }) return null
        if (uri.rawPath.orEmpty().trimEnd('/') != PERSON_RETURN_PATH) return null
        return returnOf(uri)
    }

    private fun parse(link: String?): URI? {
        val trimmed = link?.trim()?.takeIf { it.isNotEmpty() } ?: return null
        return runCatching { URI(trimmed) }.getOrNull()
    }

    private fun returnOf(uri: URI): PersonReturn? {
        val query = query(uri.rawQuery)
        val code = query["code"]?.takeIf { it.isNotBlank() } ?: return null
        val state = query["state"]?.takeIf { it.isNotBlank() } ?: return null
        return PersonReturn(code = code, state = state)
    }

    /** The first value of each name; undecodable pairs are dropped. */
    private fun query(raw: String?): Map<String, String> {
        if (raw.isNullOrEmpty()) return emptyMap()
        val out = LinkedHashMap<String, String>()
        raw.split('&').forEach { pair ->
            val name = decode(pair.substringBefore('=')) ?: return@forEach
            val value = if ('=' in pair) decode(pair.substringAfter('=')) else ""
            if (value != null && name !in out) out[name] = value
        }
        return out
    }

    private fun decode(part: String): String? =
        runCatching { URLDecoder.decode(part, Charsets.UTF_8.name()) }.getOrNull()
}

/**
 * The DigiLocker return, between the activity that receives the App Link and the
 * person-verification screen that posts it. One pending return at a time; the screen
 * [take]s it, so a return is posted once.
 */
@Singleton
class PersonVerificationReturns @Inject constructor() {

    private val _pending = MutableStateFlow<PersonReturn?>(null)

    /** Non-null while a return waits to be posted. */
    val pending: StateFlow<PersonReturn?> = _pending.asStateFlow()

    /**
     * Claims [uri] when it is the person-verification return App Link, and holds it until the
     * screen takes it. False for every other link, which the caller routes as before.
     */
    fun offerRedirect(uri: String?): Boolean {
        val link = PersonReturnLinks.appLinkReturnOf(uri) ?: return false
        _pending.value = link
        return true
    }

    /** The pending return, once; null when there is none. */
    fun take(): PersonReturn? {
        val link = _pending.value ?: return null
        _pending.compareAndSet(link, null)
        return link
    }
}
