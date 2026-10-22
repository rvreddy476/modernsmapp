package com.us.android.core.model

import kotlin.random.Random

/**
 * The app's public web addresses, in ONE place: what "Copy link" and Share
 * hand out, and what the manifest's App Links route back into the app.
 *
 * A post's address is the web client's public permalink, `https://<host>/post/<id>` — the route
 * postbook-ui serves at `src/app/post/[postId]`. It replaced `/p/<id>`,
 * which no client served, so every copied link was a 404.
 *
 * Since 21 Oct 2026 (founder: a shared link tapped on a phone must land
 * well) the web serves a preview on every share route and the app opens
 * each one on the exact item — see [targetOf] for the routes.
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

    /**
     * Settings › Support & about (2026-10-17): the web app's public pages for
     * reporting a problem, the terms and the privacy policy, the same three
     * the web settings hub links to.
     */
    const val GRIEVANCE_URL = "$ORIGIN/grievance"
    const val TERMS_URL = "$ORIGIN/terms"
    const val PRIVACY_POLICY_URL = "$ORIGIN/privacy"

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
     * Parsed back by [targetOf] as [WebLinkTarget.Profile] (2026-10-22).
     */
    fun profileUrl(username: String?): String? {
        val handle = username?.trim()?.removePrefix("@")?.trim().orEmpty()
        if (handle.isEmpty()) return null
        return "$ORIGIN/$PROFILE_SEGMENT/${handle.percentEncoded()}"
    }

    /**
     * `https://cleestudio.com/live/<streamId>` — a live stream's room on the
     * web, the route postbook-ui serves at `src/app/live/[streamId]`. Share
     * in the Android live room hands this out (T-30, 2026-10-15).
     *
     * Surrounding space is dropped and the id is percent-encoded like a
     * handle, so it can never add a path segment, a query or a fragment.
     * Null for a blank id. Not parsed back: there is no `/live/` App Link,
     * so a tapped link opens the web page.
     */
    fun liveUrl(streamId: String?): String? {
        val id = streamId?.trim().orEmpty()
        if (id.isEmpty()) return null
        return "$ORIGIN/$LIVE_SEGMENT/${id.percentEncoded()}"
    }

    // ---- share refs (web `features/share/shareRef.ts`, 21 Oct 2026) ----

    const val SHARE_REF_PARAM = "ref"
    const val SHARE_SOURCE_PARAM = "utm_source"
    const val SHARE_SOURCE = "share"

    /**
     * A short random id (10 chars, lower-case base36), minted when a link is
     * shared. It names nothing about the sharer: it is only something the
     * Install Referrer can carry back, so a tap that installs the app can
     * land on the page it was tapped for.
     */
    fun newShareRef(random: Random = Random.Default): String = buildString {
        repeat(SHARE_REF_LENGTH) { append(SHARE_REF_ALPHABET[random.nextInt(SHARE_REF_ALPHABET.length)]) }
    }

    /** Whether a ref is one of ours: 4–32 chars of `[a-z0-9]`. Anything else is dropped. */
    fun isShareRef(ref: String?): Boolean = ref != null && SHARE_REF.matches(ref)

    /**
     * [url] with `?ref=<id>&utm_source=share` appended, the way every link
     * the web hands out ends (a fragment, if any, is kept after). A url that
     * already carries a valid ref keeps it; other query parameters are kept
     * too. An invalid ref, or an empty url, leaves the url as it is.
     */
    fun withShareRef(url: String, ref: String = newShareRef()): String {
        if (url.isEmpty() || !isShareRef(ref)) return url
        val hashAt = url.indexOf('#')
        val base = if (hashAt >= 0) url.substring(0, hashAt) else url
        val hash = if (hashAt >= 0) url.substring(hashAt) else ""
        val queryAt = base.indexOf('?')
        val path = if (queryAt >= 0) base.substring(0, queryAt) else base
        val params = LinkedHashMap<String, String>()
        if (queryAt >= 0) {
            base.substring(queryAt + 1).split('&').filter { it.isNotEmpty() }.forEach { pair ->
                params[pair.substringBefore('=')] = pair.substringAfter('=', missingDelimiterValue = "")
            }
        }
        if (!isShareRef(params[SHARE_REF_PARAM])) params[SHARE_REF_PARAM] = ref
        params[SHARE_SOURCE_PARAM] = SHARE_SOURCE
        val query = params.entries.joinToString("&") { (key, value) -> if (value.isEmpty()) key else "$key=$value" }
        return "$path?$query$hash"
    }

    // ---- parsing ----

    /**
     * The post id in `https://cleestudio.com/post/<id>` (a trailing slash, query
     * and fragment ignored), or null for any other link: another host, plain
     * http, another path, extra segments, or an id that is not a UUID. Post
     * ids are UUIDs on the server (post-service parses every one with
     * `uuid.Parse`), so anything else could only ever open "not found".
     */
    fun postIdOf(link: String?): String? = (targetOf(link) as? WebLinkTarget.Post)?.postId

    /**
     * What a link on the web app opens in this app, or null for a link the
     * app does not open (another host or scheme, an unknown route, a bad id).
     * A trailing slash, the query (`?ref=…&utm_source=share`) and a fragment
     * are ignored on every route. The routes, as postbook-ui serves them:
     *
     *  - `/post/<uuid>`              → [WebLinkTarget.Post]
     *  - `/reels/<uuid>`             → [WebLinkTarget.Reel]
     *  - `/reels/sound/<id>`         → [WebLinkTarget.Sound]
     *  - `/posttube/watch/<uuid>`    → [WebLinkTarget.Watch]
     *  - `/u/<username>`             → [WebLinkTarget.Profile]
     *  - `/posttube/channel/<handle>`, `/channel/<handle>` → [WebLinkTarget.Channel]
     *  - `/hashtag/<tag>`            → [WebLinkTarget.Hashtag]
     *
     * `/shop/products/<id>` is deliberately absent: commerce is paused (21 Oct 2026).
     * A handle or tag is percent-decoded and then checked against the
     * characters a handle or tag may hold, so a decoded segment can never
     * carry a path or a query into a screen.
     */
    @Suppress("ReturnCount") // One early return per route.
    fun targetOf(link: String?): WebLinkTarget? {
        val segments = pathSegmentsOn(link) ?: return null
        when (segments.size) {
            TWO_SEGMENTS -> {
                val (head, tail) = segments
                return when (head) {
                    POST_SEGMENT -> tail.takeIf { it.isUuid() }?.let(WebLinkTarget::Post)
                    REELS_SEGMENT -> tail.takeIf { it.isUuid() }?.let(WebLinkTarget::Reel)
                    PROFILE_SEGMENT -> tail.asHandle()?.let(WebLinkTarget::Profile)
                    CHANNEL_SEGMENT -> tail.asHandle()?.let(WebLinkTarget::Channel)
                    HASHTAG_SEGMENT -> tail.asHashtag()?.let(WebLinkTarget::Hashtag)
                    else -> null
                }
            }
            THREE_SEGMENTS -> {
                val (head, middle, tail) = segments
                return when {
                    head == REELS_SEGMENT && middle == SOUND_SEGMENT -> tail.asSoundId()?.let(WebLinkTarget::Sound)
                    head == POSTTUBE_SEGMENT && middle == WATCH_SEGMENT -> tail.takeIf { it.isUuid() }?.let(WebLinkTarget::Watch)
                    head == POSTTUBE_SEGMENT && middle == CHANNEL_SEGMENT -> tail.asHandle()?.let(WebLinkTarget::Channel)
                    else -> null
                }
            }
            else -> return null
        }
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

    /** A username or channel handle: decoded, a leading `@` dropped, 1–64 of `[A-Za-z0-9._-]`. */
    private fun String.asHandle(): String? =
        percentDecoded()?.removePrefix("@")?.takeIf { HANDLE.matches(it) }

    /** A hashtag: decoded, a leading `#` dropped, 1–100 letters, digits, underscores or combining marks (any script). */
    private fun String.asHashtag(): String? =
        percentDecoded()?.removePrefix("#")
            ?.takeIf { tag -> tag.length in 1..HASHTAG_MAX && tag.all { it.isHashtagChar() } }

    /**
     * A letter, digit or underscore, or a combining mark: Indic scripts write
     * vowel signs and the virama as marks (`नमस्ते` holds two), so a tag in
     * them is letters AND marks.
     */
    private fun Char.isHashtagChar(): Boolean =
        isLetterOrDigit() || this == '_' ||
            category == CharCategory.NON_SPACING_MARK || category == CharCategory.COMBINING_SPACING_MARK

    /** A sound id (media-service): decoded, 1–64 of `[A-Za-z0-9._-]`. */
    private fun String.asSoundId(): String? = percentDecoded()?.takeIf { HANDLE.matches(it) }

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

    /** The inverse of [percentEncoded] over UTF-8; null for a malformed escape. `+` stays `+`. */
    private fun String.percentDecoded(): String? {
        if ('%' !in this) return this
        val bytes = ArrayList<Byte>(length)
        var i = 0
        while (i < length) {
            val char = this[i]
            if (char == '%') {
                if (i + 2 >= length) return null
                val high = HEX.indexOf(this[i + 1].uppercaseChar())
                val low = HEX.indexOf(this[i + 2].uppercaseChar())
                if (high < 0 || low < 0) return null
                bytes += ((high shl NIBBLE) or low).toByte()
                i += PERCENT_ESCAPE_LENGTH
            } else {
                for (byte in char.toString().encodeToByteArray()) bytes += byte
                i++
            }
        }
        return runCatching { bytes.toByteArray().decodeToString(throwOnInvalidSequence = true) }.getOrNull()
    }

    private fun Char.isUnreserved(): Boolean =
        this in 'a'..'z' || this in 'A'..'Z' || this in '0'..'9' || this in UNRESERVED_MARKS

    private const val PROFILE_SEGMENT = "u"
    private const val LIVE_SEGMENT = "live"
    private const val REELS_SEGMENT = "reels"
    private const val SOUND_SEGMENT = "sound"
    private const val POSTTUBE_SEGMENT = "posttube"
    private const val WATCH_SEGMENT = "watch"
    private const val CHANNEL_SEGMENT = "channel"
    private const val HASHTAG_SEGMENT = "hashtag"
    private const val UNRESERVED_MARKS = "-._~"
    private const val HEX = "0123456789ABCDEF"
    private const val BYTE_MASK = 0xFF
    private const val NIBBLE = 4
    private const val NIBBLE_MASK = 0x0F
    private const val PERCENT_ESCAPE_LENGTH = 3

    private const val TWO_SEGMENTS = 2
    private const val THREE_SEGMENTS = 3
    private const val HASHTAG_MAX = 100
    private const val SHARE_REF_LENGTH = 10
    private const val SHARE_REF_ALPHABET = "abcdefghijklmnopqrstuvwxyz0123456789"
    private val SHARE_REF = Regex("^[a-z0-9]{4,32}$")
    private val HANDLE = Regex("^[A-Za-z0-9._-]{1,64}$")
    private val UUID = Regex("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")
}

/** What a web link opens in the app — one case per route [WebLinks.targetOf] knows. */
sealed interface WebLinkTarget {
    /** `/post/<id>`: the post detail. */
    data class Post(val postId: String) : WebLinkTarget

    /** `/reels/<id>`: the Reels tab, on that reel. */
    data class Reel(val postId: String) : WebLinkTarget

    /** `/posttube/watch/<id>`: Tube's watch screen. */
    data class Watch(val postId: String) : WebLinkTarget

    /** `/u/<username>`: that person's profile (the username is resolved to an id first). */
    data class Profile(val username: String) : WebLinkTarget

    /** `/posttube/channel/<handle>` or `/channel/<handle>`: that channel (the handle is resolved first). */
    data class Channel(val handle: String) : WebLinkTarget

    /** `/reels/sound/<id>`: the sound page. */
    data class Sound(val soundId: String) : WebLinkTarget

    /** `/hashtag/<tag>`: the tag's posts. */
    data class Hashtag(val tag: String) : WebLinkTarget
}
