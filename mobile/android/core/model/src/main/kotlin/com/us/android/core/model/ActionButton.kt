package com.us.android.core.model

import java.net.IDN
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * The Action button (contract pinned 23 Oct 2026, action-button-contract.md):
 * one fixed label and an https link on a post, a reel, a long video or a
 * profile. Pure, so every rule is a table test; the web's
 * `features/action-button/model.ts` and `links.ts` hold the same rules and
 * the same words, and the two clients must keep agreeing.
 *
 * Read shape on post rows, channels and profiles, when present and live:
 * `{id, label, label_text, domain, href: "/l/<id>"}`. The author also gets
 * `url` and, when the link is not live, `status`. Viewers are only ever
 * handed an [ActionStatus.ACTIVE] button and never the raw url: a tap goes
 * through `GET /v1/links/:id` (which counts the click) and the leaving page.
 */
data class PostAction(
    val id: String,
    /** The wire value, `apply_now`. */
    val label: String,
    /** The words on the button, `Apply now`. */
    val labelText: String,
    /** Where it goes, for the pill: `epfindia.gov.in`. */
    val domain: String,
    /** Our redirect path, `/l/<id>`. Never the raw url. */
    val href: String,
    val status: ActionStatus = ActionStatus.ACTIVE,
    /** The raw link: only on the author's own copy, for editing. */
    val url: String? = null,
    /**
     * Why a button is not live, on the author's copy only: `unsafe` (the
     * safety check), `moderator` (our team) or `reports` (hidden while
     * reports are reviewed); null when live or not said.
     */
    val statusReason: String? = null,
) {
    /**
     * The author's own copy: the server sends the raw `url` to the author
     * alone (contract: "The raw url is included only for the author"), so a
     * row that carries one is the viewer's own button.
     */
    val isAuthorCopy: Boolean get() = url != null

    /** The pill's words: `Apply now · epfindia.gov.in`. */
    val pillText: String get() = if (domain.isNotEmpty()) "$labelText · $domain" else labelText

    companion object {
        /**
         * The wire's fields → a button, or null (no button) when the id or
         * the label is missing. Go sends `""` for an empty field, so blank is
         * absent. `href` is the server's only when it is one of our `/l/`
         * paths; anything else is rebuilt from the id, so a raw url can never
         * become a link target.
         */
        @Suppress("LongParameterList") // One parameter per wire field.
        fun fromWire(
            id: String?,
            label: String?,
            labelText: String?,
            domain: String?,
            href: String?,
            status: String?,
            url: String?,
            statusReason: String? = null,
        ): PostAction? {
            val cleanId = id?.trim().orEmpty()
            val cleanLabel = label?.trim().orEmpty()
            if (cleanId.isEmpty() || cleanLabel.isEmpty()) return null
            val serverHref = href?.trim().orEmpty()
            val cleanUrl = url?.trim()?.takeIf { it.isNotEmpty() }
            return PostAction(
                id = cleanId,
                label = cleanLabel,
                labelText = labelText?.trim()?.takeIf { it.isNotEmpty() } ?: ActionButtons.labelText(cleanLabel),
                domain = domain?.trim()?.lowercase()?.removePrefix("www.")?.takeIf { it.isNotEmpty() }
                    ?: cleanUrl?.let { ActionButtons.hostOf(it) }?.removePrefix("www.").orEmpty(),
                href = if (OUR_HREF.matches(serverHref)) serverHref else "/l/${percentEncode(cleanId)}",
                status = ActionStatus.fromWire(status),
                url = cleanUrl,
                statusReason = statusReason?.trim()?.lowercase()?.takeIf { it.isNotEmpty() },
            )
        }

        private val OUR_HREF = Regex("^/l/[^/?#\\s]+$")
    }
}

/** Where a saved link stands. Viewers only ever see [ACTIVE]. */
enum class ActionStatus(val wire: String) {
    ACTIVE("active"),

    /** A safety check flagged it, or it was removed: hidden for viewers. */
    BLOCKED("blocked"),

    /** Reported by several people: hidden for viewers until a moderator decides. */
    UNDER_REVIEW("under_review"),
    ;

    companion object {
        /** Blank is live; blocked, removed or unsafe is blocked; anything else is not live (fail closed). */
        fun fromWire(raw: String?): ActionStatus = when (raw?.trim()?.lowercase().orEmpty()) {
            "", "active" -> ACTIVE
            "blocked", "removed", "unsafe" -> BLOCKED
            else -> UNDER_REVIEW
        }
    }
}

/** One choice in the label picker: the wire value and its words. */
data class ActionLabel(val value: String, val label: String)

/** What the editor holds: a label and the link as typed. A null draft is "no button". */
data class ActionDraft(val label: String, val url: String)

/** The PATCH's `action`: left off, an explicit null, or the new `{label, url}`. */
sealed interface ActionPatch {
    /** Leave the key off: the button stays as it is. */
    data object Unchanged : ActionPatch

    /** `"action": null`: the button is removed. */
    data object Remove : ActionPatch

    /** `"action": {label, url}`: added or changed. */
    data class Set(val label: String, val url: String) : ActionPatch
}

/** The server's refusals of a button, by code. */
enum class ActionErrorCode {
    INVALID_ACTION_LABEL,
    INVALID_ACTION_URL,
    ACTION_LINK_SHORTENER,
    ACTION_LINK_GROUP_INVITE,
    ACTION_LINK_UNSAFE,
    ACTION_LINK_CHECK_UNAVAILABLE,
    ACTION_NOT_ELIGIBLE,
    ;

    companion object {
        fun fromWire(code: String?): ActionErrorCode? = entries.firstOrNull { it.name == code?.trim() }
    }
}

/** A refusal in our words; [missing] is ACTION_NOT_ELIGIBLE's `details.missing`, as sent. */
data class ActionProblem(
    val code: ActionErrorCode,
    val message: String,
    val missing: List<String> = emptyList(),
    /** ACTION_NOT_ELIGIBLE on an account too new: when it may add one (RFC 3339), as sent. */
    val eligibleAt: String? = null,
)

/** The link as the server will take it ([Ok]), or why it will not ([Bad]). */
sealed interface UrlCheck {
    data class Ok(val url: String, val domain: String) : UrlCheck
    data class Bad(val code: ActionErrorCode, val message: String) : UrlCheck
}

/** The author's plain explanation of a button that is not live. */
data class ActionStatusNote(val title: String, val detail: String)

/** Taps on a button, from `GET /v1/posts/:id/action/stats` (author only). */
data class ActionStats(val clicksTotal: Long = 0, val clicks7d: Long = 0) {
    /** `1 tap`, `12 taps · 3 this week`. */
    val tapsText: String
        get() {
            val total = "${group(clicksTotal)} ${if (clicksTotal == 1L) "tap" else "taps"}"
            return if (clicks7d > 0) "$total · ${group(clicks7d)} this week" else total
        }

    /** `1,23,456`: the web's `toLocaleString("en-IN")` (the last three digits, then pairs). */
    private fun group(n: Long): String {
        val digits = n.coerceAtLeast(0).toString()
        if (digits.length <= LAST_GROUP) return digits
        val head = digits.dropLast(LAST_GROUP)
        val pairs = head.reversed().chunked(2).joinToString(",").reversed()
        return "$pairs,${digits.takeLast(LAST_GROUP)}"
    }

    private companion object {
        const val LAST_GROUP = 3
    }
}

/**
 * The labels, the URL rules the server enforces (mirrored for instant
 * feedback; the safety lookup is the server's alone), the error words, and
 * the request builders. Every sentence matches the web's model.ts.
 */
@Suppress("TooManyFunctions") // One pure function per rule: the table the tests pin.
object ActionButtons {

    /** The contract's fixed list, in its order: what the picker shows when the server's list cannot be read. */
    val LABELS: List<ActionLabel> = listOf(
        ActionLabel("apply_now", "Apply now"),
        ActionLabel("learn_more", "Learn more"),
        ActionLabel("sign_up", "Sign up"),
        ActionLabel("register", "Register"),
        ActionLabel("book_now", "Book now"),
        ActionLabel("shop_now", "Shop now"),
        ActionLabel("order_now", "Order now"),
        ActionLabel("download", "Download"),
        ActionLabel("contact_us", "Contact us"),
        ActionLabel("visit_website", "Visit website"),
        ActionLabel("watch_more", "Watch more"),
    )

    const val MAX_URL_LENGTH = 500

    /** Link shorteners: the server answers ACTION_LINK_SHORTENER. A subdomain of one counts too. */
    val SHORTENER_HOSTS: List<String> = listOf(
        "bit.ly",
        "tinyurl.com",
        "t.co",
        "goo.gl",
        "cutt.ly",
        "rb.gy",
        "is.gd",
        "ow.ly",
        "shorturl.at",
        "tiny.cc",
        "rebrand.ly",
        "s.id",
        "t.ly",
        "bl.ink",
        "buff.ly",
    )

    // ---- labels ----

    /**
     * `GET /v1/posts/action-labels` rows → the picker's options. Rows without
     * a value are dropped (and repeats), a row without words takes the
     * fallback's. Nothing usable → the contract's list.
     */
    fun labelsFrom(rows: List<Pair<String?, String?>>): List<ActionLabel> {
        val out = ArrayList<ActionLabel>()
        val seen = HashSet<String>()
        rows.forEach { (value, label) ->
            val v = value?.trim().orEmpty()
            if (v.isEmpty() || !seen.add(v)) return@forEach
            out += ActionLabel(v, label?.trim()?.takeIf { it.isNotEmpty() } ?: labelText(v))
        }
        return out.ifEmpty { LABELS }
    }

    /** `apply_now` → `Apply now`; an unknown value reads as words rather than as a code. */
    fun labelText(value: String, labels: List<ActionLabel> = LABELS): String {
        val known = labels.firstOrNull { it.value == value } ?: LABELS.firstOrNull { it.value == value }
        if (known != null) return known.label
        val words = value.replace(Regex("[_-]+"), " ").trim()
        return words.replaceFirstChar { it.uppercaseChar() }
    }

    // ---- URL rules ----

    /**
     * The link as the server will take it, or why it will not: https only,
     * at most 500 characters, a real host (no IP literal, no localhost, no
     * `user@`), port 443 or none, no link shortener, no group invite.
     */
    @Suppress("ReturnCount", "CyclomaticComplexMethod") // One early return per rule, in the web's order.
    fun checkUrl(raw: String): UrlCheck {
        val value = raw.trim()
        if (value.isEmpty()) return bad(ActionErrorCode.INVALID_ACTION_URL, "Add the link people should open.")
        if (value.codePointCount(0, value.length) > MAX_URL_LENGTH) {
            return bad(ActionErrorCode.INVALID_ACTION_URL, "A link can be at most $MAX_URL_LENGTH characters.")
        }
        if (value.any { it.isSpaceOrControl() }) return bad(ActionErrorCode.INVALID_ACTION_URL, "A link can't have spaces in it.")
        if (!value.startsWith(HTTPS, ignoreCase = true)) return bad(ActionErrorCode.INVALID_ACTION_URL, NOT_HTTPS)
        val url = parseHttps(value)
            ?: return bad(ActionErrorCode.INVALID_ACTION_URL, "That isn't a link we can open. Check it and try again.")
        if (url.hasUserInfo) return bad(ActionErrorCode.INVALID_ACTION_URL, "Remove the name and @ before the website's address.")
        val host = url.host
        if (host.startsWith("[") || isNumericHost(host)) {
            return bad(ActionErrorCode.INVALID_ACTION_URL, "Use the website's name, not a number address.")
        }
        if (host == "localhost" || host.endsWith(".localhost")) {
            return bad(ActionErrorCode.INVALID_ACTION_URL, "That link only works on your own computer.")
        }
        if ('.' !in host) return bad(ActionErrorCode.INVALID_ACTION_URL, "That link has no website in it.")
        if (url.port != null && url.port != HTTPS_PORT) {
            return bad(ActionErrorCode.INVALID_ACTION_URL, "Use a normal https:// link, without a port number.")
        }
        if (SHORTENER_HOSTS.any { hostMatches(host, it) }) {
            return bad(ActionErrorCode.ACTION_LINK_SHORTENER, errorMessage(ActionErrorCode.ACTION_LINK_SHORTENER))
        }
        if (isGroupInvite(host, url.path)) {
            return bad(ActionErrorCode.ACTION_LINK_GROUP_INVITE, errorMessage(ActionErrorCode.ACTION_LINK_GROUP_INVITE))
        }
        return UrlCheck.Ok(url = value, domain = host.removePrefix("www."))
    }

    /** A WhatsApp or Telegram group invite (wa.me/<number> and other t.me links are fine). */
    fun isGroupInvite(host: String, path: String): Boolean {
        val p = (percentDecode(path) ?: path).lowercase()
        return when {
            hostMatches(host, "chat.whatsapp.com") -> true
            host == "t.me" || host == "www.t.me" -> p.startsWith("/+") || p.startsWith("/joinchat")
            host == "telegram.me" || host == "www.telegram.me" -> p.startsWith("/joinchat")
            else -> false
        }
    }

    /** The lower-case host of an https link (IDN as punycode, no trailing dot), or null when it is not one. */
    fun hostOf(url: String): String? = parseHttps(url.trim())?.host?.takeIf { it.isNotEmpty() }

    // ---- errors, in our words ----

    /**
     * One sentence per missing item, in the server's order, each once. When
     * the account is too new and the server said from when it may add a
     * button ([eligibleAt], `details.eligible_at`), the age item names that
     * day: "You can add an action button from 30 Oct 2026."
     */
    fun missingSentences(missing: List<String>, eligibleAt: String? = null, zone: ZoneId = ZoneId.systemDefault()): List<String> {
        val from = eligibleDay(eligibleAt, zone)?.let { "You can add an action button from $it." }
        val out = ArrayList<String>()
        missing.forEach { item ->
            val key = item.trim().lowercase()
            if (key.isEmpty()) return@forEach
            val words = key.replace(Regex("[_-]+"), " ")
            val known = MISSING_COPY[key]
            val sentence = when {
                known == AGE && from != null -> from
                known != null -> known
                else -> "${words.replaceFirstChar { it.uppercaseChar() }}."
            }
            if (sentence !in out) out += sentence
        }
        return out
    }

    /** The sentence for a refusal; ACTION_NOT_ELIGIBLE names what is missing (and from when) when it is known. */
    fun errorMessage(
        code: ActionErrorCode,
        missing: List<String> = emptyList(),
        eligibleAt: String? = null,
        zone: ZoneId = ZoneId.systemDefault(),
    ): String {
        if (code != ActionErrorCode.ACTION_NOT_ELIGIBLE) return ERROR_COPY.getValue(code)
        val items = missingSentences(missing, eligibleAt, zone)
            .ifEmpty { listOfNotNull(eligibleDay(eligibleAt, zone)?.let { "You can add an action button from $it." }) }
        val head = ERROR_COPY.getValue(ActionErrorCode.ACTION_NOT_ELIGIBLE)
        return if (items.isNotEmpty()) {
            "$head ${items.joinToString(" ")}"
        } else {
            "$head Accounts need to be 7 days old, with a verified email and no active strikes."
        }
    }

    /** A server code → the refusal in our words, or null when the code is not the button's. */
    fun problemOf(
        code: String?,
        missing: List<String> = emptyList(),
        eligibleAt: String? = null,
        zone: ZoneId = ZoneId.systemDefault(),
    ): ActionProblem? {
        val known = ActionErrorCode.fromWire(code) ?: return null
        val notEligible = known == ActionErrorCode.ACTION_NOT_ELIGIBLE
        val items = if (notEligible) missing.map { it.trim() }.filter { it.isNotEmpty() } else emptyList()
        val from = eligibleAt?.trim()?.takeIf { notEligible && it.isNotEmpty() }
        return ActionProblem(known, errorMessage(known, items, from, zone), items, from)
    }

    /** `2026-10-30T09:00:00Z` → `30 Oct 2026` in [zone]; null for anything that is not an instant. */
    fun eligibleDay(eligibleAt: String?, zone: ZoneId = ZoneId.systemDefault()): String? {
        val raw = eligibleAt?.trim().orEmpty()
        if (raw.isEmpty()) return null
        return runCatching {
            OffsetDateTime.parse(raw).atZoneSameInstant(zone).format(DateTimeFormatter.ofPattern("d MMM yyyy", Locale.ENGLISH))
        }.getOrNull()
    }

    // ---- what is shown ----

    /**
     * What a viewer gets: only a live button, and never its raw url; the
     * author sees theirs whatever its status, url included. A locked
     * (members-only) item shows no button to anyone.
     */
    fun visible(action: PostAction?, isAuthor: Boolean, locked: Boolean = false): PostAction? = when {
        action == null || locked -> null
        isAuthor -> action
        action.status == ActionStatus.ACTIVE -> action.copy(url = null)
        else -> null
    }

    /**
     * The author's explanation of a button that is not live; null when it
     * is. The server's `status_reason` says why when it is known: `unsafe`
     * (the safety check), `moderator` (our team removed it), `reports`
     * (hidden while reports are reviewed); without one, the status decides.
     */
    fun statusNote(status: ActionStatus?, reason: String? = null): ActionStatusNote? {
        if (status == null || status == ActionStatus.ACTIVE) return null
        return when (reason?.trim()?.lowercase()) {
            REASON_UNSAFE -> UNSAFE_NOTE
            REASON_MODERATOR -> MODERATOR_NOTE
            REASON_REPORTS -> REPORTS_NOTE
            else -> if (status == ActionStatus.BLOCKED) UNSAFE_NOTE else REPORTS_NOTE
        }
    }

    /** [action]'s own note: its status and its reason. */
    fun statusNote(action: PostAction?): ActionStatusNote? = action?.let { statusNote(it.status, it.statusReason) }

    const val REASON_UNSAFE = "unsafe"
    const val REASON_MODERATOR = "moderator"
    const val REASON_REPORTS = "reports"

    private val UNSAFE_NOTE = ActionStatusNote(
        "Link blocked: unsafe",
        "A safety check flagged this link, so viewers don't see the button. Change the link or remove the button.",
    )
    private val MODERATOR_NOTE = ActionStatusNote(
        "Removed by our team",
        "Our team removed this link, so viewers don't see the button. Change the link or remove the button.",
    )
    private val REPORTS_NOTE = ActionStatusNote(
        "Hidden while we review reports",
        "People reported this link, so viewers don't see the button while our team checks it.",
    )

    // ---- the editor's draft and the requests ----

    /** The editor's starting point for a saved button (the author's copy carries the url). */
    fun draftFrom(action: PostAction?): ActionDraft? = action?.let { ActionDraft(it.label, it.url.orEmpty()) }

    /**
     * Why the draft can't be sent, or null. No button is never a problem.
     * With [labels], the label must be one of them.
     */
    fun draftProblem(draft: ActionDraft?, labels: List<ActionLabel>? = null): String? {
        if (draft == null) return null
        if (draft.label.isBlank() || (labels != null && labels.none { it.value == draft.label })) {
            return errorMessage(ActionErrorCode.INVALID_ACTION_LABEL)
        }
        return (checkUrl(draft.url) as? UrlCheck.Bad)?.message
    }

    /** The create body's action: the trimmed `{label, url}` when there is a button, null (left off) otherwise. */
    fun createWrite(draft: ActionDraft?): ActionDraft? = draft?.let { ActionDraft(it.label, it.url.trim()) }

    /**
     * The PATCH's action: [ActionPatch.Unchanged] when nothing moved (the key
     * is left off), [ActionPatch.Remove] when the saved button was taken off,
     * [ActionPatch.Set] when one was added or changed. [before] is the saved
     * button (from the author's copy), [after] what the editor holds now.
     */
    fun patch(before: ActionDraft?, after: ActionDraft?): ActionPatch {
        if (after == null) return if (before != null) ActionPatch.Remove else ActionPatch.Unchanged
        val nextUrl = after.url.trim()
        if (before != null && before.label == after.label && before.url.trim() == nextUrl) return ActionPatch.Unchanged
        return ActionPatch.Set(after.label, nextUrl)
    }

    // ---- internals ----

    private const val HTTPS = "https://"
    private const val HTTPS_PORT = 443
    private const val MAX_PORT = 65_535
    private const val NOT_HTTPS = "Use a full link that starts with https://"
    private const val STRIKE = "You can't have an active strike on your account."
    private const val AGE = "Your account must be at least 7 days old."
    private const val EMAIL = "Verify your email address."

    /** The server's names are pinned (account_age, email_verified, no_active_strikes); the common spellings read too. */
    private val MISSING_COPY: Map<String, String> = mapOf(
        "account_age" to AGE,
        "account_too_new" to AGE,
        "min_account_age" to AGE,
        "account_age_7d" to AGE,
        "email_verified" to EMAIL,
        "verified_email" to EMAIL,
        "email" to EMAIL,
        "email_verification" to EMAIL,
        "no_active_strikes" to STRIKE,
        "active_strikes" to STRIKE,
        "strikes" to STRIKE,
        "standing" to STRIKE,
        "good_standing" to STRIKE,
    )

    private val ERROR_COPY: Map<ActionErrorCode, String> = mapOf(
        ActionErrorCode.INVALID_ACTION_LABEL to "Choose what the button says from the list.",
        ActionErrorCode.INVALID_ACTION_URL to "Use a full https:// link to a website.",
        ActionErrorCode.ACTION_LINK_SHORTENER to "Short links like bit.ly hide where they go. Use the full link instead.",
        ActionErrorCode.ACTION_LINK_GROUP_INVITE to
            "WhatsApp and Telegram group invites can't be used. A wa.me link to a number is fine.",
        ActionErrorCode.ACTION_LINK_UNSAFE to "That link was flagged as unsafe, so it can't be used.",
        ActionErrorCode.ACTION_LINK_CHECK_UNAVAILABLE to "We couldn't check that link right now. Try again in a few minutes.",
        ActionErrorCode.ACTION_NOT_ELIGIBLE to "You can't add an action button yet.",
    )

    private fun bad(code: ActionErrorCode, message: String) = UrlCheck.Bad(code, message)

    private fun hostMatches(host: String, domain: String): Boolean = host == domain || host.endsWith(".$domain")

    /** JS `\s` plus the C0 and C1 controls: what the web's check refuses. */
    private fun Char.isSpaceOrControl(): Boolean =
        isWhitespace() || code <= C0_END || code in C1_START..C1_END || this == ' ' || this == '﻿'

    private const val C0_END = 0x1f
    private const val C1_START = 0x7f
    private const val C1_END = 0x9f

    /**
     * An IP literal as a browser would read it: the last label is a number
     * (decimal, or `0x` hex), so `127.0.0.1`, `0x7f.1` and `2130706433` all
     * count. A real top-level domain is never numeric.
     */
    private fun isNumericHost(host: String): Boolean {
        val last = host.substringAfterLast('.')
        if (last.isEmpty()) return false
        if (last.all { it.isDigit() }) return true
        return last.startsWith("0x") && last.drop(2).all { it.isDigit() || it in 'a'..'f' }
    }

    private class ParsedUrl(val host: String, val port: Int?, val path: String, val hasUserInfo: Boolean)

    /**
     * The parts of an https link the rules read, the way a browser (WHATWG
     * URL) splits it: extra slashes after the scheme are skipped, `\` is a
     * slash, the authority ends at `/ \ ? #`, the LAST `@` ends the user
     * info, the host is percent-decoded, mapped to ASCII (IDN) and lower
     * case, a default port is no port. Null where a browser would refuse
     * the link.
     */
    @Suppress("ReturnCount")
    private fun parseHttps(value: String): ParsedUrl? {
        if (!value.startsWith(HTTPS, ignoreCase = true)) return null
        val rest = value.substring(HTTPS.length).trimStart('/', '\\')
        val end = rest.indexOfFirst { it == '/' || it == '\\' || it == '?' || it == '#' }.let { if (it < 0) rest.length else it }
        val authority = rest.substring(0, end)
        val tail = rest.substring(end)
        val at = authority.lastIndexOf('@')
        val hostPort = authority.substring(at + 1)
        val (rawHost, rawPort) = splitHostPort(hostPort) ?: return null
        val port = when {
            rawPort.isEmpty() -> null
            rawPort.all { it.isDigit() } -> rawPort.trimStart('0').ifEmpty { "0" }.toIntOrNull()?.takeIf { it <= MAX_PORT } ?: return null
            else -> return null
        }
        val host = asciiHost(rawHost) ?: return null
        val path = tail.substringBefore('?').substringBefore('#').replace('\\', '/')
        return ParsedUrl(host = host, port = port?.takeIf { it != HTTPS_PORT }, path = removeDotSegments(path), hasUserInfo = at >= 0)
    }

    private fun splitHostPort(hostPort: String): Pair<String, String>? {
        if (hostPort.startsWith("[")) {
            val close = hostPort.indexOf(']')
            if (close < 0) return null
            val after = hostPort.substring(close + 1)
            if (after.isNotEmpty() && !after.startsWith(":")) return null
            return hostPort.substring(0, close + 1) to after.removePrefix(":")
        }
        val colon = hostPort.lastIndexOf(':')
        return if (colon < 0) hostPort to "" else hostPort.substring(0, colon) to hostPort.substring(colon + 1)
    }

    @Suppress("ReturnCount")
    private fun asciiHost(raw: String): String? {
        if (raw.startsWith("[")) return raw.lowercase()
        val decoded = percentDecode(raw) ?: return null
        if (decoded.isEmpty() || decoded.any { it in FORBIDDEN_HOST_CHARS || it.isSpaceOrControl() }) return null
        val ascii = runCatching { IDN.toASCII(decoded, IDN.ALLOW_UNASSIGNED) }.getOrNull() ?: return null
        return ascii.lowercase().trimEnd('.').ifEmpty { null }
    }

    private const val FORBIDDEN_HOST_CHARS = "#%/:<>?@[\\]^|"

    /** `/a/./b/../+x` → `/a/+x`, as a browser normalises a path (`%2e` counts as `.`). */
    private fun removeDotSegments(path: String): String {
        if (path.isEmpty()) return path
        val out = ArrayList<String>()
        val segments = path.split('/')
        segments.forEachIndexed { index, segment ->
            if (index == 0) return@forEachIndexed
            when (segment.lowercase().replace("%2e", ".")) {
                "." -> if (index == segments.lastIndex) out += ""
                ".." -> {
                    if (out.isNotEmpty()) out.removeAt(out.lastIndex)
                    if (index == segments.lastIndex) out += ""
                }
                else -> out += segment
            }
        }
        return "/" + out.joinToString("/")
    }
}

/**
 * Where a tap on a button lands (`GET /v1/links/:id` → `{id, url, domain,
 * label, status}`), the per-person memory of domains already shown the
 * "You're leaving" page, and the choice between that page and opening at
 * once. The memory is a most-recent-last list capped at [MAX_SEEN_DOMAINS]
 * — the oldest fall off first, exactly as the web's localStorage list.
 */
object ActionLinks {

    const val MAX_SEEN_DOMAINS = 500

    /** A target we may open. */
    data class Target(val id: String, val url: String, val domain: String, val label: String)

    /** What a tap does: open at once (a domain seen before), or show the leaving page first. */
    sealed interface Visit {
        data class Open(val url: String) : Visit
        data class Interstitial(val url: String, val domain: String) : Visit
    }

    /** Only an https link with a host is ever opened (the server takes nothing else); anything else is no link. */
    fun safeTarget(url: String?): String? {
        val value = url?.trim().orEmpty()
        if (value.isEmpty() || value.any { it.isWhitespace() || it.code < ' '.code }) return null
        return value.takeIf { ActionButtons.hostOf(it) != null }
    }

    /** The host as the memory keys it: lower case, no trailing dot, no `www.`. */
    fun normalizeDomain(domain: String): String = domain.trim().lowercase().trimEnd('.').removePrefix("www.")

    /**
     * The links route's fields → the target, or null when there is nothing
     * to open (no id, no https url, or a status that is not active). The
     * domain is the url's OWN host — where the browser will actually go —
     * not the `domain` field beside it (clarification 7).
     */
    fun targetOf(id: String?, url: String?, label: String?, status: String?): Target? {
        val cleanId = id?.trim().orEmpty()
        val safe = safeTarget(url) ?: return null
        if (cleanId.isEmpty()) return null
        val s = status?.trim()?.lowercase().orEmpty().ifEmpty { "active" }
        if (s != "active") return null
        val host = ActionButtons.hostOf(safe) ?: return null
        return Target(id = cleanId, url = safe, domain = normalizeDomain(host), label = label?.trim().orEmpty())
    }

    /** Whether [domain] was continued to before. */
    fun hasSeen(seen: List<String>, domain: String): Boolean {
        val d = normalizeDomain(domain)
        return d.isNotEmpty() && d in seen
    }

    /** [seen] with [domain] remembered as the most recent; the oldest drop off past [MAX_SEEN_DOMAINS]. */
    fun remember(seen: List<String>, domain: String): List<String> {
        val d = normalizeDomain(domain)
        if (d.isEmpty()) return seen
        return (seen.filter { it != d } + d).takeLast(MAX_SEEN_DOMAINS)
    }

    /** A domain seen before opens at once; a new one gets the leaving page. */
    fun decide(target: Target, seen: List<String>): Visit =
        if (hasSeen(seen, target.domain)) Visit.Open(target.url) else Visit.Interstitial(target.url, target.domain)
}

private const val HEX_DIGITS = "0123456789ABCDEF"
private const val BYTE_MASK = 0xFF
private const val NIBBLE = 4
private const val NIBBLE_MASK = 0x0F
private const val ESCAPE_LENGTH = 3

/** RFC 3986 percent-encoding of everything outside the unreserved set, over UTF-8. */
private fun percentEncode(value: String): String = buildString {
    value.encodeToByteArray().forEach { byte ->
        val code = byte.toInt() and BYTE_MASK
        val c = code.toChar()
        if (c in 'a'..'z' || c in 'A'..'Z' || c in '0'..'9' || c in "-._~") {
            append(c)
        } else {
            append('%').append(HEX_DIGITS[code shr NIBBLE]).append(HEX_DIGITS[code and NIBBLE_MASK])
        }
    }
}

/** `%XX` escapes over UTF-8; null for a malformed one. */
@Suppress("ReturnCount")
private fun percentDecode(value: String): String? {
    if ('%' !in value) return value
    val bytes = ArrayList<Byte>(value.length)
    var i = 0
    while (i < value.length) {
        val c = value[i]
        if (c == '%') {
            if (i + 2 >= value.length) return null
            val high = HEX_DIGITS.indexOf(value[i + 1].uppercaseChar())
            val low = HEX_DIGITS.indexOf(value[i + 2].uppercaseChar())
            if (high < 0 || low < 0) return null
            bytes += ((high shl NIBBLE) or low).toByte()
            i += ESCAPE_LENGTH
        } else {
            c.toString().encodeToByteArray().forEach { bytes += it }
            i++
        }
    }
    return runCatching { bytes.toByteArray().decodeToString(throwOnInvalidSequence = true) }.getOrNull()
}
