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

    private const val POST_SEGMENTS = 2
    private val UUID = Regex("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
}
