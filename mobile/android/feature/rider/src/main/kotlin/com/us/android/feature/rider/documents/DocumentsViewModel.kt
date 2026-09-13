package com.us.android.feature.rider.documents

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryDocumentDto
import com.us.android.core.food.network.DeliveryDocumentRequest
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.kyc.DrivingLicence
import com.us.android.feature.rider.kyc.VehicleRegistration
import com.us.android.feature.rider.kycui.DocumentUploadUi
import com.us.android.feature.rider.ui.asMessage
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

/** The two driving documents a motorised rider submits by hand. */
enum class DrivingDocument(val wire: String, val title: String) {
    DRIVING_LICENCE("DRIVING_LICENCE", "Driving licence"),
    VEHICLE_RC("VEHICLE_RC", "Vehicle registration (RC)"),
}

data class DocumentDraft(
    val number: String = "",
    val numberError: String? = null,
    val upload: DocumentUploadUi = DocumentUploadUi.Idle,
    val submitting: Boolean = false,
    val submitted: DeliveryDocumentDto? = null,
)

data class DocumentsUiState(
    val loading: Boolean = true,
    val required: Boolean = true,
    val drafts: Map<DrivingDocument, DocumentDraft> = DrivingDocument.entries.associateWith { DocumentDraft() },
    val message: UsMessage? = null,
)

/**
 * DL and RC: a number checked against shared/kyc's formats, a photo through
 * :core:media, then `POST /v1/food/delivery/documents`. Skipped entirely for a
 * bicycle, as the server skips them (`driving_documents_required: false`).
 */
@HiltViewModel
class DocumentsViewModel @Inject constructor(
    private val rider: RiderRepository,
    private val uploader: RiderDocumentUploader,
) : ViewModel() {

    private val _state = MutableStateFlow(DocumentsUiState())
    val state: StateFlow<DocumentsUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            val result = rider.kycStatus()
            _state.update { current ->
                when (result) {
                    is FoodResult.Success -> current.copy(
                        loading = false,
                        required = result.value.drivingDocumentsRequired,
                        drafts = current.drafts.mapValues { (doc, draft) ->
                            draft.copy(submitted = result.value.documents.lastOrNull { it.documentType == doc.wire })
                        },
                    )
                    is FoodResult.Failure -> current.copy(loading = false, message = result.error.asMessage())
                }
            }
        }
    }

    fun onNumber(document: DrivingDocument, value: String) =
        draft(document) { it.copy(number = value.uppercase().take(MAX_INPUT), numberError = null) }

    fun onPhotoPicked(document: DrivingDocument, uri: String) {
        draft(document) { it.copy(upload = DocumentUploadUi.Uploading(0f)) }
        viewModelScope.launch {
            val outcome = uploader.uploadImage(uri) { progress -> draft(document) { it.copy(upload = DocumentUploadUi.Uploading(progress)) } }
            draft(document) {
                it.copy(
                    upload = when (outcome) {
                        is UploadOutcome.Ready -> DocumentUploadUi.Uploaded(outcome.mediaId)
                        is UploadOutcome.Failed -> DocumentUploadUi.Failed(outcome.message)
                    },
                )
            }
        }
    }

    fun submit(document: DrivingDocument) {
        val current = _state.value.drafts.getValue(document)
        val normalized = when (document) {
            DrivingDocument.DRIVING_LICENCE -> DrivingLicence.parse(current.number)?.normalized
            DrivingDocument.VEHICLE_RC -> VehicleRegistration.parse(current.number)?.normalized
        }
        if (normalized == null) {
            val message = if (document == DrivingDocument.DRIVING_LICENCE) DrivingLicence.INVALID_MESSAGE else VehicleRegistration.INVALID_MESSAGE
            draft(document) { it.copy(numberError = message) }
            return
        }
        val mediaId = (current.upload as? DocumentUploadUi.Uploaded)?.mediaId
        if (mediaId == null) {
            draft(document) { it.copy(upload = DocumentUploadUi.Failed("Add a clear photo of the document first.")) }
            return
        }
        draft(document) { it.copy(submitting = true) }
        viewModelScope.launch {
            val result = rider.addDocument(DeliveryDocumentRequest(documentType = document.wire, documentNumber = normalized, mediaId = mediaId))
            when (result) {
                is FoodResult.Success -> {
                    draft(document) { DocumentDraft(submitted = result.value) }
                    _state.update { it.copy(message = success("${document.title} submitted for review.")) }
                }
                is FoodResult.Failure -> {
                    val error = result.error
                    if (error is FoodError.InvalidField && error.field == "document_number") {
                        draft(document) { it.copy(submitting = false, numberError = error.message) }
                    } else {
                        draft(document) { it.copy(submitting = false) }
                        _state.update { it.copy(message = error.asMessage()) }
                    }
                }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    private fun draft(document: DrivingDocument, change: (DocumentDraft) -> DocumentDraft) {
        _state.update { it.copy(drafts = it.drafts + (document to change(it.drafts.getValue(document)))) }
    }

    private companion object {
        const val MAX_INPUT = 20
    }
}
