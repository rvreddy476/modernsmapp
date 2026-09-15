package com.us.android.feature.dating.safety

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.valueOrNull
import com.us.android.feature.dating.location.CurrentLocationSource
import com.us.android.feature.dating.location.LocationEffect
import com.us.android.feature.dating.location.LocationPermissionFlow
import com.us.android.feature.dating.location.LocationStep
import com.us.android.feature.dating.network.ShareLocationRequest
import com.us.android.feature.dating.network.SharedLocationDto
import com.us.android.feature.dating.ui.errorMessage
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

data class PersonOption(val userId: String, val name: String)

data class ActiveShare(val shareId: String, val recipientId: String, val recipientName: String, val expiresAt: String)

/**
 * The live location shares started in this process. dating-service has no
 * route that lists a sharer's active shares (backend gap), so this is the only
 * place the Stop control can come from.
 */
@Singleton
class LiveShares @Inject constructor() {
    private val _shares = MutableStateFlow<List<ActiveShare>>(emptyList())
    val shares: StateFlow<List<ActiveShare>> = _shares.asStateFlow()

    fun add(share: ActiveShare) = _shares.update { it + share }

    fun remove(shareId: String) = _shares.update { list -> list.filterNot { it.shareId == shareId } }
}

data class PanicUi(val incidentId: String, val repeated: Boolean)

data class SafetyUiState(
    val loading: Boolean = true,
    val contacts: List<PersonOption> = emptyList(),
    val maxContacts: Int = MAX_TRUSTED_CONTACTS,
    /** Matches that are not trusted contacts yet. */
    val candidates: List<PersonOption> = emptyList(),
    /** Who a live location can go to: trusted contacts and matches. */
    val recipients: List<PersonOption> = emptyList(),
    val shares: List<ActiveShare> = emptyList(),
    val panic: PanicUi? = null,
    val panicBusy: Boolean = false,
    val location: LocationStep = LocationStep.Idle,
    val sharing: Boolean = false,
    val message: UsMessage? = null,
) {
    val canAddContact: Boolean get() = contacts.size < maxContacts && candidates.isNotEmpty()
}

const val MAX_TRUSTED_CONTACTS = 3

/** Share durations offered; the server caps a share at 120 minutes. */
val SHARE_MINUTES = listOf(15, 30, 60, 120)

/**
 * The safety centre: panic, trusted contacts (at most 3, each a match or a
 * connection), and live location with a match or a trusted contact for at most
 * 120 minutes, with Stop.
 */
@HiltViewModel
class SafetyViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val locationSource: CurrentLocationSource,
    private val liveShares: LiveShares,
) : ViewModel() {

    private val _state = MutableStateFlow(SafetyUiState())
    val state: StateFlow<SafetyUiState> = _state.asStateFlow()

    private val permissionFlow = LocationPermissionFlow()
    private var pendingShare: Pair<String, Int>? = null

    init {
        refresh()
        viewModelScope.launch { liveShares.shares.collect { shares -> _state.update { it.copy(shares = shares) } } }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun refresh() {
        viewModelScope.launch {
            val removed = session.removed.value
            val contactsDto = when (val result = repository.trustedContacts()) {
                is DatingResult.Success -> result.value
                is DatingResult.Failure -> {
                    _state.update { it.copy(loading = false, message = DatingCopy.message(result.error)) }
                    return@launch
                }
            }
            val matches = repository.matches().valueOrNull().orEmpty()
                .filterNot { it.status == "closed" }
                .map { session.otherOf(it.userA, it.userB) }
                .filterNot { it in removed }
                .distinct()
            val contacts = contactsDto.items.map { it.contactId }.filterNot { it in removed }
            _state.update {
                it.copy(
                    loading = false,
                    contacts = contacts.map(::option),
                    maxContacts = contactsDto.max.takeIf { max -> max > 0 } ?: MAX_TRUSTED_CONTACTS,
                    candidates = matches.filterNot { id -> id in contacts }.map(::option),
                    recipients = (contacts + matches).distinct().map(::option),
                )
            }
        }
    }

    /**
     * Pages the safety team. The location goes with it only if permission is
     * ALREADY granted: nobody in trouble is shown a permission dialog.
     */
    fun panic() {
        if (_state.value.panicBusy) return
        _state.update { it.copy(panicBusy = true) }
        viewModelScope.launch {
            val fix = if (locationSource.hasPermission()) locationSource.current() else null
            when (val result = repository.panic(fix?.latitude, fix?.longitude)) {
                is DatingResult.Success -> _state.update {
                    it.copy(panicBusy = false, panic = PanicUi(result.value.incidentId, result.value.deduplicated))
                }
                is DatingResult.Failure -> _state.update {
                    it.copy(panicBusy = false, message = errorMessage("The alert didn't send. If you're in danger, call 112 now."))
                }
            }
        }
    }

    fun addContact(userId: String) {
        viewModelScope.launch {
            when (val result = repository.addTrustedContact(userId)) {
                is DatingResult.Success -> {
                    _state.update { it.copy(message = successMessage("Trusted contact added.")) }
                    refresh()
                }
                is DatingResult.Failure -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
            }
        }
    }

    fun removeContact(userId: String) {
        viewModelScope.launch {
            when (val result = repository.removeTrustedContact(userId)) {
                is DatingResult.Success -> refresh()
                is DatingResult.Failure -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
            }
        }
    }

    /** Starts a live share with [recipientId]; returns what the screen must do next (the rationale comes first). */
    fun share(recipientId: String, minutes: Int): LocationEffect {
        pendingShare = recipientId to minutes.coerceIn(1, MAX_SHARE_MINUTES)
        val effect = permissionFlow.onUseCurrentLocation(locationSource.hasPermission())
        syncLocation()
        if (effect == LocationEffect.FetchLocation) fetchAndShare()
        return effect
    }

    fun onRationaleAccepted(): LocationEffect = permissionFlow.onRationaleAccepted().also { syncLocation() }

    fun onRationaleDismissed() {
        permissionFlow.onRationaleDismissed()
        pendingShare = null
        syncLocation()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) {
        val effect = permissionFlow.onPermissionResult(granted, canAskAgain)
        syncLocation()
        if (effect == LocationEffect.FetchLocation) fetchAndShare()
    }

    fun stopShare(shareId: String) {
        viewModelScope.launch {
            when (val result = repository.stopShare(shareId)) {
                is DatingResult.Success -> {
                    liveShares.remove(shareId)
                    _state.update { it.copy(message = successMessage("Stopped sharing your location.")) }
                }
                is DatingResult.Failure -> {
                    // A share that already ended is gone either way.
                    if ((result.error as? com.us.android.feature.dating.data.DatingError.Refused)?.status == HTTP_NOT_FOUND) {
                        liveShares.remove(shareId)
                    }
                    _state.update { it.copy(message = DatingCopy.message(result.error)) }
                }
            }
        }
    }

    private fun fetchAndShare() {
        val (recipientId, minutes) = pendingShare ?: return
        _state.update { it.copy(sharing = true) }
        viewModelScope.launch {
            val fix = locationSource.current()
            permissionFlow.onLocationResult(fix)
            syncLocation()
            if (fix == null) {
                _state.update { it.copy(sharing = false, message = errorMessage("We couldn't get your location. Try again.")) }
                return@launch
            }
            val request = ShareLocationRequest(recipientId = recipientId, durationMinutes = minutes, latitude = fix.latitude, longitude = fix.longitude)
            when (val result = repository.shareLocation(request)) {
                is DatingResult.Success -> {
                    pendingShare = null
                    val share = result.value
                    liveShares.add(ActiveShare(share.shareId, share.recipientId, option(share.recipientId).name, share.expiresAt))
                    _state.update { it.copy(sharing = false, message = successMessage("Sharing your location for $minutes minutes.")) }
                }
                is DatingResult.Failure -> _state.update { it.copy(sharing = false, message = DatingCopy.message(result.error)) }
            }
        }
    }

    private fun option(userId: String) = PersonOption(userId, session.person(userId)?.firstName?.takeIf { it.isNotBlank() } ?: "Your match")

    private fun syncLocation() = _state.update { it.copy(location = permissionFlow.step) }

    private companion object {
        const val MAX_SHARE_MINUTES = 120
        const val HTTP_NOT_FOUND = 404
    }
}

sealed interface SharedLocationState {
    data object Loading : SharedLocationState

    data class Live(val share: SharedLocationDto) : SharedLocationState

    data class Ended(val message: String) : SharedLocationState
}

/** Someone's live location shared with me: readable only while live, only by me. */
@HiltViewModel
class SharedLocationViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DatingRepository,
) : ViewModel() {

    private val shareId: String = checkNotNull(savedStateHandle[ARG_SHARE_ID]) { "a shared location route needs a shareId" }

    private val _state = MutableStateFlow<SharedLocationState>(SharedLocationState.Loading)
    val state: StateFlow<SharedLocationState> = _state.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            _state.value = when (val result = repository.sharedLocation(shareId)) {
                is DatingResult.Success -> {
                    val share = result.value
                    if (share.stoppedAt != null || share.latitude == null || share.longitude == null) {
                        SharedLocationState.Ended("This person has stopped sharing their location.")
                    } else {
                        SharedLocationState.Live(share)
                    }
                }
                is DatingResult.Failure -> SharedLocationState.Ended("This location share has ended.")
            }
        }
    }

    companion object {
        const val ARG_SHARE_ID = "shareId"
    }
}
