package com.us.android.feature.mopedu.captain.selfie

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.feature.mopedu.captain.data.CaptainDocumentTypes
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.userMessage
import com.us.android.feature.mopedu.captain.upload.CaptainDocumentUploader
import com.us.android.feature.mopedu.captain.upload.UploadOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface SelfieStep {
    /** The camera is live (or asking for permission). */
    data object Camera : SelfieStep

    data class Review(val shot: SelfieShot) : SelfieStep

    data class Uploading(val shot: SelfieShot, val progress: Float) : SelfieStep

    /** Submitted: `status` is the document's review status. */
    data class Submitted(val status: String) : SelfieStep
}

data class SelfieUiState(
    val step: SelfieStep = SelfieStep.Camera,
    val cameraUnavailable: Boolean = false,
    /** ONE retake: the second shot is the one that goes in. */
    val retakesLeft: Int = MAX_RETAKES,
    val message: UsMessage? = null,
) {
    val canRetake: Boolean get() = retakesLeft > 0
}

/**
 * Selfie capture → upload through :core:media → `POST /partners/me/documents
 * {document_type: profile_photo, media_id}`. The document record is submitted
 * ONLY after the uploader answers a confirmed media id: a failed upload leaves
 * the captain on the review step with the reason, and nothing is posted.
 *
 * Copied from :feature:rider's selfie/SelfieViewModel.kt, plus the one-retake rule.
 */
@HiltViewModel
class CaptainSelfieViewModel @Inject constructor(
    private val repository: MopeduCaptainRepository,
    private val uploader: CaptainDocumentUploader,
) : ViewModel() {

    private val _state = MutableStateFlow(SelfieUiState())
    val state: StateFlow<SelfieUiState> = _state.asStateFlow()

    fun onCaptured(shot: SelfieShot) = _state.update { it.copy(step = SelfieStep.Review(shot)) }

    fun onCaptureFailed() = _state.update { it.copy(message = error("The photo didn't take. Try again.")) }

    fun onCameraUnavailable() = _state.update { it.copy(cameraUnavailable = true) }

    /** Back to the camera, once. A second tap does nothing: the shot on screen is the one to use. */
    fun retake() = _state.update { current ->
        if (current.step !is SelfieStep.Review || !current.canRetake) return@update current
        current.copy(step = SelfieStep.Camera, retakesLeft = current.retakesLeft - 1)
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val shot = (_state.value.step as? SelfieStep.Review)?.shot ?: return
        _state.update { it.copy(step = SelfieStep.Uploading(shot, 0f), message = null) }
        viewModelScope.launch {
            val outcome = uploader.uploadImage(shot.uri) { progress ->
                _state.update { it.copy(step = SelfieStep.Uploading(shot, progress)) }
            }
            val mediaId = when (outcome) {
                is UploadOutcome.Ready -> outcome.mediaId
                is UploadOutcome.Failed -> {
                    _state.update { it.copy(step = SelfieStep.Review(shot), message = error(outcome.message)) }
                    return@launch
                }
            }
            when (val result = repository.submitDocument(CaptainDocumentTypes.SELFIE, documentNumber = null, mediaId = mediaId)) {
                is CaptainResult.Success -> _state.update {
                    it.copy(step = SelfieStep.Submitted(result.value.status), message = success("Selfie submitted."))
                }
                is CaptainResult.Failure -> _state.update {
                    it.copy(step = SelfieStep.Review(shot), message = error("Selfie not submitted: ${result.error.userMessage()}"))
                }
            }
        }
    }

    private fun error(text: String) = UsMessage(text, UsMessageType.Error)

    private fun success(text: String) = UsMessage(text, UsMessageType.Success)
}

private const val MAX_RETAKES = 1
