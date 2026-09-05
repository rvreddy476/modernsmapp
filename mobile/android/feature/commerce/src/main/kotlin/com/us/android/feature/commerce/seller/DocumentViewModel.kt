package com.us.android.feature.commerce.seller

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.SellerDocument
import com.us.android.core.commerce.model.SellerDocumentType
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.feature.commerce.ui.describe
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Sending an identity document for review.
 *
 * Four steps, and every one of them can fail in a way the seller has to know
 * about: reserve a media row, push the bytes, confirm them, then attach the
 * resulting id to the shop. The last step is the one that matters — the server
 * verifies the media id belongs to THIS caller, is ready, and has passed
 * moderation, so an upload that succeeded can still be refused at attach time
 * and the seller must be told why rather than shown a spinner that stops.
 */
data class DocumentUploadState(
    val type: SellerDocumentType = SellerDocumentType.PAN_CARD,
    val documentNumber: String = "",
    /** Bytes pushed so far, over the total. Null when nothing is in flight. */
    val progress: Pair<Long, Long>? = null,
    val stage: Stage = Stage.Idle,
    val error: String? = null,
) {
    enum class Stage {
        Idle,

        /** Reserving the row and getting a presigned URL. */
        Starting,

        /** Pushing the bytes. */
        Uploading,

        /** Bytes are there; the server is registering them. */
        Confirming,

        /** Attaching the media id to the shop, where ownership is verified. */
        Attaching,

        Done,
    }

    val busy: Boolean get() = stage != Stage.Idle && stage != Stage.Done

    /**
     * The document number is optional.
     *
     * A reviewer reads the number off the document itself. Demanding it typed
     * as well adds a transcription error to a check that already has the
     * original in front of it.
     */
    val canPick: Boolean get() = !busy
}

@HiltViewModel
class DocumentViewModel @Inject constructor(
    private val repo: CommerceRepository,
    private val uploader: MediaUploader,
    private val sources: MediaSourceResolver,
) : ViewModel() {

    private val _state = MutableStateFlow(DocumentUploadState())
    val state: StateFlow<DocumentUploadState> = _state.asStateFlow()

    private var inFlight: Job? = null

    fun setType(type: SellerDocumentType) = edit { it.copy(type = type, error = null) }

    fun setNumber(raw: String) = edit {
        it.copy(documentNumber = raw.uppercase().take(MAX_DOC_NUMBER), error = null)
    }

    /**
     * Runs the whole flow for one picked file.
     *
     * Guarded against a second start while one is running: a double tap on the
     * picker result would reserve two media rows, and the abandoned one sits
     * in the store until the server's sweep reclaims it.
     */
    fun upload(uri: String, onAttached: () -> Unit) {
        if (inFlight?.isActive == true) return
        inFlight = viewModelScope.launch { run(uri, onAttached) }
    }

    private suspend fun run(uri: String, onAttached: () -> Unit) {
        val picked = readPicked(uri) ?: return

        edit { it.copy(stage = DocumentUploadState.Stage.Starting, error = null, progress = null) }
        val init = when (val r = uploader.reserve(mimeType = picked.mimeType, sizeBytes = picked.sizeBytes)) {
            is AppResult.Success -> r.data
            is AppResult.Failure -> return fail("The upload could not start.")
        }

        edit { it.copy(stage = DocumentUploadState.Stage.Uploading) }
        when (
            uploader.upload(
                uploadUrl = init.uploadUrl,
                mimeType = picked.mimeType,
                sizeBytes = picked.sizeBytes,
                source = picked.source,
            ) { sent, total -> edit { it.copy(progress = sent to total) } }
        ) {
            is PresignedPutResult.Success -> Unit

            // An expired link can never succeed on retry — a new reservation
            // is required — so it is said differently from a transport failure
            // the seller can simply try again.
            is PresignedPutResult.UrlExpired ->
                return fail("The upload link expired. Please choose the file again.")

            is PresignedPutResult.Failed ->
                return fail("The document did not finish uploading.")
        }

        edit { it.copy(stage = DocumentUploadState.Stage.Confirming, progress = null) }
        if (uploader.confirm(init.mediaId) is AppResult.Failure) {
            return fail("The upload could not be confirmed.")
        }

        // Attach. The server verifies the media id belongs to this caller, is
        // ready, and has passed moderation — so this can refuse even though
        // every step above succeeded, and the seller is told which.
        edit { it.copy(stage = DocumentUploadState.Stage.Attaching) }
        val document = SellerDocument(
            type = _state.value.type,
            mediaId = init.mediaId,
            documentNumber = _state.value.documentNumber,
        )
        when (val r = repo.saveDocuments(listOf(document))) {
            is CommerceResult.Failure -> fail(r.error.describe())

            is CommerceResult.Success -> {
                edit { it.copy(stage = DocumentUploadState.Stage.Done, progress = null) }
                onAttached()
            }
        }
    }

    /**
     * Reads the picked file, refusing anything a reviewer could not open.
     *
     * Checked here rather than left to the server because the round trip is a
     * whole upload: a 12 MB HEIC that is rejected on attach has cost the
     * seller their upload and told them nothing until it finished.
     */
    private fun readPicked(uri: String): PickedMedia? {
        val picked = sources.resolve(uri)
        val rejection = when {
            picked == null -> "That file could not be read."
            picked.mimeType !in AcceptedDocumentTypes ->
                "Send a JPEG, PNG or PDF."

            picked.sizeBytes > MAX_DOCUMENT_BYTES -> "That file is larger than 10 MB."
            else -> null
        }
        if (rejection != null) {
            fail(rejection)
            return null
        }
        return picked
    }

    private fun fail(message: String) = edit {
        it.copy(stage = DocumentUploadState.Stage.Idle, progress = null, error = message)
    }

    private fun edit(transform: (DocumentUploadState) -> DocumentUploadState) {
        _state.value = transform(_state.value)
    }

    private companion object {
        const val MAX_DOC_NUMBER = 24
    }
}

/** What a reviewer can actually open. */
internal val AcceptedDocumentTypes = setOf("image/jpeg", "image/png", "application/pdf")

internal const val MAX_DOCUMENT_BYTES = 10L * 1024 * 1024
