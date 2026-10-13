package com.us.android.feature.doorsteppro.root

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.ProfessionalDto
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.deeplink.ProDeepLink
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinkBus
import com.us.android.feature.doorsteppro.domain.ProStatus
import com.us.android.feature.doorsteppro.location.OfflineReason
import com.us.android.feature.doorsteppro.location.ProDuty
import com.us.android.feature.doorsteppro.store.OnboardingMemory
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.Job
import javax.inject.Inject

sealed interface ProRootState {
    data object Loading : ProRootState

    /** Signed in without a professional record: the welcome, with "apply". */
    data object NotApplied : ProRootState

    /** Filling in the application. */
    data object Applying : ProRootState
    data class Submitted(val professional: ProfessionalDto) : ProRootState

    data object SessionExpired : ProRootState

    /** Doorstep is not open to this account (the gateway's pilot gate), or the server could not be reached. */
    data class Unavailable(val title: String, val message: String) : ProRootState

    /** [onboarded] true opens on Home; false on the onboarding checklist. */
    data class Ready(val professional: ProfessionalDto, val onboarded: Boolean) : ProRootState
}

/**
 * The gate in front of every professional screen: `GET /pro/me` (404
 * DOORSTEP_PRO_NOT_FOUND → apply), then the account status. An approved,
 * suspended or blocked professional opens on Home, which says what their
 * status allows; everyone else opens on the onboarding checklist.
 */
@HiltViewModel
class ProRootViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val auth: AuthRepository,
    private val duty: ProDuty,
    private val memory: OnboardingMemory,
    private val deepLinks: ProDeepLinkBus,
    private val session: ProSession,
) : ViewModel() {

    private val _state = MutableStateFlow<ProRootState>(ProRootState.Loading)
    val state: StateFlow<ProRootState> = _state.asStateFlow()

    val links: SharedFlow<ProDeepLink> = deepLinks.incoming
    private var loadJob: Job? = null
    private val _refreshing = MutableStateFlow(false)
    val refreshing = _refreshing.asStateFlow()
    private val _refreshError = MutableStateFlow<String?>(null)
    val refreshError = _refreshError.asStateFlow()

    init {
        load()
    }

    fun load() {
        refresh(force = true)
    }

    /** Reconcile without unmounting the form, workspace or review receipt. */
    fun refreshStatus() {
        if (_state.value != ProRootState.Applying) refresh(force = false)
    }

    private fun refresh(force: Boolean) {
        if (loadJob?.isActive == true) return
        if (force && _state.value is ProRootState.Unavailable) _state.value = ProRootState.Loading
        _refreshing.value = true
        _refreshError.value = null
        loadJob = viewModelScope.launch {
            try {
              when (val me = repository.me()) {
                is ProResult.Success -> {
                    session.set(me.value)
                    _state.value = rootStateFor(me.value)
                }
                is ProResult.Failure -> {
                    if (me.error == ProError.Unauthorized || _state.value == ProRootState.Loading) {
                        _state.value = failureState(me.error)
                    } else {
                        _refreshError.value = "Couldn't refresh your status. Please try again."
                    }
                }
              }
            } finally {
                _refreshing.value = false
            }
        }
    }

    fun startApplying() {
        _state.value = ProRootState.Applying
    }

    fun cancelApplying() {
        _state.value = ProRootState.NotApplied
    }

    /** A link has been navigated to. DigiLocker links are consumed by their own screen. */
    fun linkHandled() {
        deepLinks.consumed()
    }

    /** Off duty first — while the session can still tell the server — then sign out. */
    fun signOut() {
        viewModelScope.launch {
            if (duty.isOnline) {
                repository.dutyOff()
                duty.requestStop(OfflineReason.SIGNED_OUT)
            }
            memory.clearAll()
            session.set(null)
            auth.logout()
        }
    }

    private fun failureState(error: ProError): ProRootState = when {
        error.code == ProCodes.PRO_NOT_FOUND -> ProRootState.NotApplied
        error == ProError.Unauthorized -> ProRootState.SessionExpired
        error == ProError.NotFound -> ProRootState.Unavailable(
            title = "Doorstep isn't open to you yet",
            message = "Doorstep Pro is in a pilot in Hyderabad. Your account isn't on it yet — " +
                "ask the Doorstep team to add you, then try again.",
        )
        else -> ProRootState.Unavailable(title = "Couldn't open Doorstep Pro", message = error.userMessage())
    }

}

/** The current server status wins over the presence of an old submitted application. */
internal fun rootStateFor(pro: ProfessionalDto): ProRootState = when (ProStatus.of(pro.status)) {
    ProStatus.APPROVED, ProStatus.SUSPENDED, ProStatus.BLOCKED -> ProRootState.Ready(pro, onboarded = true)
    ProStatus.PENDING_VERIFICATION -> ProRootState.Submitted(pro)
    ProStatus.DRAFT -> if (pro.application == null) ProRootState.Applying else ProRootState.Ready(pro, onboarded = false)
    else -> ProRootState.Ready(pro, onboarded = false)
}
