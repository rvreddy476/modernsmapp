package com.us.android.feature.rider.selfie

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryDocumentRequest
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.ui.asMessage
import com.us.android.feature.rider.ui.error
import com.us.android.feature.rider.ui.success
import com.us.android.feature.rider.upload.RiderDocumentUploader
import com.us.android.feature.rider.upload.UploadOutcome
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
    val message: UsMessage? = null,
)

/** Selfie capture → upload through :core:media → `POST …/documents {document_type: SELFIE, media_id}` (no number). */
@HiltViewModel
class SelfieViewModel @Inject constructor(
    private val rider: RiderRepository,
    private val uploader: RiderDocumentUploader,
) : ViewModel() {

    private val _state = MutableStateFlow(SelfieUiState())
    val state: StateFlow<SelfieUiState> = _state.asStateFlow()

    fun onCaptured(shot: SelfieShot) = _state.update { it.copy(step = SelfieStep.Review(shot)) }

    fun onCaptureFailed() = _state.update { it.copy(message = error("The photo didn't take. Try again.")) }

    fun onCameraUnavailable() = _state.update { it.copy(cameraUnavailable = true) }

    fun retake() = _state.update { it.copy(step = SelfieStep.Camera) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val shot = (_state.value.step as? SelfieStep.Review)?.shot ?: return
        _state.update { it.copy(step = SelfieStep.Uploading(shot, 0f)) }
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
            when (val result = rider.addDocument(DeliveryDocumentRequest(documentType = SELFIE, mediaId = mediaId))) {
                is FoodResult.Success -> _state.update {
                    it.copy(step = SelfieStep.Submitted(result.value.status), message = success("Selfie submitted for review."))
                }
                is FoodResult.Failure -> _state.update { it.copy(step = SelfieStep.Review(shot), message = result.error.asMessage()) }
            }
        }
    }

    private companion object {
        const val SELFIE = "SELFIE"
    }
}
