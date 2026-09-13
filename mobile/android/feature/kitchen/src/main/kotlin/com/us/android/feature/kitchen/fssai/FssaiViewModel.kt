package com.us.android.feature.kitchen.fssai

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.FssaiDto
import com.us.android.core.food.network.FssaiRequest
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.feature.kitchen.kyc.FssaiLicence
import com.us.android.feature.kitchen.kycui.DocumentUploadUi
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.queue.KitchenClock
import com.us.android.feature.kitchen.ui.IndiaTime
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.success
import com.us.android.feature.kitchen.upload.KitchenDocumentUploader
import com.us.android.feature.kitchen.upload.UploadOutcome
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.LocalDate
import javax.inject.Inject

data class FssaiUiState(
    val licence: String = "",
    val expiresAt: String = "",
    val upload: DocumentUploadUi = DocumentUploadUi.Idle,
    /** Keyed by request field: `licence_number`, `expires_at`, `media_id`. */
    val errors: Map<String, String> = emptyMap(),
    val saving: Boolean = false,
    val saved: FssaiDto? = null,
    val message: UsMessage? = null,
)

/** FSSAI licence number, expiry and the licence photo → `PUT …/fssai`. */
@HiltViewModel
class FssaiViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val food: FoodRepository,
    private val uploader: KitchenDocumentUploader,
    private val clock: KitchenClock,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()
    private var uploadJob: Job? = null

    private val _state = MutableStateFlow(FssaiUiState())
    val state: StateFlow<FssaiUiState> = _state.asStateFlow()

    val today: LocalDate get() = IndiaTime.today(clock)

    fun onLicence(value: String) = edit(FIELD_LICENCE) {
        it.copy(licence = value.filter { c -> c.isDigit() || c == ' ' }.take(MAX_LICENCE_INPUT))
    }

    fun onExpiresAt(value: String) = edit(FIELD_EXPIRY) { it.copy(expiresAt = value) }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    fun onPhotoPicked(uri: String) {
        uploadJob?.cancel()
        _state.update { it.copy(upload = DocumentUploadUi.Uploading(0f), errors = it.errors - FIELD_MEDIA) }
        uploadJob = viewModelScope.launch {
            val outcome = uploader.uploadImage(uri) { progress ->
                _state.update { if (it.upload is DocumentUploadUi.Uploading) it.copy(upload = DocumentUploadUi.Uploading(progress)) else it }
            }
            _state.update {
                it.copy(
                    upload = when (outcome) {
                        is UploadOutcome.Ready -> DocumentUploadUi.Uploaded(outcome.mediaId)
                        is UploadOutcome.Failed -> DocumentUploadUi.Failed(outcome.message)
                    },
                )
            }
        }
    }

    fun save() {
        val s = _state.value
        val licence = FssaiLicence.normalize(s.licence)
        val expiry = runCatching { LocalDate.parse(s.expiresAt.trim()) }.getOrNull()
        val mediaId = (s.upload as? DocumentUploadUi.Uploaded)?.mediaId
        val errors = buildMap {
            if (licence == null) put(FIELD_LICENCE, FssaiLicence.INVALID_MESSAGE)
            when {
                expiry == null -> put(FIELD_EXPIRY, "Enter the date the licence expires")
                !expiry.isAfter(today) -> put(FIELD_EXPIRY, "The licence must expire after today")
            }
            if (mediaId == null) put(FIELD_MEDIA, "Add a photo of the licence")
        }
        if (errors.isNotEmpty() || licence == null || expiry == null || mediaId == null) {
            _state.update { it.copy(errors = errors) }
            return
        }
        _state.update { it.copy(saving = true, errors = emptyMap()) }
        viewModelScope.launch {
            when (val result = food.putFssai(restaurantId, FssaiRequest(licence, expiry.toString(), mediaId))) {
                is FoodResult.Success -> _state.update {
                    it.copy(saving = false, saved = result.value, message = success("Licence sent for review"))
                }
                is FoodResult.Failure -> _state.update { current ->
                    val error = result.error
                    val field = (error as? FoodError.InvalidField)?.field
                    if (error is FoodError.InvalidField && field != null) {
                        current.copy(saving = false, errors = mapOf(field to error.message))
                    } else {
                        current.copy(saving = false, message = error.asMessage())
                    }
                }
            }
        }
    }

    private inline fun edit(field: String, crossinline change: (FssaiUiState) -> FssaiUiState) {
        _state.update { change(it).copy(errors = it.errors - field) }
    }

    private companion object {
        const val FIELD_LICENCE = "licence_number"
        const val FIELD_EXPIRY = "expires_at"
        const val FIELD_MEDIA = "media_id"
        const val MAX_LICENCE_INPUT = 20
    }
}
