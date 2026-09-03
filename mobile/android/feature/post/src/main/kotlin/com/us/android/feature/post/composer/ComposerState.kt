package com.us.android.feature.post.composer

import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC

/**
 * Everything the composer can be doing, as one closed set.
 *
 * One sealed phase rather than a handful of booleans. `isUploading`,
 * `isPublishing` and `hasError` as separate flags admit combinations that
 * cannot happen (uploading AND published) and, worse, admit the one that
 * matters: showing success while a publish is still in flight.
 */
sealed interface ComposerPhase {
    /** Typing. The only phase where the draft is editable. */
    data object Editing : ComposerPhase

    /** Reading the picked image's size/type before anything is sent. */
    data object PreparingImage : ComposerPhase

    /** Bytes moving. [uploaded]/[total] is monotonic. */
    data class Uploading(val uploaded: Long, val total: Long) : ComposerPhase {
        val fraction: Float
            get() = if (total <= 0L) 0f else (uploaded.toFloat() / total.toFloat()).coerceIn(0f, 1f)
    }

    /** Telling the server the bytes landed, and waiting for `ready`. */
    data object Confirming : ComposerPhase

    /** The create call is in flight. */
    data object Publishing : ComposerPhase

    /** The server returned a post id. Nothing before this may claim success. */
    data class Published(val postId: String) : ComposerPhase

    /**
     * Something failed and trying again might work.
     *
     * The draft is intact and the creation key is unchanged, so a retry sends
     * the identical bytes under the identical key and the server replays rather
     * than duplicating.
     */
    data class RetryableFailure(val message: String) : ComposerPhase

    /**
     * Something failed and trying again cannot work.
     *
     * Media rejected by moderation, an unsupported file, a payload the server
     * refuses. Separate from [RetryableFailure] because offering Retry here is
     * offering a button that is guaranteed to fail.
     */
    data class TerminalFailure(val message: String) : ComposerPhase
}

/** Why Post is unavailable, so the UI never shows a disabled control with no reason. */
enum class PostBlockedReason {
    /** Neither text nor an image. */
    Empty,

    /** Over the server's code-point ceiling. */
    TextTooLong,

    /** An image is attached with neither a description nor a decorative mark. */
    MissingAltDecision,

    /** The image is still uploading or processing. */
    MediaNotReady,

    /** A publish is already in flight. Single-flight guard. */
    Busy,

    /** An article with no title. Only reachable in long-form mode. */
    MissingTitle,
}

/**
 * The immutable snapshot a publish is made from.
 *
 * ## WHY THE PAYLOAD IS FROZEN
 *
 * The exact request bytes and the creation key are captured together the moment
 * Post is pressed, and neither changes afterwards. Rebuilding the request on
 * each retry from live UI state would mean a retry could carry different bytes
 * under the same key — which the server correctly rejects as key reuse (409),
 * and which the user experiences as "my post failed and now it will not even
 * retry".
 *
 * Persisted with the draft, so a retry after process death still sends the same
 * key with the same body and gets the original post replayed instead of
 * creating a second one.
 */
data class FrozenPublish(
    /** The UUID sent as `Idempotency-Key`. Stable across every retry. */
    val creationKey: String,
    val request: CreatePostRequest,
)

/**
 * One immutable state object for the whole screen.
 *
 * The screen renders this and calls back; it holds no parallel copy of upload
 * or publish truth. A second copy is how a UI ends up showing "uploading" for
 * an upload that already failed.
 */
data class ComposerUiState(
    val text: String = "",
    /**
     * The article title. Only meaningful — and only sent — when [longForm];
     * a short post ignores it entirely. Not part of the durable draft: the
     * draft row has no column for it, and adding one is a Room migration
     * that this change deliberately does not make.
     */
    val title: String = "",
    /** True for the Article surface: a title is required and goes on the wire. */
    val longForm: Boolean = false,
    /** Content URI of the picked image, as a string so it survives persistence. */
    val imageUri: String? = null,
    val altText: String = "",
    val decorative: Boolean = false,
    val language: String = DEFAULT_LANGUAGE,
    /** One of `SupportedAudience`. Public unless the author narrows it. */
    val visibility: String = VISIBILITY_PUBLIC,
    /** Set once the image is confirmed AND ready. Only then may it be attached. */
    val mediaId: String? = null,
    val phase: ComposerPhase = ComposerPhase.Editing,
    val frozen: FrozenPublish? = null,
    /** True after a validation failure, so the screen can move focus to the error. */
    val showValidationErrors: Boolean = false,
    /** Set when the user asked to leave with unsaved content. */
    val confirmingDiscard: Boolean = false,
    /**
     * Set ONLY after the durable draft has actually been deleted — C-CLB-2.
     *
     * The screen navigates away on this, not on the discard tap. The two are
     * not the same moment: popping the destination clears the Hilt-owned
     * ViewModel and cancels its scope, so a `drafts.clear()` still in flight
     * was cancelled with it and the draft survived. Content the user
     * explicitly threw away then came back the next time they opened the
     * composer, which is the worst possible direction for that bug to fail in.
     *
     * Making navigation a CONSEQUENCE of the delete rather than a sibling of it
     * is what fixes the ordering. Same shape as [ComposerPhase.Published],
     * where the screen also moves only once the durable outcome is real.
     */
    val discarded: Boolean = false,
    /** True when the draft was restored after process death. */
    val restoredFromDraft: Boolean = false,
) {
    val hasImage: Boolean get() = imageUri != null

    /** Counted in CODE POINTS, matching the server. See [MAX_TEXT_CODE_POINTS]. */
    val textCodePoints: Int get() = text.codePointCount(0, text.length)

    val textTooLong: Boolean get() = textCodePoints > MAX_TEXT_CODE_POINTS

    /** An attached image must be described or explicitly marked decorative. */
    val altDecisionMade: Boolean get() = !hasImage || decorative || altText.isNotBlank()

    val isBusy: Boolean
        get() = when (phase) {
            is ComposerPhase.PreparingImage,
            is ComposerPhase.Uploading,
            is ComposerPhase.Confirming,
            is ComposerPhase.Publishing,
            -> true

            else -> false
        }

    /** True when there is content worth confirming a discard for. */
    val hasContent: Boolean get() = text.isNotBlank() || hasImage || (longForm && title.isNotBlank())

    /** An article without a title is not an article. */
    val titleMissing: Boolean get() = longForm && title.isBlank()

    val titleTooLong: Boolean get() = title.length > MAX_TITLE_LENGTH

    /**
     * Why Post cannot be pressed, or null when it can.
     *
     * Ordered so the FIRST blocker the user should fix is the one reported:
     * telling someone their post is empty while also complaining about alt text
     * is noise.
     */
    val blockedReason: PostBlockedReason?
        get() = when {
            isBusy -> PostBlockedReason.Busy
            text.isBlank() && !hasImage -> PostBlockedReason.Empty
            titleMissing -> PostBlockedReason.MissingTitle
            textTooLong || titleTooLong -> PostBlockedReason.TextTooLong
            !altDecisionMade -> PostBlockedReason.MissingAltDecision
            hasImage && mediaId == null -> PostBlockedReason.MediaNotReady
            else -> null
        }

    val canPost: Boolean get() = blockedReason == null
}

/**
 * The server's ceiling, in Unicode CODE POINTS.
 *
 * Code points, not `String.length`. Kotlin's `length` counts UTF-16 units, so
 * an emoji costs two and a Devanagari string can exceed the limit long before
 * the server thinks it has. Counting the same unit the server counts is what
 * makes the two agree on the boundary instead of disagreeing by script.
 */
const val MAX_TEXT_CODE_POINTS = 5000

/**
 * A ceiling for the article title. Post-service binds `title` with no
 * length rule of its own, so this is a product cap — a headline, not a
 * paragraph — and it is counted in UTF-16 units because that is what the
 * field's own length is.
 */
const val MAX_TITLE_LENGTH = 200

/** Largest image the media service accepts. */
const val MAX_IMAGE_BYTES = 20L * 1024L * 1024L

/** MIME types the composer will upload. Static images only in this slice. */
val SupportedImageMimeTypes: Set<String> = setOf("image/jpeg", "image/png", "image/webp")

const val DEFAULT_LANGUAGE = "en"
