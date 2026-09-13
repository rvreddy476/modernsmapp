package com.us.android.feature.rider.onboarding

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryDocumentDto
import com.us.android.core.food.network.KycCheckDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.ui.PartnerStatusText
import com.us.android.feature.rider.ui.asMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class VerificationUiState(
    val loading: Boolean = true,
    val status: String = "",
    val vehicleType: String = "",
    val checklist: RiderChecklist? = null,
    val checks: List<KycCheckDto> = emptyList(),
    val documents: List<DeliveryDocumentDto> = emptyList(),
    val message: UsMessage? = null,
) {
    val canGoOnline: Boolean get() = status in PartnerStatusText.CAN_GO_ONLINE
}

/**
 * The verification checklist and review status, from `GET …/kyc/status`.
 *
 * The checklist is built from the server's `missing[]` only; its
 * `driving_documents_required` decides whether DL and RC read "not needed".
 */
@HiltViewModel
class VerificationViewModel @Inject constructor(
    private val rider: RiderRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(VerificationUiState())
    val state: StateFlow<VerificationUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.update { it.copy(loading = it.checklist == null) }
        viewModelScope.launch {
            when (val result = rider.kycStatus()) {
                is FoodResult.Success -> {
                    val kyc = result.value
                    _state.update {
                        it.copy(
                            loading = false,
                            status = kyc.status,
                            vehicleType = kyc.vehicleType,
                            checklist = RiderChecklist.from(kyc.missing, kyc.drivingDocumentsRequired),
                            checks = kyc.checks,
                            documents = kyc.documents,
                        )
                    }
                }
                is FoodResult.Failure -> _state.update {
                    // 404: no profile yet — the whole list is to do, starting with the vehicle.
                    val checklist = if (result.error == FoodError.NotFound) {
                        RiderChecklist.from(RiderStep.entries.map { s -> s.wire }, drivingDocumentsRequired = true)
                    } else {
                        it.checklist
                    }
                    it.copy(loading = false, checklist = checklist, message = result.error.takeIf { e -> e != FoodError.NotFound }?.asMessage())
                }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }
}
