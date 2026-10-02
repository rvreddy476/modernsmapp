package com.us.android.feature.dating.onboarding

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.network.DatingPhotoDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.photos.PhotoUploader
import com.us.android.feature.dating.photos.UploadOutcome
import com.us.android.feature.dating.ui.errorMessage
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** One of the person's own photos, as the photo step shows it. */
data class OwnPhoto(
    val id: String,
    val url: String,
    val primary: Boolean,
    val status: PhotoStatus,
)

enum class PhotoStatus(val label: String) {
    APPROVED("Approved"),
    CHECKING("Being checked"),
    REJECTED("Not approved"),
    ;

    companion object {
        fun fromWire(moderationStatus: String): PhotoStatus = when (moderationStatus) {
            "approved" -> APPROVED
            "rejected" -> REJECTED
            else -> CHECKING
        }
    }
}

data class PhotosUiState(
    val loading: Boolean = true,
    val photos: List<OwnPhoto> = emptyList(),
    /** 0..1 while a photo uploads; null otherwise. */
    val uploadProgress: Float? = null,
    val message: UsMessage? = null,
) {
    val hasApprovedPrimary: Boolean get() = photos.any { it.primary && it.status == PhotoStatus.APPROVED }
    val canAddMore: Boolean get() = photos.count { it.status != PhotoStatus.REJECTED } < MAX_PHOTOS && uploadProgress == null

    companion object {
        const val MAX_PHOTOS = 6
    }
}

/**
 * The photo step (pending_photo) and photo editing later: upload through
 * `:core:media`, attach the media id, make one primary. The server moves the
 * profile on to the selfie step once the primary photo is approved.
 */
@HiltViewModel
class PhotosViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val uploader: PhotoUploader,
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    private val _state = MutableStateFlow(PhotosUiState())
    val state: StateFlow<PhotosUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.myPhotos()) {
                is DatingResult.Success -> _state.update { it.copy(loading = false, photos = result.value.toOwn()) }
                is DatingResult.Failure -> _state.update { it.copy(loading = false, message = DatingCopy.message(result.error)) }
            }
        }
    }

    /** [uri] is the content:// URI the photo picker returned. */
    fun addPhoto(uri: String) {
        if (!_state.value.canAddMore) return
        _state.update { it.copy(uploadProgress = 0f) }
        viewModelScope.launch {
            val mediaId = when (val outcome = uploader.upload(uri) { p -> _state.update { it.copy(uploadProgress = p) } }) {
                is UploadOutcome.Ready -> outcome.mediaId
                is UploadOutcome.Failed -> {
                    _state.update { it.copy(uploadProgress = null, message = errorMessage(outcome.message)) }
                    return@launch
                }
            }
            val makePrimary = _state.value.photos.none { it.primary && it.status != PhotoStatus.REJECTED }
            when (val attached = repository.attachPhoto(mediaId, primary = makePrimary)) {
                is DatingResult.Success -> {
                    val status = PhotoStatus.fromWire(attached.value.moderationStatus)
                    val note = when (status) {
                        PhotoStatus.APPROVED -> successMessage("Photo added.")
                        PhotoStatus.CHECKING -> successMessage("Photo added. We're checking it.")
                        PhotoStatus.REJECTED -> errorMessage(rejectionCopy(attached.value.moderationReason))
                    }
                    _state.update { it.copy(uploadProgress = null, message = note) }
                    refresh()
                }
                is DatingResult.Failure -> _state.update {
                    it.copy(uploadProgress = null, message = DatingCopy.message(attached.error))
                }
            }
        }
    }

    fun makePrimary(photoId: String) {
        viewModelScope.launch {
            when (val result = repository.makePrimary(photoId)) {
                is DatingResult.Success -> refresh()
                is DatingResult.Failure -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
            }
        }
    }

    fun delete(photoId: String) {
        viewModelScope.launch {
            when (val result = repository.deletePhoto(photoId)) {
                is DatingResult.Success -> refresh()
                is DatingResult.Failure -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
            }
        }
    }

    private fun List<DatingPhotoDto>.toOwn(): List<OwnPhoto> =
        sortedWith(compareByDescending<DatingPhotoDto> { it.isPrimary }.thenBy { it.sortOrder })
            .map { OwnPhoto(it.id, urls.own(it.id), it.isPrimary, PhotoStatus.fromWire(it.moderationStatus)) }

    companion object {
        fun rejectionCopy(reason: String?): String = when (reason) {
            "NO_FACE" -> "We couldn't see a face in that photo. Your photos need to show you."
            "EXPLICIT_CONTENT" -> "That photo breaks our community guidelines."
            "BORDERLINE_CONTENT" -> "That photo needs a moderator's review."
            else -> "That photo wasn't approved. Choose a clear photo of you."
        }
    }
}
