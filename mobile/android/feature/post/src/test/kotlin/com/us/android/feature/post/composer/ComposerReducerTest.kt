package com.us.android.feature.post.composer

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The composer state machine — C-LB-7.2.
 *
 * Every case here is a data-loss, duplicate-post or false-success bug when the
 * transition is wrong. Double-tap creates two posts; a retry that rebuilds its
 * payload is a 409 the user cannot resolve; an edit that keeps the old key is
 * the same; "published" shown before the server answered is a lie the user acts
 * on by closing the screen.
 */
class ComposerReducerTest {

    private val editing = ComposerUiState()
    private var keysMinted = 0
    private val newKey: () -> String = {
        keysMinted++
        "key-$keysMinted"
    }

    private fun withImage(mediaReady: Boolean = true) = ComposerUiState(
        text = "look at this",
        imageUri = "content://pick/1",
        altText = "a cat asleep on a keyboard",
        mediaId = if (mediaReady) "media-1" else null,
    )

    // ── Validation and the Post gate ────────────────────────────────────

    @Test
    fun `an empty composer cannot post and says why`() {
        assertThat(editing.canPost).isFalse()
        assertThat(editing.blockedReason).isEqualTo(PostBlockedReason.Empty)
    }

    @Test
    fun `whitespace-only text is empty`() {
        val state = editing.copy(text = "   \n\t  ")
        assertThat(state.canPost).isFalse()
        assertThat(state.blockedReason).isEqualTo(PostBlockedReason.Empty)
    }

    @Test
    fun `text alone is postable`() {
        assertThat(editing.copy(text = "hello").canPost).isTrue()
    }

    /**
     * Counted in CODE POINTS, matching the server.
     *
     * `String.length` counts UTF-16 units, so an emoji costs two and a limit
     * enforced on `length` would reject a legal post in some scripts and accept
     * an illegal one in others. The boundary is asserted exactly.
     */
    @Test
    fun `the text ceiling is counted in code points at the exact boundary`() {
        assertThat(MAX_TEXT_CODE_POINTS).isEqualTo(5000)
        assertThat(editing.copy(text = "x".repeat(5000)).canPost).isTrue()
        assertThat(editing.copy(text = "x".repeat(5001)).blockedReason)
            .isEqualTo(PostBlockedReason.TextTooLong)

        // 2,600 astral emoji are 2,600 code points and 5,200 UTF-16 units. A
        // length-based limit would wrongly reject this.
        val emoji = "😀".repeat(2600)
        assertThat(emoji.length).isGreaterThan(MAX_TEXT_CODE_POINTS)
        assertThat(editing.copy(text = emoji).canPost).isTrue()
    }

    @Test
    fun `an image with no accessibility decision blocks posting`() {
        val state = ComposerUiState(imageUri = "content://pick/1", mediaId = "m1")
        assertThat(state.blockedReason).isEqualTo(PostBlockedReason.MissingAltDecision)
    }

    @Test
    fun `marking an image decorative satisfies the accessibility decision`() {
        val state = ComposerUiState(imageUri = "content://pick/1", mediaId = "m1", decorative = true)
        assertThat(state.canPost).isTrue()
    }

    /** Server enforcement is authoritative, but Post is not offered before ready. */
    @Test
    fun `an image that is not ready blocks posting`() {
        assertThat(withImage(mediaReady = false).blockedReason)
            .isEqualTo(PostBlockedReason.MediaNotReady)
    }

    // ── Single-flight ───────────────────────────────────────────────────

    /**
     * A double tap must not produce two publishes.
     *
     * The guard lives in the reducer rather than the ViewModel so it is the
     * same logic under test as in production.
     */
    @Test
    fun `pressing post twice mints one key and stays in one publish`() {
        val first = ComposerReducer.onPostPressed(editing.copy(text = "hi"), newKey)
        val second = ComposerReducer.onPostPressed(first, newKey)

        assertThat(keysMinted).isEqualTo(1)
        assertThat(second.frozen).isEqualTo(first.frozen)
        assertThat(second.phase).isInstanceOf(ComposerPhase.Publishing::class.java)
    }

    @Test
    fun `pressing post while invalid surfaces validation instead of publishing`() {
        val result = ComposerReducer.onPostPressed(editing, newKey)

        assertThat(result.phase).isEqualTo(ComposerPhase.Editing)
        assertThat(result.showValidationErrors).isTrue()
        assertThat(keysMinted).isEqualTo(0)
    }

    // ── Retry semantics ─────────────────────────────────────────────────

    /**
     * A retry sends the IDENTICAL bytes under the IDENTICAL key.
     *
     * That is what lets the server replay the post it already created rather
     * than creating a second one. Rebuilding the request here would defeat the
     * durable idempotency entirely.
     */
    @Test
    fun `a retry after a retryable failure reuses the frozen payload and key`() {
        val published = ComposerReducer.onPostPressed(editing.copy(text = "hi"), newKey)
        val failed = ComposerReducer.onPublishFailed(published, "network")
        val retried = ComposerReducer.onPostPressed(failed, newKey)

        assertThat(keysMinted).isEqualTo(1)
        assertThat(retried.frozen).isEqualTo(published.frozen)
        assertThat(retried.frozen?.request?.text).isEqualTo("hi")
    }

    /**
     * Editing after a failure MUST rotate the key.
     *
     * Same key + different bytes is `409 IDEMPOTENCY_KEY_REUSED`, and the user
     * would see their edited post refused with no way to understand why.
     */
    @Test
    fun `editing after a failure drops the frozen payload so a new key is minted`() {
        val published = ComposerReducer.onPostPressed(editing.copy(text = "hi"), newKey)
        val failed = ComposerReducer.onPublishFailed(published, "network")

        val editable = ComposerReducer.onEditAfterFailure(failed)
        assertThat(editable.frozen).isNull()
        assertThat(editable.phase).isEqualTo(ComposerPhase.Editing)

        val edited = ComposerEdits.onTextChanged(editable, "hi, edited")
        val republished = ComposerReducer.onPostPressed(edited, newKey)

        assertThat(keysMinted).isEqualTo(2)
        assertThat(republished.frozen!!.creationKey).isNotEqualTo(published.frozen!!.creationKey)
        assertThat(republished.frozen!!.request.text).isEqualTo("hi, edited")
    }

    /** A terminal failure is a new intent, so it also rotates the key. */
    @Test
    fun `a terminal publish failure discards the frozen payload`() {
        val published = ComposerReducer.onPostPressed(editing.copy(text = "hi"), newKey)
        val rejected = ComposerReducer.onPublishRejected(published, "media rejected")

        assertThat(rejected.frozen).isNull()
        assertThat(rejected.phase).isInstanceOf(ComposerPhase.TerminalFailure::class.java)
    }

    // ── Editing is refused while in flight or after success ─────────────

    @Test
    fun `text cannot change while publishing`() {
        val publishing = ComposerReducer.onPostPressed(editing.copy(text = "hi"), newKey)
        assertThat(ComposerEdits.onTextChanged(publishing, "sneaky").text).isEqualTo("hi")
    }

    @Test
    fun `text cannot change after publishing succeeded`() {
        val done = ComposerReducer.onPublishSucceeded(editing.copy(text = "hi"), "post-1")
        assertThat(ComposerEdits.onTextChanged(done, "after the fact").text).isEqualTo("hi")
    }

    // ── Media transitions ───────────────────────────────────────────────

    /**
     * A new image clears the previous description.
     *
     * Carrying it over would describe one photo with another photo's alt text —
     * worse than none, because it reads as deliberate to a screen-reader user.
     */
    @Test
    fun `picking a new image clears the previous accessibility decision`() {
        val replaced = ComposerEdits.onImagePicked(withImage(), "content://pick/2")

        assertThat(replaced.altText).isEmpty()
        assertThat(replaced.decorative).isFalse()
        assertThat(replaced.mediaId).isNull()
        assertThat(replaced.phase).isEqualTo(ComposerPhase.PreparingImage)
    }

    @Test
    fun `typing alt text clears the decorative mark`() {
        val decorative = ComposerUiState(imageUri = "u", decorative = true)
        val described = ComposerEdits.onAltTextChanged(decorative, "a sunset")

        assertThat(described.decorative).isFalse()
        assertThat(described.altText).isEqualTo("a sunset")
    }

    @Test
    fun `marking decorative clears any typed alt text`() {
        val described = ComposerUiState(imageUri = "u", altText = "a sunset")
        val decorative = ComposerEdits.onDecorativeChanged(described, true)

        assertThat(decorative.altText).isEmpty()
        assertThat(decorative.decorative).isTrue()
    }

    /** A rejected image is removed, not left with a Retry that cannot work. */
    @Test
    fun `a rejected image is dropped and reported terminally`() {
        val rejected = ComposerUpload.onMediaRejected(withImage(), "failed moderation")

        assertThat(rejected.imageUri).isNull()
        assertThat(rejected.mediaId).isNull()
        assertThat(rejected.phase).isInstanceOf(ComposerPhase.TerminalFailure::class.java)
    }

    @Test
    fun `upload progress is reported and reaches ready`() {
        val uploading = ComposerUpload.onUploadProgress(withImage(mediaReady = false), 50, 200)
        assertThat((uploading.phase as ComposerPhase.Uploading).fraction).isWithin(0.001f).of(0.25f)

        val confirming = ComposerUpload.onUploadConfirming(uploading)
        assertThat(confirming.phase).isEqualTo(ComposerPhase.Confirming)

        val ready = ComposerUpload.onMediaReady(confirming, "media-9")
        assertThat(ready.mediaId).isEqualTo("media-9")
        assertThat(ready.canPost).isTrue()
    }

    /** A failed upload is retryable and the draft survives it. */
    @Test
    fun `a failed upload keeps the draft`() {
        val failed = ComposerUpload.onUploadFailed(withImage(mediaReady = false), "offline")

        assertThat(failed.phase).isInstanceOf(ComposerPhase.RetryableFailure::class.java)
        assertThat(failed.text).isEqualTo("look at this")
        assertThat(failed.imageUri).isNotNull()
    }

    // ── Discard ─────────────────────────────────────────────────────────

    @Test
    fun `discarding an empty composer needs no confirmation`() {
        assertThat(ComposerReducer.onDiscardRequested(editing).confirmingDiscard).isFalse()
    }

    @Test
    fun `discarding real content asks first`() {
        val state = ComposerReducer.onDiscardRequested(editing.copy(text = "half a thought"))
        assertThat(state.confirmingDiscard).isTrue()
        assertThat(ComposerReducer.onDiscardCancelled(state).confirmingDiscard).isFalse()
    }

    // ── The frozen request ──────────────────────────────────────────────

    @Test
    fun `a text post freezes as post_type text with no media`() {
        val frozen = ComposerReducer.onPostPressed(editing.copy(text = " hi "), newKey).frozen!!

        // Trimmed on the way out: trailing whitespace is not content.
        assertThat(frozen.request.text).isEqualTo("hi")
        assertThat(frozen.request.postType).isEqualTo("text")
        assertThat(frozen.request.mediaIds).isEmpty()
        assertThat(frozen.request.visibility).isEqualTo("public")
    }

    @Test
    fun `an image post freezes as post_type image with exactly one media id`() {
        val frozen = ComposerReducer.onPostPressed(withImage(), newKey).frozen!!

        assertThat(frozen.request.postType).isEqualTo("image")
        assertThat(frozen.request.mediaIds).containsExactly("media-1")
    }

    /** Slice C never sends more than one attachment. */
    @Test
    fun `the request can never carry more than one media id`() {
        val frozen = ComposerReducer.onPostPressed(withImage(), newKey).frozen!!
        assertThat(frozen.request.mediaIds.size).isAtMost(1)
    }
}
