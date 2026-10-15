package com.us.android.core.model

/**
 * The app's public web addresses, in ONE place: what "Copy link" and Share
 * hand out, and what the manifest's App Links route back into the app.
 *
 * A post's address is the web client's public permalink, `https://<host>/post/<id>` — the route
 * postbook-ui serves at `src/app/post/[postId]`. It replaced `/p/<id>`,
 * which no client served, so every copied link was a 404.
 *
 * Pure string work, no `Uri`: the parser is unit-tested off-device, and the
 * app's unit tests stub Android to defaults.
 */
object WebLinks {

    /**
     * The host chat invite links are minted on (message-service
     * `INVITE_LINK_BASE_URL`) and the manifest's `/chat/join/` App Link.
     */
    const val INVITE_HOST = "atpost.app"

    /**
     * The web app's public host: production serves postbook-ui on
     * `cleestudio.com` and `app.cleestudio.com` (deploy/web/app/values-prod.yaml).
     * Post permalinks are minted on the bare host and accepted on both.
     */
    const val WEB_HOST = "cleestudio.com"
    val WEB_HOSTS: Set<String> = setOf(WEB_HOST, "app.$WEB_HOST")

    private const val ORIGIN = "https://$WEB_HOST"
    private const val POST_SEGMENT = "post"

    /** `https://cleestudio.com/post/<postId>` — a post's public permalink. */
    fun postPermalink(postId: String): String = "$ORIGIN/$POST_SEGMENT/$postId"

    /**
     * `https://cleestudio.com/u/<username>` — a profile's public page, the
     * route postbook-ui serves at `src/app/u/[username]` (a public prefix
     * there, so the link opens for someone who is not signed in). Share
     * profile hands this out (T-21, 2026-10-15).
     *
     * A leading `@` and surrounding space are dropped, and everything outside
     * the URL's unreserved characters is percent-encoded, so a handle can
     * never add a path segment, a query or a fragment. Null for a blank
     * handle: there is no page to link to, and `/u/` alone is not one.
     *
     * Not parsed back: the app has no profile-by-username route, so there is
     * no `/u/` App Link and a tapped profile link opens the web page.
     */
    fun profileUrl(username: String?): String? {
        val handle = username?.trim()?.removePrefix("@")?.trim().orEmpty()
        if (handle.isEmpty()) return null
        return "$ORIGIN/$PROFILE_SEGMENT/${handle.percentEncoded()}"
    }

    /**
     * The post id in `https://cleestudio.com/post/<id>` (a trailing slash, query
     * and fragment ignored), or null for any other link: another host, plain
     * http, another path, extra segments, or an id that is not a UUID. Post
     * ids are UUIDs on the server (post-service parses every one with
     * `uuid.Parse`), so anything else could only ever open "not found".
     */
    fun postIdOf(link: String?): String? {
        val segments = pathSegmentsOn(link) ?: return null
        if (segments.size != POST_SEGMENTS || segments[0] != POST_SEGMENT) return null
        return segments[1].takeIf { it.isUuid() }
    }

    /**
     * The path segments of a link on one of [WEB_HOSTS], or null when the
     * link is on any other scheme or host. A port or user-info part makes it
     * another host.
     */
    private fun pathSegmentsOn(link: String?): List<String>? {
        val trimmed = link?.trim()?.takeIf { it.isNotEmpty() } ?: return null
        if (!trimmed.startsWith("https://", ignoreCase = true)) return null
        val rest = trimmed.substring("https://".length)
        val authority = rest.substringBefore('/').substringBefore('?').substringBefore('#')
        if (WEB_HOSTS.none { authority.equals(it, ignoreCase = true) }) return null
        val path = rest.substring(authority.length).substringBefore('?').substringBefore('#')
        return path.split('/').filter { it.isNotEmpty() }
    }

    private fun String.isUuid(): Boolean = UUID.matches(this)

    /** RFC 3986 percent-encoding of everything but the unreserved set, over UTF-8. */
    private fun String.percentEncoded(): String = buildString {
        for (byte in this@percentEncoded.encodeToByteArray()) {
            val code = byte.toInt() and BYTE_MASK
            val char = code.toChar()
            if (char.isUnreserved()) {
                append(char)
            } else {
                append('%').append(HEX[code shr NIBBLE]).append(HEX[code and NIBBLE_MASK])
            }
        }
    }

    private fun Char.isUnreserved(): Boolean =
        this in 'a'..'z' || this in 'A'..'Z' || this in '0'..'9' || this in UNRESERVED_MARKS

    private const val PROFILE_SEGMENT = "u"
    private const val UNRESERVED_MARKS = "-._~"
    private const val HEX = "0123456789ABCDEF"
    private const val BYTE_MASK = 0xFF
    private const val NIBBLE = 4
    private const val NIBBLE_MASK = 0x0F

    private const val POST_SEGMENTS = 2
    private val UUID = Regex("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
}
