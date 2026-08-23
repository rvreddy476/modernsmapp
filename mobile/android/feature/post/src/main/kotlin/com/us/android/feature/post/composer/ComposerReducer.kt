package com.us.android.feature.post.composer

import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.feature.post.data.dto.POST_TYPE_IMAGE
import com.us.android.feature.post.data.dto.POST_TYPE_TEXT

/**
 * Field edits: the transitions that change what the post SAYS.
 *
 * Split from [ComposerReducer] along a real seam rather than to satisfy a
 * threshold. These six are pure field mutations gated on editability; the
 * publish lifecycle next door is about idempotency keys, frozen payloads and
 * failure classification. They are read, changed and reasoned about separately.
 */
object ComposerEdits {

    /** Text edits are only accepted while editable — see [isEditable]. */
    fun onTextChanged(state: ComposerUiState, text: String): ComposerUiState =
        if (!isEditable(state)) state else state.copy(text = text)

    fun onImagePicked(state: ComposerUiState, uri: String): ComposerUiState =
        if (!isEditable(state)) {
            state
        } else {
            state.copy(
                imageUri = uri,
                // A new image invalidates everything decided about the old one.
                // Carrying the previous alt text over would silently describe
                // one photo with another photo's description — worse than
                // having none, because it reads as deliberate.
                altText = "",
                decorative = false,
                mediaId = null,
                phase = ComposerPhase.PreparingImage,
            )
        }

    fun onImageRemoved(state: ComposerUiState): ComposerUiState =
        if (!isEditable(state)) {
            state
        } else {
            state.copy(
                imageUri = null,
                altText = "",
                decorative = false,
                mediaId = null,
                phase = ComposerPhase.Editing,
            )
        }

    fun onAltTextChanged(state: ComposerUiState, alt: String): ComposerUiState =
        if (!isEditable(state)) {
            state
        } else {
            // Typing a description is an implicit "this is not decorative".
            // Leaving both set would send a described image marked as carrying
            // no information, and screen readers would skip the description.
            state.copy(altText = alt, decorative = if (alt.isNotBlank()) false else state.decorative)
        }

    fun onDecorativeChanged(state: ComposerUiState, decorative: Boolean): ComposerUiState =
        if (!isEditable(state)) {
            state
        } else {
            state.copy(decorative = decorative, altText = if (decorative) "" else state.altText)
        }

    fun onLanguageChanged(state: ComposerUiState, language: String): ComposerUiState =
        if (!isEditable(state)) state else state.copy(language = language)
}

/**
 * The publish lifecycle: upload progress, the frozen payload and the creation key.
 *
 * This is where the duplicate-post and data-loss decisions live. Kept apart from
 * [ComposerEdits] because the questions are different — that one asks "may this
 * field change?", this one asks "is this the same intent as last time?".
 *
 * Pure and Android-free on purpose: testing double-tap, retry-after-failure and
 * media rejection through a ViewModel would mean testing them through a
 * coroutine scope and a DI graph. Here they are a table.
 */
/**
 * The image upload lifecycle.
 *
 * The third seam: [ComposerEdits] asks "may this field change?",
 * [ComposerReducer] asks "is this the same publish intent?", and this asks
 * "where has the image got to?". Upload failures and moderation rejections are
 * classified here because they are about the ASSET, not about the post.
 */
object ComposerUpload {

    fun onUploadProgress(state: ComposerUiState, uploaded: Long, total: Long): ComposerUiState =
        state.copy(phase = ComposerPhase.Uploading(uploaded, total))

    fun onUploadConfirming(state: ComposerUiState): ComposerUiState =
        state.copy(phase = ComposerPhase.Confirming)

    /** The image is confirmed and `ready`; only now may it be attached. */
    fun onMediaReady(state: ComposerUiState, mediaId: String): ComposerUiState =
        state.copy(mediaId = mediaId, phase = ComposerPhase.Editing)

    /**
     * Moderation or processing refused the image.
     *
     * Terminal, and the image is dropped: leaving it on screen with a Retry
     * would invite the user to keep pressing a button that cannot succeed.
     */
    fun onMediaRejected(state: ComposerUiState, reason: String): ComposerUiState =
        state.copy(
            imageUri = null,
            mediaId = null,
            altText = "",
            decorative = false,
            phase = ComposerPhase.TerminalFailure(reason),
        )

    fun onUploadFailed(state: ComposerUiState, message: String): ComposerUiState =
        state.copy(phase = ComposerPhase.RetryableFailure(message))
}

/**
 * The publish lifecycle: the frozen payload, the creation key and failure
 * classification.
 *
 * This is where the duplicate-post and data-loss decisions live. Pure and
 * Android-free on purpose: testing double-tap and retry-after-failure through a
 * ViewModel would mean testing them through a coroutine scope and a DI graph.
 * Here they are a table.
 */
object ComposerReducer {

    /**
     * Press Post.
     *
     * Returns the state unchanged when it must not proceed — that IS the
     * single-flight guard, and it is here rather than in the ViewModel so a
     * double tap is rejected by the same logic the tests exercise.
     *
     * Freezes the payload and mints the creation key exactly once. A retry
     * reuses the existing [FrozenPublish]; it never rebuilds it, because
     * rebuilt bytes under the same key are a 409 the user cannot resolve.
     */
    fun onPostPressed(state: ComposerUiState, newKey: () -> String): ComposerUiState {
        if (state.isBusy) return state
        // A published composer is finished. Without this, pressing Post again
        // after success starts a SECOND publish — the server replays it under
        // the same key so no duplicate row appears, but the client is issuing a
        // write it has no reason to issue, and any future weakening of the
        // server guarantee would turn it into a duplicate post.
        if (state.phase is ComposerPhase.Published) return state
        if (!state.canPost) return state.copy(showValidationErrors = true)

        val frozen = state.frozen ?: FrozenPublish(
            creationKey = newKey(),
            request = buildRequest(state),
        )
        return state.copy(frozen = frozen, phase = ComposerPhase.Publishing, showValidationErrors = false)
    }

    fun onPublishSucceeded(state: ComposerUiState, postId: String): ComposerUiState =
        state.copy(phase = ComposerPhase.Published(postId))

    /**
     * A publish failed but may work later.
     *
     * The frozen payload and key are KEPT. That is what makes the retry safe:
     * identical bytes and an identical key mean the server either creates the
     * post once or replays the one it already created.
     */
    fun onPublishFailed(state: ComposerUiState, message: String): ComposerUiState =
        state.copy(phase = ComposerPhase.RetryableFailure(message))

    /**
     * A publish failed in a way retrying cannot fix.
     *
     * The frozen payload is DISCARDED so the next attempt is a genuinely new
     * intent with a new key — a rejected payload replayed under the same key
     * would be refused forever.
     */
    fun onPublishRejected(state: ComposerUiState, message: String): ComposerUiState =
        state.copy(frozen = null, phase = ComposerPhase.TerminalFailure(message))

    /**
     * The user edited after a failure.
     *
     * Editing makes the frozen bytes stale, so the frozen operation is dropped
     * and the next Post mints a NEW key. Reusing the old key with new text is
     * exactly the 409 case, and the user would have no way to understand it.
     */
    fun onEditAfterFailure(state: ComposerUiState): ComposerUiState =
        when (state.phase) {
            is ComposerPhase.RetryableFailure,
            is ComposerPhase.TerminalFailure,
            -> state.copy(frozen = null, phase = ComposerPhase.Editing)

            else -> state
        }

    fun onDiscardRequested(state: ComposerUiState): ComposerUiState =
        state.copy(confirmingDiscard = state.hasContent)

    fun onDiscardCancelled(state: ComposerUiState): ComposerUiState =
        state.copy(confirmingDiscard = false)

    /**
     * Builds the wire request from the current draft.
     *
     * `post_type` mirrors what is actually attached, so an image post is never
     * announced as text. `content_type` stays the canonical `post` — Slice C
     * adds no new semantics to either field.
     */
    fun buildRequest(state: ComposerUiState): CreatePostRequest = CreatePostRequest(
        text = state.text.trim(),
        postType = if (state.mediaId != null) POST_TYPE_IMAGE else POST_TYPE_TEXT,
        mediaIds = listOfNotNull(state.mediaId),
        language = state.language,
        distribution = DistributionRequest(),
    )
}

/**
 * Editable only when nothing is in flight and no publish has succeeded.
 *
 * A published composer is finished: accepting edits afterwards would let
 * someone type into a screen whose content is already public.
 *
 * Top-level because both objects need it — the edits object to gate fields, and
 * the publish object to decide whether a post-failure edit is allowed.
 */
internal fun isEditable(state: ComposerUiState): Boolean =
    !state.isBusy && state.phase !is ComposerPhase.Published
