package com.us.android.feature.tube.data.hub

import com.us.android.core.feed.data.action.toPostAction
import com.us.android.core.feed.data.copyright.hasCopyrightHoldFlag
import com.us.android.core.feed.data.copyright.hasTitleReviewRestriction
import com.us.android.core.feed.data.copyright.toCopyrightReview
import com.us.android.core.model.CopyrightReview
import com.us.android.core.model.PostAction
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull
import java.math.BigDecimal
import java.math.RoundingMode
import java.time.LocalDate
import java.time.format.DateTimeParseException
import java.time.temporal.ChronoUnit

/*
  The Creator Hub's models and normalisers (T-31, 2026-10-15).

  Every function here is a PORT of the web Creator Hub's
  (`src/features/posttube/hub/hubApi.ts`, `hubModel.ts`, `DraftsTab.tsx`,
  `reels/model.ts formatCount`), field for field and fall-through for
  fall-through, so the app and the web show the same numbers for the same
  response (audit T-31 acceptance: "Android shows the same numbers as web").
  The tests replay the web's own fixtures and expectations.

  The rules the web follows, and so these follow:
   - A number is only a JSON number; a string "12" is 0, as `num()` does.
   - A string is only a JSON string; anything else is "".
   - An empty string is absent (Go zero values), so `a || b` falls through.
   - `firstNum` takes the first NON-ZERO of its keys, so a Go zero in the
     new field falls through to the older writer's name.
*/

// ── Periods ────────────────────────────────────────────────────────────

/** The overview's periods. The web also offers a year; the app asks for these three (T-31). */
enum class HubPeriod(val wire: String, val label: String) {
    DAYS_7("7d", "7 days"),
    DAYS_28("28d", "28 days"),
    DAYS_90("90d", "90 days"),
    ;

    /** The metric note under each total: "Last 7 days" (web `since`). */
    val since: String get() = "Last ${label.lowercase()}"

    companion object {
        /** The web's default period. */
        val DEFAULT = DAYS_28
    }
}

/** Every period the server and the web understand; a wire period outside this keeps the requested one. */
internal val INSIGHTS_PERIODS = setOf("7d", "28d", "90d", "365d")

// ── Creator insights ───────────────────────────────────────────────────

/** One calendar day of views, "YYYY-MM-DD". */
data class HubDayPoint(val day: String, val views: Long)

/** `GET /v1/analytics/creator/me`, normalised as the web's `normalizeCreatorInsights`. */
data class CreatorInsights(
    val period: String,
    val views: Long,
    val likes: Long,
    val comments: Long,
    val shares: Long,
    /** Follows made straight from a video in the period — NOT the net follower change. */
    val followsFromContent: Long,
    val watchTimeMs: Long,
    val uniqueViewers: Long,
    /** Null = not tracked yet (the wire sends null today). */
    val followersDelta: Long?,
    /** Views per day, oldest first, gaps filled with 0. Empty hides the chart. */
    val viewsByDay: List<HubDayPoint>,
)

/**
 * The analytics body, unwrapped the way the web does: `res.data.data ?? res.data`.
 * analytics-service answers with the bare stats object; an enveloped answer
 * (a gateway that wraps it) still reads.
 */
fun unwrapInsightsBody(body: JsonElement?): JsonElement? {
    val inner = (body as? JsonObject)?.get("data")
    return if (inner != null && inner !is JsonNull) inner else body
}

fun normalizeCreatorInsights(raw: JsonElement?, requested: HubPeriod): CreatorInsights {
    val d = raw as? JsonObject ?: JsonObject(emptyMap())
    val wirePeriod = str(d["period"])
    val daily = dayPoints(d["daily_stats"])
    return CreatorInsights(
        period = if (wirePeriod in INSIGHTS_PERIODS) wirePeriod else requested.wire,
        views = num(d["views"]).toLong(),
        likes = firstNum(d, "likes", "total_likes").toLong(),
        comments = firstNum(d, "comments", "total_comments").toLong(),
        shares = firstNum(d, "shares", "total_shares").toLong(),
        followsFromContent = firstNum(d, "followers_gained", "follower_growth").toLong(),
        watchTimeMs = num(d["watch_time_ms"]).toLong(),
        uniqueViewers = num(d["unique_viewers"]).toLong(),
        followersDelta = numberOrNull(d["followers_delta"])?.toLong()
            ?: numberOrNull(d["subscribers_delta"])?.toLong(),
        viewsByDay = sortedDays(daily.ifEmpty { dayPoints(d["views_by_day"]) }),
    )
}

private val DAY_KEY = Regex("^(\\d{4}-\\d{2}-\\d{2})(?:$|[T ])")
private val ISO_DAY = Regex("^\\d{4}-\\d{2}-\\d{2}$")

/** A Go `time.Time` ("2026-10-01T00:00:00Z") keeps its calendar day; anything else passes through. */
internal fun dayKey(v: String): String = DAY_KEY.find(v)?.groupValues?.get(1) ?: v

private fun dayPoints(v: JsonElement?): List<HubDayPoint> =
    jsonList(v)
        .mapNotNull { it as? JsonObject }
        .map { p ->
            val day = dayKey(str(p["day"]).ifEmpty { str(p["date"]) })
            HubDayPoint(day = day, views = maxOf(0.0, num(p["views"])).toLong())
        }
        .filter { it.day.isNotEmpty() }

/** The longest span the gap fill walks (the web's 400-day guard). */
private const val MAX_FILL_DAYS = 400L

/**
 * Merges duplicate days (summed), sorts oldest first and fills a day the
 * server skipped (no views recorded) with 0 between the first and last day,
 * so the chart's evenly spaced x axis shows every day.
 */
internal fun sortedDays(points: List<HubDayPoint>): List<HubDayPoint> {
    val byDay = LinkedHashMap<String, Long>()
    for (p in points) byDay[p.day] = (byDay[p.day] ?: 0L) + p.views
    val sorted = byDay.entries.sortedWith { a, b -> a.key.compareTo(b.key) }.map { HubDayPoint(it.key, it.value) }
    if (sorted.size < 2 || !sorted.all { ISO_DAY.matches(it.day) }) return sorted
    val first: LocalDate
    val last: LocalDate
    try {
        first = LocalDate.parse(sorted.first().day)
        last = LocalDate.parse(sorted.last().day)
    } catch (_: DateTimeParseException) {
        return sorted
    }
    if (ChronoUnit.DAYS.between(first, last) > MAX_FILL_DAYS) return sorted
    val out = ArrayList<HubDayPoint>()
    var day = first
    while (!day.isAfter(last)) {
        val key = day.toString()
        out += HubDayPoint(key, byDay[key] ?: 0L)
        day = day.plusDays(1)
    }
    return out
}

// ── Library ────────────────────────────────────────────────────────────

/** The two upload lists the library reads; [wire] is the path segment. */
enum class HubLibraryKind(val wire: String) {
    VIDEOS("videos"),
    FLICKS("flicks"),
}

enum class HubVisibility(val label: String) {
    PUBLIC("Public"),
    UNLISTED("Unlisted"),
    PRIVATE("Private"),
    SCHEDULED("Scheduled"),
}

/** A row of `GET /v1/uploads/videos|flicks`, normalised as the web's `normalizeLibraryRow` (the read-only fields). */
data class HubLibraryRow(
    val id: String,
    val title: String,
    val contentType: String,
    val visibility: HubVisibility,
    val scheduledAt: String?,
    val publishedAt: String?,
    val createdAt: String,
    val viewCount: Long,
    val likeCount: Long,
    val commentCount: Long,
    val processingStatus: String,
    /**
     * The row's still (2026-10-17), as the web's `normalizeLibraryRow` finds
     * it: `video_metadata.thumbnail_url`, else the chosen cover served at
     * `/v1/media/<cover_media_id>/serve`. A gateway path or an absolute URL;
     * null when the row has neither.
     */
    val thumbnailPath: String? = null,
    /**
     * The Action button (2026-10-23), the author's shape (url and status
     * included, clarification 4); null when the item has none. Its taps come
     * from `GET /v1/posts/:id/action/stats`.
     */
    val action: PostAction? = null,
    /**
     * Copyright title review (2026-10-24): the author's view of the row's
     * review (post-service my_uploads.go:399, the shape
     * copyright_title_review.go:651-658); null when it was never flagged.
     */
    val copyrightReview: CopyrightReview? = null,
    /** `flags[]` has `copyright_hold` (my_uploads.go:70): some active restriction hides the row. */
    val copyrightHold: Boolean = false,
    /** `restrictions[]` has one with `source: "title_review"` (copyright_title_review.go:28). */
    val titleReviewHold: Boolean = false,
) {
    /**
     * The row is hidden for copyright: a review that holds it, or a
     * restriction the uploads answer flags. A hold with no review to show
     * (a case-specific restriction) still reads as held.
     */
    val heldForCopyright: Boolean get() = copyrightReview?.isHeld ?: (copyrightHold || titleReviewHold)

    /** The row's date: published, else scheduled, else created (web `rowDate`). */
    val date: String get() = publishedAt ?: scheduledAt ?: createdAt
}

fun normalizeVisibility(raw: JsonElement?, scheduledAt: String?): HubVisibility =
    when (str(raw).lowercase()) {
        "public" -> HubVisibility.PUBLIC
        "unlisted" -> HubVisibility.UNLISTED
        "private" -> HubVisibility.PRIVATE
        "scheduled" -> HubVisibility.SCHEDULED
        else -> if (scheduledAt != null) HubVisibility.SCHEDULED else HubVisibility.PUBLIC
    }

/**
 * Null for anything that is not a row with an id. The web keeps an empty
 * id; the app drops it — a row with no id can open nothing, and a list
 * keyed on ids cannot hold two of them.
 */
fun normalizeLibraryRow(raw: JsonElement?): HubLibraryRow? {
    val r = raw as? JsonObject ?: return null
    val id = str(r["id"]).ifEmpty { return null }
    val vm = r["video_metadata"] as? JsonObject ?: JsonObject(emptyMap())
    val counts = r["counts"] as? JsonObject ?: JsonObject(emptyMap())
    val scheduledAt = strOrNull(r["scheduled_at"]) ?: strOrNull(r["publish_at"])
    return HubLibraryRow(
        id = id,
        title = str(r["title"]).trim().ifEmpty { firstLine(str(r["text"])) }.ifEmpty { "Untitled" },
        contentType = str(r["content_type"]).ifEmpty { str(vm["final_category"]) },
        visibility = normalizeVisibility(r["visibility"], scheduledAt),
        scheduledAt = scheduledAt,
        publishedAt = strOrNull(r["published_at"]),
        createdAt = str(r["created_at"]),
        viewCount = num(r["view_count"]).toLong(),
        likeCount = num(r["like_count"]).orElse(num(counts["likes"])).toLong(),
        commentCount = num(r["comment_count"]).orElse(num(counts["comments"])).toLong(),
        processingStatus = str(r["processing_status"]).ifEmpty { str(vm["upload_status"]) },
        thumbnailPath = strOrNull(vm["thumbnail_url"]) ?: strOrNull(r["cover_media_id"])?.let { "/v1/media/$it/serve" },
        action = r["action"].toPostAction(),
        copyrightReview = r["copyright_review"].toCopyrightReview(),
        copyrightHold = r["flags"].hasCopyrightHoldFlag(),
        titleReviewHold = r["restrictions"].hasTitleReviewRestriction(),
    )
}

/** The library's page size (web `listLibrary`). */
const val HUB_LIBRARY_PAGE = 30

// ── Drafts ─────────────────────────────────────────────────────────────

/** A row of `GET /v1/reels/drafts` (post-service `ReelDraft`), normalised as the web's `normalizeDraft`. */
data class HubDraft(
    val id: String,
    val title: String,
    val visibility: String,
    val status: String,
    val scheduleAt: String?,
    val publishedPostId: String?,
    val blockedReason: String?,
    val createdAt: String,
    val updatedAt: String,
) {
    /** A draft waiting on its schedule keeps status "draft" on the wire; it reads as Scheduled (web `draftStatus`). */
    val displayStatus: String get() = if (status == "draft" && scheduleAt != null) "scheduled" else status

    /** "Draft", "Scheduled", "Held", "Published" (web `draftStatusLabel`). */
    val statusLabel: String get() = draftStatusLabel(displayStatus)
}

fun normalizeDraft(raw: JsonElement?): HubDraft? {
    val r = raw as? JsonObject ?: return null
    val id = str(r["id"]).ifEmpty { return null }
    val createdAt = str(r["created_at"])
    return HubDraft(
        id = id,
        title = str(r["title"]).trim().ifEmpty { firstLine(str(r["caption"])) }.ifEmpty { "Untitled draft" },
        visibility = str(r["visibility"]).lowercase(),
        status = str(r["status"]).lowercase().ifEmpty { "draft" },
        scheduleAt = strOrNull(r["schedule_at"]),
        publishedPostId = strOrNull(r["published_post_id"]),
        blockedReason = strOrNull(r["blocked_reason"]),
        createdAt = createdAt,
        updatedAt = str(r["updated_at"]).ifEmpty { createdAt },
    )
}

private val DRAFT_STATUS_LABEL = mapOf(
    "draft" to "Draft",
    "scheduled" to "Scheduled",
    "blocked" to "Held",
    "published" to "Published",
)

fun draftStatusLabel(status: String): String =
    DRAFT_STATUS_LABEL[status] ?: status.replaceFirstChar { it.uppercaseChar() }.ifEmpty { "Draft" }

/** The server's default page; it caps `limit` at 50 (web `HUB_DRAFTS_PAGE`). */
const val HUB_DRAFTS_PAGE = 20
private const val DRAFTS_MAX = 50

/** The web's `draftsParams` limit: whole, 1..50. */
fun draftsLimit(limit: Int = HUB_DRAFTS_PAGE): Int = limit.coerceIn(1, DRAFTS_MAX)

// ── Comments inbox ─────────────────────────────────────────────────────

/** The inbox filters the app sends: every comment, every kind, newest first (the web overview's). */
object HubInboxQuery {
    const val STATUS = "all"
    const val CONTENT = "all"
    const val SORT = "newest"
    const val LIMIT = 30
}

/** A row of `GET /v1/comments/inbox`, normalised as the web's `normalizeInboxRow` (the read-only fields). */
data class HubInboxRow(
    val commentId: String,
    val postId: String,
    val body: String,
    val authorId: String,
    /** The hydrated author's display name, else handle; "" when the server sent no author. */
    val authorName: String,
    val createdAt: String,
    val postTitle: String,
    val postContentType: String,
    /** The post's author (the creator) has replied to this comment. */
    val authorReplied: Boolean,
    /** The creator pinned it (`comment.pinned`; at most one per post, the server's rule). */
    val pinned: Boolean = false,
    /** The creator hearted it (`comment.hearted_by_author`). */
    val hearted: Boolean = false,
    /** A reply: the server refuses a pin on one (422 `CANNOT_PIN_REPLY`), so it offers none. */
    val isReply: Boolean = false,
)

/** [rows] with [commentId]'s heart set to [hearted]. */
fun List<HubInboxRow>.withHeart(commentId: String, hearted: Boolean): List<HubInboxRow> =
    map { if (it.commentId == commentId) it.copy(hearted = hearted) else it }

/**
 * [rows] with [commentId]'s pin set to [pinned]. A pin REPLACES any other
 * pin on the same post — post-service keeps one per post — so the rows of
 * that post lose theirs at the same moment.
 */
fun List<HubInboxRow>.withPin(commentId: String, pinned: Boolean): List<HubInboxRow> {
    val postId = firstOrNull { it.commentId == commentId }?.postId ?: return this
    return map { row ->
        when {
            row.commentId == commentId -> row.copy(pinned = pinned)
            pinned && row.postId == postId -> row.copy(pinned = false)
            else -> row
        }
    }
}

fun normalizeInboxRow(raw: JsonElement?): HubInboxRow? {
    val r = raw as? JsonObject ?: return null
    val c = r["comment"] as? JsonObject ?: r
    val id = c["id"]
    if (id !is JsonPrimitive || !id.isString || id.content.isEmpty()) return null
    val p = r["post"] as? JsonObject ?: JsonObject(emptyMap())
    val postId = str(p["id"]).ifEmpty { str(c["post_id"]) }.ifEmpty { return null }
    val author = c["author"] as? JsonObject
    return HubInboxRow(
        commentId = id.content,
        postId = postId,
        body = str(c["body"]).ifEmpty { str(c["text"]) },
        authorId = str(c["author_id"]),
        authorName = author?.let { str(it["display_name"]).trim().ifEmpty { str(it["username"]) } }.orEmpty(),
        createdAt = str(c["created_at"]),
        postTitle = str(p["title"]).trim().ifEmpty { "Untitled" },
        postContentType = str(p["content_type"]),
        authorReplied = bool(r["author_replied"]),
        pinned = bool(c["pinned"]),
        hearted = bool(c["hearted_by_author"]),
        isReply = bool(c["is_reply"]),
    )
}

// ── Where a row opens ──────────────────────────────────────────────────

/** The surface a creator's post opens on: Reels, the watch page, or post detail. */
enum class HubOpenKind { REEL, VIDEO, POST }

/** The web's `postHref`: a short type opens on Reels, a long video on the watch page, anything else as a post. */
fun hubOpenKind(contentType: String, fallback: HubOpenKind = HubOpenKind.POST): HubOpenKind =
    when (contentType.lowercase()) {
        "reel", "flick", "short" -> HubOpenKind.REEL
        "long_video", "video" -> HubOpenKind.VIDEO
        "" -> fallback
        else -> HubOpenKind.POST
    }

// ── Formatting (the web's, so the figures read the same) ───────────────

private const val THOUSAND = 1_000.0
private const val MILLION = 1_000_000.0
private const val BILLION = 1_000_000_000.0
private const val ONE_DECIMAL_BELOW = 10.0

/**
 * The web's `formatCount` (reels/model.ts), which the Creator Hub uses:
 * `1234` → "1.2K", `12345` → "12K", `0` or less → "0". Not
 * `core.ui.formatCount`, which truncates to one decimal at every size and
 * would read "12.3K" where the web reads "12K".
 */
fun hubCount(n: Long): String = when {
    n <= 0 -> "0"
    n < THOUSAND -> n.toString()
    n < MILLION -> "${trimFixed(n / THOUSAND)}K"
    n < BILLION -> "${trimFixed(n / MILLION)}M"
    else -> "${trimFixed(n / BILLION)}B"
}

/** JS `x.toFixed(x < 10 ? 1 : 0)` with a trailing ".0" dropped; `toFixed` rounds the exact binary value half up. */
private fun trimFixed(x: Double): String {
    val s = BigDecimal(x).setScale(if (x < ONE_DECIMAL_BELOW) 1 else 0, RoundingMode.HALF_UP).toPlainString()
    return s.removeSuffix(".0")
}

private const val SECONDS_PER_HOUR = 3_600L
private const val SECONDS_PER_MINUTE = 60L
private const val MS_PER_SECOND = 1_000L

/** Watch time: "12h 5m", "48m", "30s" (web `formatWatchTime`). */
fun formatWatchTime(ms: Long): String {
    val total = maxOf(0L, ms / MS_PER_SECOND)
    val h = total / SECONDS_PER_HOUR
    val m = (total % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE
    return when {
        h > 0 -> "${h}h ${m}m"
        m > 0 -> "${m}m"
        else -> "${total}s"
    }
}

/** "+12", "-3", "0"; null (not tracked) → an em dash (web `formatDelta`). */
fun formatDelta(n: Long?): String = when {
    n == null -> "—"
    n > 0 -> "+$n"
    else -> n.toString()
}

/** The note under Followers: the net change is not tracked yet; follows straight from a video are (web `followersNote`). */
fun followersNote(d: CreatorInsights): String = when {
    d.followersDelta != null -> "Net change in this period"
    d.followsFromContent > 0 -> "Not tracked yet · ${hubCount(d.followsFromContent)} followed from a video"
    else -> "Not tracked yet"
}

// ── JSON readers (the web's num / str / bool) ──────────────────────────

private fun jsonList(v: JsonElement?): List<JsonElement> = (v as? JsonArray).orEmpty()

/** A finite JSON number, else 0 — strings, booleans and null are not numbers. */
internal fun num(v: JsonElement?): Double = numberOrNull(v) ?: 0.0

private fun numberOrNull(v: JsonElement?): Double? {
    val p = v as? JsonPrimitive ?: return null
    if (p is JsonNull || p.isString) return null
    return p.doubleOrNull?.takeIf { it.isFinite() }
}

internal fun str(v: JsonElement?): String {
    val p = v as? JsonPrimitive ?: return ""
    return if (p.isString) p.content else ""
}

private fun strOrNull(v: JsonElement?): String? = str(v).ifEmpty { null }

private fun bool(v: JsonElement?): Boolean {
    val p = v as? JsonPrimitive ?: return false
    return if (p.isString) false else p.booleanOrNull ?: false
}

/** JS `a || b` over numbers: zero falls through. */
private fun Double.orElse(other: Double): Double = if (this != 0.0) this else other

/** The first non-zero number among [keys] (web `firstNum`). */
private fun firstNum(d: JsonObject, vararg keys: String): Double {
    for (k in keys) {
        val n = num(d[k])
        if (n != 0.0) return n
    }
    return 0.0
}

private fun firstLine(text: String): String =
    text.split(Regex("\\r?\\n")).map { it.trim() }.firstOrNull { it.isNotEmpty() }.orEmpty()
