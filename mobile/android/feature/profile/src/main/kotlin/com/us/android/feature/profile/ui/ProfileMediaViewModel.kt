package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.media.upload.MEDIA_MODERATION_PASSED
import com.us.android.core.media.upload.MEDIA_MODERATION_REJECTED
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PROCESSING_FAILED
import com.us.android.core.media.upload.PROCESSING_READY
import com.us.android.core.media.upload.PROCESSING_REJECTED
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.SUBTYPE_AVATAR
import com.us.android.core.media.upload.SUBTYPE_COVER
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

enum class ProfileMediaKind(val subtype: String, val label: String) {
    Avatar(SUBTYPE_AVATAR, "profile photo"),
    Cover(SUBTYPE_COVER, "cover photo"),
}

data class ProfileMediaUiState(
    val avatarUrl: String? = null,
    val coverUrl: String? = null,
    val uploading: ProfileMediaKind? = null,
    val uploadedBytes: Long = 0,
    val totalBytes: Long = 0,
    val message: String? = null,
    val error: String? = null,
) {
    val busy: Boolean get() = uploading != null
}

/**
 * One owner for profile-image delivery and upload.
 *
 * It reuses :core:media's URI reader, bare presigned PUT and strict readiness
 * gate. Profile screens therefore do not grow a second upload implementation
 * that can drift from the composer security contract.
 */
@HiltViewModel
class ProfileMediaViewModel @Inject constructor(
    private val mediaRepository: MediaRepository,
    private val profileRepository: ProfileRepository,
    private val uploader: MediaUploader,
    private val sources: MediaSourceResolver,
) : ViewModel() {
    private val _state = MutableStateFlow(ProfileMediaUiState())
    val state: StateFlow<ProfileMediaUiState> = _state.asStateFlow()

    private var boundAvatarId: String? = null
    private var boundCoverId: String? = null

    fun bind(avatarMediaId: String?, coverMediaId: String?) {
        if (boundAvatarId == avatarMediaId && boundCoverId == coverMediaId) return
        boundAvatarId = avatarMediaId
        boundCoverId = coverMediaId
        viewModelScope.launch {
            val avatar = async { resolve(avatarMediaId) }
            val cover = async { resolve(coverMediaId) }
            _state.update {
                it.copy(avatarUrl = avatar.await(), coverUrl = cover.await())
            }
        }
    }

    fun loadOwnerMedia() {
        viewModelScope.launch {
            when (val profile = profileRepository.getOwnProfile()) {
                is AppResult.Success -> bind(profile.data.avatarMediaId, profile.data.coverMediaId)
                is AppResult.Failure -> _state.update {
                    it.copy(error = "We couldn't load your profile images.")
                }
            }
        }
    }

    fun upload(uri: String, kind: ProfileMediaKind) {
        if (_state.value.busy) return
        val source = sources.resolve(uri)
        val rejection = validateSource(source)
        if (source == null || rejection != null) {
            _state.update { it.copy(error = rejection, message = null) }
            return
        }

        _state.update {
            it.copy(
                uploading = kind,
                uploadedBytes = 0,
                totalBytes = source.sizeBytes,
                message = null,
                error = null,
            )
        }
        // IO, not Main: the presigned PUT is a blocking OkHttp execute() and
        // crashed the app with NetworkOnMainThreadException on the founder's
        // phone (2026-09-04). State updates go through the StateFlow.
        viewModelScope.launch(Dispatchers.IO) {
            performUpload(source, kind)
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null, error = null) }

    private suspend fun resolve(mediaId: String?): String? {
        if (mediaId.isNullOrBlank()) return null
        return when (val result = mediaRepository.delivery(mediaId)) {
            is AppResult.Success -> result.data.posterUrl.takeIf { result.data.isReady }
            is AppResult.Failure -> null
        }
    }

    private fun validateSource(source: PickedMedia?): String? = when {
        source == null -> "That photo could not be read."
        source.mimeType !in SUPPORTED_MIME_TYPES -> "Choose a JPEG, PNG or WebP image."
        source.sizeBytes > MAX_PROFILE_IMAGE_BYTES -> "Profile images must be 10 MB or smaller."
        else -> null
    }

    private suspend fun performUpload(source: PickedMedia, kind: ProfileMediaKind) {
        val reserved = uploader.reserve(
            mimeType = source.mimeType,
            sizeBytes = source.sizeBytes,
            mediaSubtype = kind.subtype,
            uploadPurpose = PROFILE_UPLOAD_PURPOSE,
        )
        if (reserved !is AppResult.Success) {
            fail("We couldn't prepare that ${kind.label} upload.")
            return
        }

        val mediaId = reserved.data.mediaId
        val put = uploader.upload(
            uploadUrl = reserved.data.uploadUrl,
            mimeType = source.mimeType,
            sizeBytes = source.sizeBytes,
            source = source.source,
        ) { uploaded, total ->
            _state.update { it.copy(uploadedBytes = uploaded, totalBytes = total) }
        }
        if (put !is PresignedPutResult.Success) {
            discardAndFail(mediaId, "That ${kind.label} upload was interrupted. Please try again.")
            return
        }
        if (uploader.confirm(mediaId) !is AppResult.Success) {
            discardAndFail(mediaId, "We couldn't verify that ${kind.label}.")
            return
        }

        val readinessError = awaitReady(mediaId)
        if (readinessError != null) {
            discardAndFail(mediaId, readinessError)
            return
        }
        attachReadyMedia(mediaId, kind)
    }

    private suspend fun attachReadyMedia(mediaId: String, kind: ProfileMediaKind) {
        val attached = when (kind) {
            ProfileMediaKind.Avatar -> profileRepository.updateAvatar(mediaId)
            ProfileMediaKind.Cover -> profileRepository.updateCover(mediaId)
        }
        if (attached !is AppResult.Success) {
            discardAndFail(mediaId, "We couldn't attach that ${kind.label} to your profile.")
            return
        }

        val deliveryUrl = resolve(mediaId)
        when (kind) {
            ProfileMediaKind.Avatar -> {
                boundAvatarId = mediaId
                _state.update { it.copy(avatarUrl = deliveryUrl) }
            }
            ProfileMediaKind.Cover -> {
                boundCoverId = mediaId
                _state.update { it.copy(coverUrl = deliveryUrl) }
            }
        }
        _state.update {
            it.copy(
                uploading = null,
                uploadedBytes = 0,
                totalBytes = 0,
                message = "Your ${kind.label} was updated.",
            )
        }
    }

    private suspend fun discardAndFail(mediaId: String, message: String) {
        uploader.discard(mediaId)
        fail(message)
    }

    private suspend fun awaitReady(mediaId: String): String? {
        repeat(READINESS_POLLS) { attempt ->
            when (val status = uploader.status(mediaId)) {
                is AppResult.Failure -> return "We couldn't verify that image's safety status."
                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus
                    if (processing == PROCESSING_REJECTED ||
                        processing == PROCESSING_FAILED ||
                        moderation == MEDIA_MODERATION_REJECTED
                    ) {
                        return "That image couldn't be used. Choose a different one."
                    }
                    if (processing == PROCESSING_READY && moderation == MEDIA_MODERATION_PASSED) {
                        return null
                    }
                }
            }
            if (attempt < READINESS_POLLS - 1) delay(READINESS_POLL_DELAY_MS)
        }
        return "That image is still processing. Please try again shortly."
    }

    private fun fail(message: String) = _state.update {
        it.copy(uploading = null, uploadedBytes = 0, totalBytes = 0, error = message)
    }

    private companion object {
        const val MAX_PROFILE_IMAGE_BYTES = 10L * 1024L * 1024L
        const val PROFILE_UPLOAD_PURPOSE = "profile"
        const val READINESS_POLLS = 12
        const val READINESS_POLL_DELAY_MS = 1_000L
        val SUPPORTED_MIME_TYPES = setOf("image/jpeg", "image/png", "image/webp")
    }
}
