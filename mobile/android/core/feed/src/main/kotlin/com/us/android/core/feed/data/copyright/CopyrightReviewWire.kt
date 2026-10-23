package com.us.android.core.feed.data.copyright

import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.serverCode
import com.us.android.core.model.CopyrightCopy
import com.us.android.core.model.CopyrightReview
import com.us.android.core.model.CopyrightReviewState
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/*
  The copyright title review's wire (post-service, server commit dbdd1c0ea):

    copyright_review   service/copyright_title_review.go:651-658
                       {state, matched_title, created_at, decided_at?, review_requested_at?}
                       on the author's own GET /v1/posts/:id (service/post.go:615) and
                       uploads rows (service/my_uploads.go:399); absent otherwise
    flags[]            "copyright_hold" (my_uploads.go:70): an active restriction
    restrictions[]     {case_id, source, reason_code, policy_version, placed_at}
                       (my_uploads.go:77-83); source "title_review"
                       (store/postgres/copyright_title_review.go:28)

  Read leniently, as every wire here is: a field of the wrong type is absent,
  and a review whose state this build does not know is not drawn at all.
*/

/** The `copyright_review` object, or null when absent, not an object, or of an unknown state. */
fun JsonElement?.toCopyrightReview(): CopyrightReview? {
    val o = this as? JsonObject ?: return null
    val state = CopyrightReviewState.fromWire(o.string("state")) ?: return null
    return CopyrightReview(
        state = state,
        matchedTitle = o.string("matched_title").orEmpty().trim(),
        createdAt = o.string("created_at").orEmpty(),
        decidedAt = o.string("decided_at")?.takeIf { it.isNotBlank() },
        reviewRequestedAt = o.string("review_requested_at")?.takeIf { it.isNotBlank() },
    )
}

/** `flags[]` carries `copyright_hold`: the row is hidden from viewers by an active restriction. */
fun JsonElement?.hasCopyrightHoldFlag(): Boolean =
    (this as? JsonArray).orEmpty().any { (it as? JsonPrimitive)?.takeIf { p -> p.isString }?.content == FLAG_COPYRIGHT_HOLD }

/** `restrictions[]` has one from the title review (`source: "title_review"`). */
fun JsonElement?.hasTitleReviewRestriction(): Boolean =
    (this as? JsonArray).orEmpty().any { (it as? JsonObject)?.string("source") == SOURCE_TITLE_REVIEW }

const val FLAG_COPYRIGHT_HOLD = "copyright_hold"
const val SOURCE_TITLE_REVIEW = "title_review"

/** The server's codes for this feature (http/copyright_review.go:49-55, http/standing_errors.go:72-73). */
object CopyrightCodes {
    const val UPLOADS_PAUSED = "UPLOADS_PAUSED"
    const val COPYRIGHT_REVIEW = "COPYRIGHT_REVIEW"
    const val REVIEW_NOT_FOUND = "REVIEW_NOT_FOUND"
    const val REVIEW_ALREADY_DECIDED = "REVIEW_ALREADY_DECIDED"
    const val UNAVAILABLE = "COPYRIGHT_REVIEW_UNAVAILABLE"

    /** The `details` key UPLOADS_PAUSED carries its date under (standing_errors.go:55). */
    const val UNTIL = "until"
}

/**
 * A publish refused because long-video uploads are paused (403
 * `UPLOADS_PAUSED`, standing_errors.go:53-55), in words with the date; null
 * for any other refusal. The mapper keeps a 403's code and details.
 */
fun AppError.uploadsPausedMessage(): String? {
    val forbidden = this as? AppError.Forbidden ?: return null
    if (forbidden.code != CopyrightCodes.UPLOADS_PAUSED) return null
    return CopyrightCopy.uploadsPaused(forbidden.details[CopyrightCodes.UNTIL])
}

/** 409 `COPYRIGHT_REVIEW` (standing_errors.go:75-78): a members-only level on a held video. */
fun AppError.isCopyrightReviewTier(): Boolean = serverCode() == CopyrightCodes.COPYRIGHT_REVIEW

/**
 * Why "Request review" did not go through, in words
 * (http/copyright_review.go:322-334). A 404 comes from the mapper with no
 * code (it files every 404 as NotFound), and on this route it can only
 * mean there is no review of the caller's on the post.
 */
fun copyrightRequestRefusal(error: AppError): String = when {
    error is AppError.NotFound -> "This video has no copyright review to answer any more."
    error.serverCode() == CopyrightCodes.REVIEW_NOT_FOUND -> "This video has no copyright review to answer any more."
    error.serverCode() == CopyrightCodes.REVIEW_ALREADY_DECIDED ->
        "This review has already been decided. Refresh to see the result."
    error.serverCode() == CopyrightCodes.UNAVAILABLE ||
        (error is AppError.Server && error.statusCode == HTTP_UNAVAILABLE) ->
        "Copyright review is unavailable right now. Try again in a little while."
    error is AppError.InvalidRequest -> "Write a note of 1 to ${CopyrightCopy.NOTE_MAX} characters."
    error is AppError.NoNetwork || error is AppError.Timeout -> "You're offline. Try again when you're connected."
    else -> "Your request didn't go through. Try again."
}

private const val HTTP_UNAVAILABLE = 503

private fun JsonObject.string(key: String): String? {
    val p = this[key] as? JsonPrimitive ?: return null
    return if (p.isString) p.content else null
}
