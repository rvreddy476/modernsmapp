package com.us.android.core.feed.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.PUT
import retrofit2.http.Path
import javax.inject.Inject
import javax.inject.Singleton

/*
 * A published FEED post's owner tools (2026-10-24, the web's postEdit.ts and
 * pinned.ts): "Edit post" — text, who can see it, comments, the like count —
 * and "Pin to profile". The video Edit sheet ([PostEditForm]) edits far more;
 * a feed post edits these four and NOTHING else, so the PATCH never names
 * `tier_required_id` or `action`, whose null CLEARS (post_edit.go:95-100):
 * an edit can't silently open a members-only post or drop its button.
 */

/** Who can see a feed post: the composer's four, in its words (postEdit.ts EDIT_VISIBILITY_OPTIONS). */
object FeedPostVisibility {
    val choices = listOf(
        EditChoice("public", "Everyone"),
        EditChoice("followers", "Followers"),
        EditChoice("friends", "Friends"),
        EditChoice("private", "Only you"),
    )

    /** Every value the PATCH may carry; `unlisted` only where the post already is. */
    private val sendable = choices.map { it.wire }.toSet() + "unlisted"

    fun isSendable(value: String): Boolean = value in sendable

    /** The four, plus the post's own when it is another (`unlisted`, or one this build does not know). */
    fun choicesFor(current: String): List<EditChoice> = when {
        choices.any { it.wire == current } || current.isBlank() -> choices
        current == "unlisted" -> choices + EditChoice("unlisted", "Unlisted")
        else -> choices + EditChoice(current, "Current setting")
    }
}

/** The sheet's four fields. */
data class FeedPostEditForm(
    val text: String = "",
    val visibility: String = "public",
    val allowComments: Boolean = true,
    val hideLikeCount: Boolean = false,
)

/** The form for a post, from its owner detail. */
fun PostEditDetailDto.toFeedPostEditForm(): FeedPostEditForm = FeedPostEditForm(
    text = text,
    visibility = visibility.ifBlank { "public" },
    allowComments = !noComments,
    hideLikeCount = hideLikeCount,
)

/**
 * The PATCH from [base] to [next]: ONLY the four fields, and only those that
 * moved (postEdit.ts `buildPostEditBody`). A visibility the route does not
 * take is never sent.
 */
fun feedPostEditPatch(base: FeedPostEditForm, next: FeedPostEditForm): PostEditPatch = PostEditPatch(
    text = next.text.takeIf { it != base.text },
    visibility = next.visibility.takeIf { it != base.visibility && FeedPostVisibility.isSendable(it) },
    noComments = (!next.allowComments).takeIf { next.allowComments != base.allowComments },
    hideLikeCount = next.hideLikeCount.takeIf { it != base.hideLikeCount },
)

/** The server's ceiling on post text, in code points (create_guards.go MaxPostTextRunes). */
const val FEED_POST_TEXT_MAX = 5000

/** What stops Save before anything is sent (postEdit.ts `postEditProblem`); null when it may. */
fun feedPostEditProblem(base: FeedPostEditForm, form: FeedPostEditForm, hasMedia: Boolean): String? {
    val length = form.text.codePointCount(0, form.text.length)
    return when {
        length > FEED_POST_TEXT_MAX -> "Posts can be up to 5,000 characters. This one is ${String.format(java.util.Locale.US, "%,d", length)}."
        form.text.isBlank() && !hasMedia && form.text != base.text -> EMPTY_POST
        else -> null
    }
}

/** A refused or failed save, in the web's sentences (postEdit.ts EDIT_ERROR_COPY). */
fun feedPostEditRefusal(error: AppError): String = when (error) {
    is AppError.NoNetwork, is AppError.Timeout ->
        "You seem to be offline. Nothing was saved. Check your connection and try again."
    is AppError.NotFound -> "This post no longer exists."
    is AppError.RateLimited -> FEED_EDIT_REFUSALS.getValue("RATE_LIMITED")
    else -> FEED_EDIT_REFUSALS[error.serverCode()]
        ?: if (error is AppError.Forbidden) {
            FEED_EDIT_REFUSALS.getValue("FORBIDDEN")
        } else if (error is AppError.Server && error.statusCode == HTTP_UNAVAILABLE) {
            FEED_EDIT_REFUSALS.getValue("STORE_UNAVAILABLE")
        } else {
            "Your changes couldn't be saved. Please try again."
        }
}

private const val EMPTY_POST = "A post without a photo or video needs some text."
private const val HTTP_UNAVAILABLE = 503

private val FEED_EDIT_REFUSALS: Map<String, String> = mapOf(
    "COPYRIGHT_REVIEW" to "This post is in copyright review, so it can't stay members-only. Nothing was changed. " +
        "Remove members-only from the post, or wait for the review to finish.",
    "EMPTY_POST" to EMPTY_POST,
    "TEXT_TOO_LONG" to "Posts can be up to 5,000 characters. Shorten the text and try again.",
    "INVALID_VISIBILITY" to "Choose who can see this post from the list.",
    "INVALID_HASHTAG" to "One of the hashtags can't be used. Hashtags take letters, numbers and underscores.",
    "TOO_MANY_HASHTAGS" to "This post has too many hashtags. Remove a few and try again.",
    "FORBIDDEN" to "Only the person who posted this can edit it.",
    "NOT_FOUND" to "This post no longer exists.",
    "INVALID_TIER" to "The members-only level on this post isn't one of your active levels any more. Nothing was changed.",
    "MEMBERSHIP_TIERS_UNAVAILABLE" to "Membership levels couldn't be checked right now, so nothing was saved. Try again in a moment.",
    "STORE_UNAVAILABLE" to "Editing isn't available right now. Nothing was saved. Try again in a moment.",
    "RATE_LIMITED" to "Too many changes at once. Wait a moment and try again.",
    "INVALID_REQUEST" to "Those changes couldn't be read. Try again.",
)

// ── Pin to profile ──────────────────────────────────────────────────────

/** `PUT /v1/posts/:id/pin {pinned}` (post-service internal/http/handler.go:133, TogglePin :1443-1475). */
interface PostPinApi {
    @PUT("v1/posts/{postId}/pin")
    suspend fun pin(@Path("postId") postId: String, @Body body: PinRequest): ApiEnvelope<PinAnswer>
}

@Serializable
data class PinRequest(val pinned: Boolean)

/** The answer: `{"data":{"pinned":<bool>}}` (handler.go:1474). */
@Serializable
data class PinAnswer(val pinned: Boolean = false)

/** The pin cap (post-service MaxPinnedPosts, the 409's `details.max`). */
const val MAX_PINNED_POSTS = 3

/** What 409 PIN_LIMIT says (pinned.ts PIN_LIMIT_MESSAGE). */
const val PIN_LIMIT_MESSAGE = "You can pin up to 3 posts. Unpin one first."

/**
 * A failed pin or unpin (pinned.ts `pinErrorCopy`): ONLY a 409 with
 * `PIN_LIMIT` on a pin says how many — another 409, a bare code on another
 * status, or an unpin is a plain failure.
 */
fun pinRefusal(error: AppError, pinning: Boolean): String = when {
    error is AppError.NoNetwork || error is AppError.Timeout ->
        "You seem to be offline. Check your connection and try again."
    pinning && error is AppError.Unknown && error.statusCode == HTTP_CONFLICT && error.code == "PIN_LIMIT" ->
        PIN_LIMIT_MESSAGE
    pinning -> "That post couldn't be pinned. Please try again."
    else -> "That post couldn't be unpinned. Please try again."
}

private const val HTTP_CONFLICT = 409

/**
 * The pins this session made, by post id — what the More sheet reads over a
 * row's `is_pinned`, so "Unpin" follows a pin at once without a refetch.
 */
@Singleton
class PostPins @Inject constructor() {
    private val _pins = MutableStateFlow<Map<String, Boolean>>(emptyMap())
    val pins: StateFlow<Map<String, Boolean>> = _pins.asStateFlow()

    fun record(postId: String, pinned: Boolean) = _pins.update { it + (postId to pinned) }
}

@Singleton
class PostPinRepository @Inject constructor(
    private val api: PostPinApi,
    private val session: PostPins,
    private val errorMapper: ErrorMapper,
) {
    /** This session's pins, by post id. */
    val pins: StateFlow<Map<String, Boolean>> get() = session.pins

    /** Pins or unpins [postId]; on success the session learns it through [PostPins]. */
    suspend fun setPinned(postId: String, pinned: Boolean): AppResult<Boolean> =
        apiCall(errorMapper) { api.pin(postId, PinRequest(pinned)) }
            .map { it.pinned }
            .also { if (it is AppResult.Success) session.record(postId, it.data) }
}
