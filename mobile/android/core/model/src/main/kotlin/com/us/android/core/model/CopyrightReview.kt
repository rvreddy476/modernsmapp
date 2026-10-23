package com.us.android.core.model

import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/*
  Copyright title review (founder quick wins, 2026-10-24; server commit
  dbdd1c0ea). A long video whose title, description or hashtags name a film
  or TV title on the staff catalogue is published but HELD — hidden from
  every viewer — until a reviewer clears or blocks it.

  The AUTHOR sees where it stands: their own `GET /v1/posts/:id` and uploads
  rows carry `copyright_review` (post-service service/post.go:615, the shape
  service/copyright_title_review.go:651-658), absent when the video was never
  flagged and absent for every other viewer. The author may answer a pending
  review once with a note ("I own the rights / I have a licence"),
  `POST /v1/posts/:id/copyright-review/request` (http/copyright_review.go:22).

  Every sentence the app says about it is HERE, so the hub, the Edit sheet,
  the watch screen and the publish tile cannot disagree — and the web
  (`features/posttube/copyright`) says the same words.
*/

/** `copyright_review.state` (store/postgres/copyright_title_review.go:40). */
enum class CopyrightReviewState(val wire: String) {
    PENDING("pending"),
    CLEARED("cleared"),
    BLOCKED("blocked"),
    ;

    companion object {
        /** Null for anything the server does not send: such a review is not drawn. */
        fun fromWire(raw: String?): CopyrightReviewState? {
            val value = raw?.trim()?.lowercase().orEmpty()
            return entries.firstOrNull { it.wire == value }
        }
    }
}

/**
 * The author's view of one review. Instants are RFC 3339 strings as the wire
 * sends them; [reviewRequestedAt] is set once the author has sent their note.
 */
data class CopyrightReview(
    val state: CopyrightReviewState,
    val matchedTitle: String,
    val createdAt: String,
    val decidedAt: String? = null,
    val reviewRequestedAt: String? = null,
) {
    /** The video is hidden from viewers: in review, or blocked. */
    val isHeld: Boolean get() = state != CopyrightReviewState.CLEARED

    /** The author may still send "I own the rights": pending, and not asked yet. */
    val canRequestReview: Boolean get() = state == CopyrightReviewState.PENDING && reviewRequestedAt == null
}

/** What a surface draws for a review: the sentence, and the second line when there is one. */
data class CopyrightNotice(
    val state: CopyrightReviewState,
    val message: String,
    /** "Review requested 24 Oct 2026", once the author has asked; null otherwise. */
    val requested: String? = null,
    /** "Request review" is offered. */
    val offersRequest: Boolean = false,
)

/** Every word the app says about copyright review and the long-video upload pause. */
object CopyrightCopy {

    /** The longest note the server takes, in characters (code points; copyright_title_review.go:709). */
    const val NOTE_MAX = 1000

    /** The request sheet's placeholder (the contract's words). */
    const val NOTE_PLACEHOLDER = "I own the rights / I have a licence"

    const val REQUEST_LABEL = "Request review"

    /** The members-only refusal while a video is held (409 COPYRIGHT_REVIEW, standing_errors.go:75-78). */
    const val MEMBERS_ONLY_HELD =
        "This video is in copyright review, so it can't be members-only. Choose Everyone and try again."

    /** Said under the members-only picker of a held video, before anyone tries. */
    const val MEMBERS_ONLY_HELD_NOTE = "Members-only is off while this video is in copyright review."

    /** The notice for [review]: its sentence, the request line, and whether "Request review" is offered. */
    fun notice(review: CopyrightReview, zone: ZoneId = ZoneId.systemDefault()): CopyrightNotice {
        val title = review.matchedTitle.trim()
        return when (review.state) {
            CopyrightReviewState.PENDING -> CopyrightNotice(
                state = review.state,
                message = pendingMessage(title),
                requested = review.reviewRequestedAt?.let { requestedLine(it, zone) },
                offersRequest = review.canRequestReview,
            )
            CopyrightReviewState.CLEARED -> CopyrightNotice(review.state, CLEARED)
            CopyrightReviewState.BLOCKED -> CopyrightNotice(review.state, blockedMessage(title))
        }
    }

    /** The short label a list row or a tile wears. */
    fun label(review: CopyrightReview): String = when (review.state) {
        CopyrightReviewState.PENDING -> "In copyright review"
        CopyrightReviewState.CLEARED -> "Cleared"
        CopyrightReviewState.BLOCKED -> "Blocked for copyright"
    }

    /**
     * 403 `UPLOADS_PAUSED` (standing_errors.go:54-55, details `until`
     * RFC 3339): the date in [zone]; a missing or unreadable date leaves it out.
     */
    fun uploadsPaused(until: String?, zone: ZoneId = ZoneId.systemDefault()): String {
        val day = day(until, zone)
        val pause = if (day == null) "Long-video uploads are paused" else "Long-video uploads are paused until $day"
        return "$pause after repeated copyright blocks. Reels and posts still work."
    }

    /**
     * A parked draft's or scheduled post's `blocked_reason` in words
     * (copyright_title_review.go:64-75): `uploads_paused until <RFC3339>`,
     * or `copyright_review`. Null for any other reason, which reads as itself.
     */
    fun blockedReason(reason: String?, zone: ZoneId = ZoneId.systemDefault()): String? {
        val raw = reason?.trim().orEmpty()
        return when {
            raw == BLOCK_COPYRIGHT_REVIEW -> MEMBERS_ONLY_HELD
            raw == BLOCK_UPLOADS_PAUSED -> uploadsPaused(null, zone)
            raw.startsWith("$BLOCK_UPLOADS_PAUSED until ") ->
                uploadsPaused(raw.removePrefix("$BLOCK_UPLOADS_PAUSED until "), zone)
            else -> null
        }
    }

    /** Why a note cannot be sent yet; null when it can (1..[NOTE_MAX] characters once trimmed). */
    fun noteProblem(note: String): String? {
        val trimmed = note.trim()
        return when {
            trimmed.isEmpty() -> "Tell us why you have the rights to this video."
            trimmed.codePointCount(0, trimmed.length) > NOTE_MAX -> "Keep it to $NOTE_MAX characters."
            else -> null
        }
    }

    /** `2026-10-24T09:00:00Z` → `24 Oct 2026` in [zone]; null for anything that is not an instant. */
    fun day(instant: String?, zone: ZoneId = ZoneId.systemDefault()): String? {
        val raw = instant?.trim().orEmpty()
        if (raw.isEmpty()) return null
        return runCatching {
            OffsetDateTime.parse(raw).atZoneSameInstant(zone).format(DAY)
        }.getOrNull()
    }

    private fun pendingMessage(title: String): String {
        val base = "In copyright review — this video is hidden until our team checks it."
        return if (title.isEmpty()) base else "$base It matched “$title”."
    }

    private fun blockedMessage(title: String): String {
        val head = if (title.isEmpty()) "Blocked for copyright." else "Blocked for copyright — it matched “$title”."
        return "$head This isn't a strike, but repeated blocks pause long-video uploads."
    }

    private fun requestedLine(at: String, zone: ZoneId): String =
        day(at, zone)?.let { "Review requested $it" } ?: "Review requested"

    private const val CLEARED = "Cleared — your video is live"

    /** The server's parked-publication reasons (copyright_title_review.go:73-74). */
    const val BLOCK_UPLOADS_PAUSED = "uploads_paused"
    const val BLOCK_COPYRIGHT_REVIEW = "copyright_review"

    private val DAY: DateTimeFormatter = DateTimeFormatter.ofPattern("d MMM yyyy", Locale.ENGLISH)
}
