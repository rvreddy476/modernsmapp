package com.us.android.feature.rider.root

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.deeplink.RiderDeepLink
import com.us.android.feature.rider.deeplink.RiderDeepLinkBus
import com.us.android.feature.rider.gate.RiderGate
import com.us.android.feature.rider.location.OfflineReason
import com.us.android.feature.rider.location.RiderDuty
import com.us.android.feature.rider.ui.PartnerStatusText
import com.us.android.feature.rider.ui.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface RiderRootState {
    data object Loading : RiderRootState

    /** Signed in without a partner profile: the friendly screen, with "become a rider". */
    data object NotRider : RiderRootState

    /** Filling in the profile that makes this account a rider. */
    data object BecomingRider : RiderRootState

    data object SessionExpired : RiderRootState

    data class Unavailable(val message: String) : RiderRootState

    /** [verified] false opens on the verification checklist instead of Home. */
    data class Ready(val userId: String, val verified: Boolean) : RiderRootState
}

/** The gate in front of every rider screen: capabilities (fail closed), then the partner's status. */
@HiltViewModel
class RiderRootViewModel @Inject constructor(
    private val food: FoodRepository,
    private val rider: RiderRepository,
    private val auth: AuthRepository,
    private val duty: RiderDuty,
    private val deepLinks: RiderDeepLinkBus,
) : ViewModel() {

    private val _state = MutableStateFlow<RiderRootState>(RiderRootState.Loading)
    val state: StateFlow<RiderRootState> = _state.asStateFlow()

    val links: SharedFlow<RiderDeepLink> = deepLinks.incoming

    init {
        load()
    }

    fun load() {
        _state.value = RiderRootState.Loading
        viewModelScope.launch {
            when (val gate = RiderGate.from(food.capabilities())) {
                is RiderGate.DeliveryPartner -> loadPartner(gate.userId)
                is RiderGate.NotDeliveryPartner -> _state.value = RiderRootState.NotRider
                RiderGate.SessionExpired -> _state.value = RiderRootState.SessionExpired
                is RiderGate.Unavailable -> _state.value = RiderRootState.Unavailable(gate.error.userMessage())
                RiderGate.Checking -> Unit
            }
        }
    }

    fun becomeRider() {
        _state.value = RiderRootState.BecomingRider
    }

    fun cancelBecomingRider() {
        _state.value = RiderRootState.NotRider
    }

    /** An offer link has been navigated to. DigiLocker links are consumed by their own screen. */
    fun linkHandled() {
        deepLinks.consumed()
    }

    /** Offline first — while the session can still tell the server — then sign out. */
    fun signOut() {
        viewModelScope.launch {
            if (duty.isOnline) {
                rider.setAvailability(online = false)
                duty.requestStop(OfflineReason.SIGNED_OUT)
            }
            auth.logout()
        }
    }

    private suspend fun loadPartner(userId: String) {
        _state.value = when (val profile = rider.profile()) {
            is FoodResult.Success -> {
                val status = profile.value?.status
                if (status == null) {
                    RiderRootState.NotRider
                } else {
                    RiderRootState.Ready(userId = userId, verified = status in PartnerStatusText.CAN_GO_ONLINE)
                }
            }
            is FoodResult.Failure -> when (profile.error) {
                FoodError.Unauthorized -> RiderRootState.SessionExpired
                else -> RiderRootState.Unavailable(profile.error.userMessage())
            }
        }
    }
}
