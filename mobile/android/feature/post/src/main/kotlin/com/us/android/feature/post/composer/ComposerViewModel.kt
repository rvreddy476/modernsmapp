package com.us.android.feature.post.composer

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.MEDIA_MODERATION_PASSED
import com.us.android.core.media.upload.MEDIA_MODERATION_REJECTED
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PROCESSING_FAILED
import com.us.android.core.media.upload.PROCESSING_READY
import com.us.android.core.media.upload.PROCESSING_REJECTED
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.feature.post.data.ComposerRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject

/** What the screen needs from the platform to read a picked image. */
typealias ImageSourceResolver = MediaSourceResolver
typealias PickedImage = PickedMedia

/** Mints creation keys. Injected so a test can make them deterministic. */
fun interface CreationKeyFactory {
    fun newKey(): String
}

/**
 * Orchestrates the composer: one state, one publish, one creation key.
 *
 * ## WHAT LIVES HERE AND WHAT DOES NOT
 *
 * Meaning lives in the pure reducers, which are table-tested. This class owns
 * effects — network, persistence, the clock, and the single job that must not
 * run twice. The screen owns neither: it renders [state] and calls back.
 *
 * ## DURABILITY (C-P0-3)
 *
 * [ComposerDraftStore] (Room) is the authority. It survives a navigation pop,
 * which `SavedStateHandle` does not — and the thing that must survive is not
 * only the text but the CREATION KEY, because that key is what stops a retry
 * publishing a post the server already committed.
 *
 * ## READINESS (C-P0-2)
 *
 * An asset is attachable only at exact `processing_status=ready` AND
 * `moderation_status=passed`. `confirm` usually returns before the verdict
 * exists, so the ViewModel POLLS the status endpoint rather than treating
 * "not rejected" as ready. The old code called `onMediaReady` in a final `else`,
 * which made `pending`, `processing`, `failed` and every unknown value
 * attachable client-side.
 */
@HiltViewModel
class ComposerViewModel @Inject constructor(
    private val repository: ComposerRepository,
    private val uploader: MediaUploader,
    private val imageSources: ImageSourceResolver,
    private val drafts: ComposerDraftStore,
    private val keys: CreationKeyFactory,
) : ViewModel() {

    private val _state = MutableStateFlow(ComposerUiState())
    val state: StateFlow<ComposerUiState> = _state.asStateFlow()

    /** The one in-flight effect. A second Post cannot start a second publish. */
    private var inFlight: Job? = null

    init {
        viewModelScope.launch {
            drafts.load()?.let { restored ->
                _state.value = restored
                // Resume whatever the process death interrupted. Without this
                // the user comes back to an image that will never become
                // attachable, with Post disabled as MediaNotReady and no Retry
                // control offered — stuck, with removing and reselecting the
                // photo as the only escape.
                if (restored.hasImage && restored.mediaId == null) {
                    restored.imageUri?.let(::startUpload)
                }
            }
        }
    }

    // ── Edits ───────────────────────────────────────────────────────────

    fun onTextChanged(text: String) = update {
        ComposerEdits.onTextChanged(ComposerReducer.onEditAfterFailure(it), text)
    }

    fun onVisibilityChanged(visibility: String) = update {
        ComposerEdits.onVisibilityChanged(it, visibility)
    }

    fun onLanguageChanged(language: String) = update {
        ComposerEdits.onLanguageChanged(it, language)
    }

    fun onAltTextChanged(alt: String) = update { ComposerEdits.onAltTextChanged(it, alt) }

    fun onDecorativeChanged(decorative: Boolean) = update {
        ComposerEdits.onDecorativeChanged(it, decorative)
    }

    fun onImageRemoved() = update { ComposerEdits.onImageRemoved(it) }

    fun onImagePicked(uri: String) {
        update { ComposerEdits.onImagePicked(it, uri) }
        startUpload(uri)
    }

    // ── Upload ──────────────────────────────────────────────────────────

    /**
     * Client-side pre-checks that mirror the server's.
     *
     * They save the user a multi-megabyte upload on mobile data; they do not
     * replace enforcement, which stays on the server.
     */
    private fun readPickedImage(uri: String): PickedImage? {
        val picked = imageSources.resolve(uri)
        val rejection = when {
            picked == null -> "That image could not be read."
            picked.mimeType !in SupportedImageMimeTypes ->
                "Only JPEG, PNG and WebP images can be posted."

            picked.sizeBytes > MAX_IMAGE_BYTES -> "That image is larger than 20 MB."
            else -> null
        }
        if (rejection != null) {
            update { ComposerUpload.onMediaRejected(it, rejection) }
            return null
        }
        return picked
    }

    private fun startUpload(uri: String) {
        if (inFlight?.isActive == true) return
        inFlight = viewModelScope.launch {
            val picked = readPickedImage(uri) ?: return@launch

            val init = when (val reserved = reserve(picked)) {
                null -> return@launch
                else -> reserved
            }

            if (!pushBytes(init.uploadUrl, picked)) return@launch

            update { ComposerUpload.onUploadConfirming(it) }
            when (uploader.confirm(init.mediaId)) {
                is AppResult.Failure -> {
                    update { ComposerUpload.onUploadFailed(it, "The upload could not be confirmed.") }
                    return@launch
                }

                is AppResult.Success -> awaitReady(init.mediaId)
            }
        }
    }

    /**
     * `init`, carrying the composer lease.
     *
     * The lease is what makes this asset eligible for server GC if the user
     * abandons it. Without it an abandoned composer image is stored forever;
     * with it, and only with it, the sweep may reclaim a CONFIRMED asset.
     *
     * Alt text is NOT sent here. See [publish] — the accessibility decision is
     * made after the picker returns, so anything sent now would be the empty
     * placeholder, and the server would keep it.
     */
    private suspend fun reserve(picked: PickedImage) = when (
        val result = uploader.reserve(
            mimeType = picked.mimeType,
            sizeBytes = picked.sizeBytes,
        )
    ) {
        is AppResult.Success -> result.data
        is AppResult.Failure -> {
            update { ComposerUpload.onUploadFailed(it, "The upload could not start.") }
            null
        }
    }

    private suspend fun pushBytes(url: String, picked: PickedImage): Boolean =
        when (
            uploader.upload(
                uploadUrl = url,
                mimeType = picked.mimeType,
                sizeBytes = picked.sizeBytes,
                source = picked.source,
            ) { uploaded, total -> update { ComposerUpload.onUploadProgress(it, uploaded, total) } }
        ) {
            is PresignedPutResult.Success -> true

            // An expired URL can never succeed on retry: a NEW init is required,
            // and the abandoned asset is left for server GC.
            is PresignedPutResult.UrlExpired -> {
                update { ComposerUpload.onUploadFailed(it, "The upload link expired.") }
                false
            }

            is PresignedPutResult.Failed -> {
                update { ComposerUpload.onUploadFailed(it, "The image did not finish uploading.") }
                false
            }
        }

    /**
     * Polls until the asset is EXACTLY ready and passed, or terminally not.
     *
     * `confirm` returns as soon as the bytes are accepted; the safety verdict
     * lands afterwards. The previous implementation treated every non-rejected
     * confirmation as ready, so `pending`, `processing`, `failed` and any
     * unknown value became attachable — and the server would then refuse the
     * post with an error the user could not act on.
     *
     * Bounded: an asset that never reaches a verdict is a failure, not an
     * indefinite spinner.
     */
    private suspend fun awaitReady(mediaId: String) {
        repeat(READINESS_POLLS) { attempt ->
            when (val status = uploader.status(mediaId)) {
                is AppResult.Failure -> {
                    update { ComposerUpload.onUploadFailed(it, "We couldn't check that image.") }
                    return
                }

                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus

                    // Terminal-bad first: a rejected or failed asset can never
                    // become ready, so offering a Retry would be a button that
                    // cannot work.
                    if (processing == PROCESSING_REJECTED ||
                        processing == PROCESSING_FAILED ||
                        moderation == MEDIA_MODERATION_REJECTED
                    ) {
                        update { ComposerUpload.onMediaRejected(it, "That image can't be posted.") }
                        return
                    }

                    // Terminal-good: EXACT ready AND passed. Nothing else.
                    if (processing == PROCESSING_READY && moderation == MEDIA_MODERATION_PASSED) {
                        update { ComposerUpload.onMediaReady(it, mediaId) }
                        return
                    }
                }
            }
            if (attempt < READINESS_POLLS - 1) delay(READINESS_POLL_MILLIS)
        }
        update {
            ComposerUpload.onUploadFailed(it, "That image is taking too long to process.")
        }
    }

    // ── Publish ─────────────────────────────────────────────────────────

    /**
     * Press Post.
     *
     * Single-flight twice over: the reducer refuses while busy, and the job
     * guard refuses a second launch. Duplicating a post is the most visible
     * failure this screen can produce.
     */
    fun onPostPressed() {
        val after = ComposerReducer.onPostPressed(_state.value, keys::newKey)
        _state.value = after
        persist(after)

        if (after.phase !is ComposerPhase.Publishing) return
        if (inFlight?.isActive == true) return

        val frozen = after.frozen ?: return
        inFlight = viewModelScope.launch { publish(frozen, after) }
    }

    private suspend fun publish(frozen: FrozenPublish, snapshot: ComposerUiState) {
        // THE FINAL ACCESSIBILITY DECISION, WRITTEN BEFORE THE POST EXISTS
        // (C-P0-2).
        //
        // The decision is made after the picker returns, by which time `init`
        // has already run — so whatever `init` stored is a placeholder. Sending
        // it now, owner-authenticated, is what makes the value the server keeps
        // match the value the composer required and displayed.
        //
        // Before the create, not after: a post that exists with the wrong alt
        // text is already wrong for anyone who reads it in that window, and a
        // failure here must not leave a published post behind.
        snapshot.mediaId?.let { mediaId ->
            if (!uploader.updateAccessibility(mediaId, snapshot.altText, snapshot.decorative)) {
                update {
                    ComposerUpload.onUploadFailed(
                        it,
                        "We couldn't save the photo description.",
                    )
                }
                return
            }
        }

        when (val result = repository.createPost(frozen.creationKey, frozen.request)) {
            is AppResult.Success -> {
                // Set the state WITHOUT persisting, then clear.
                //
                // `update` writes the draft on a launched coroutine, so a
                // success that went through it could have its write land AFTER
                // the clear — resurrecting the draft of a post that is already
                // published, which the next launch would restore. Publishing
                // means the draft is finished; deleting it IS the write.
                _state.value = ComposerReducer.onPublishSucceeded(_state.value, result.data)
                drafts.clear()
            }

            is AppResult.Failure -> {
                if (repository.isTerminal(result.error)) {
                    update { ComposerReducer.onPublishRejected(it, repository.message(result.error)) }
                } else {
                    update { ComposerReducer.onPublishFailed(it, repository.message(result.error)) }
                }
            }
        }
    }

    fun onRetry() {
        val current = _state.value
        // A failed upload retries the upload; a failed publish retries the
        // publish. Same button, and the state says which.
        if (current.hasImage && current.mediaId == null) {
            current.imageUri?.let(::startUpload)
        } else {
            onPostPressed()
        }
    }

    // ── Discard ─────────────────────────────────────────────────────────

    fun onDiscardRequested() = update { ComposerReducer.onDiscardRequested(it) }

    fun onDiscardCancelled() = update { ComposerReducer.onDiscardCancelled(it) }

    /**
     * Throws the draft away, THEN lets the screen leave — C-CLB-2.
     *
     * # THE ORDERING IS THE WHOLE POINT
     *
     * This used to clear the state and launch `drafts.clear()`, while the
     * screen called `onClose()` on the same tap. Popping the destination clears
     * the navigation-owned ViewModel and cancels `viewModelScope`, so the Room
     * delete raced the pop and frequently lost. The draft survived an explicit
     * discard and came back the next time the composer opened — the user threw
     * something away and the app kept it.
     *
     * So navigation is now downstream of the delete: `discarded` is set only
     * after `clear()` has returned, and the screen watches that flag. Same
     * shape as publish, where the screen also moves only once the durable
     * outcome is real.
     *
     * The asset delete stays best-effort and is deliberately AFTER the signal:
     * it is a network call, and blocking a discard tap on it would make the
     * screen hang on a bad connection. An app that is force-stopped or
     * uninstalled never makes that call either, which is why it can never be
     * the authority.
     *
     * Note that with confirmed-media GC disabled (C-CLB-1) this call is now the
     * main way an abandoned confirmed upload gets removed. It is still not a
     * guarantee, and the retention that follows from missing it is the accepted
     * cost recorded there.
     */
    fun onDiscardConfirmed() {
        val mediaId = _state.value.mediaId
        _state.value = ComposerUiState()
        viewModelScope.launch {
            drafts.clear()
            // Durably gone. Only now may the screen navigate.
            _state.value = _state.value.copy(discarded = true)
            mediaId?.let { uploader.discard(it) }
        }
    }

    // ── State plumbing ──────────────────────────────────────────────────

    private inline fun update(block: (ComposerUiState) -> ComposerUiState) {
        val next = block(_state.value)
        _state.value = next
        persist(next)
    }

    private fun persist(state: ComposerUiState) {
        viewModelScope.launch { drafts.save(state, System.currentTimeMillis()) }
    }

    private companion object {
        /**
         * How long to wait for a safety verdict before calling it a failure.
         *
         * Image processing is synchronous server-side, so this normally
         * resolves on the first or second poll. The bound exists so a stuck
         * asset becomes an actionable error instead of a spinner the user
         * stares at.
         */
        const val READINESS_POLLS = 20
        const val READINESS_POLL_MILLIS = 500L
    }
}

/** Production keys are random UUIDs. */
class RandomCreationKeyFactory @Inject constructor() : CreationKeyFactory {
    override fun newKey(): String = UUID.randomUUID().toString()
}
