package com.us.android.feature.dating.safety

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.valueOrNull
import com.us.android.feature.dating.location.CurrentLocationSource
import com.us.android.feature.dating.location.LocationEffect
import com.us.android.feature.dating.location.LocationPermissionFlow
import com.us.android.feature.dating.location.LocationStep
import com.us.android.feature.dating.network.DatingPersonDto
import com.us.android.feature.dating.network.ShareLocationRequest
import com.us.android.feature.dating.network.SharedLocationDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
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

/**
 * Someone this screen can name: a trusted contact, a match, a share recipient.
 *
 * [name] is the server's `first_name` from that person's card, falling back to
 * "Your match" when the card is null — a profile that was deleted or purged.
 * [photoUrl] is already in the variant the card's `photo_state` allows.
 */
data class PersonOption(val userId: String, val name: String, val photoUrl: String? = null)

/** A share I am sending, as the SERVER lists it, so Stop works after a restart. */
data class ActiveShare(val shareId: String, val recipientId: String, val recipientName: String, val expiresAt: String)

/** A share someone is sending to ME. Opened by [shareId]; it carries no coordinates. */
data class SharedWithMe(val shareId: String, val sharerName: String, val expiresAt: String)

data class PanicUi(val incidentId: String, val repeated: Boolean)

data class SafetyUiState(
    val loading: Boolean = true,
    val contacts: List<PersonOption> = emptyList(),
    val maxContacts: Int = MAX_TRUSTED_CONTACTS,
    /** Matches that are not trusted contacts yet. */
    val candidates: List<PersonOption> = emptyList(),
    /** Who a live location can go to: trusted contacts and matches. */
    val recipients: List<PersonOption> = emptyList(),
    /** Live shares I am sending, from the server. */
    val shares: List<ActiveShare> = emptyList(),
    /** Live shares sent to me. */
    val sharedWithMe: List<SharedWithMe> = emptyList(),
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
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    private val _state = MutableStateFlow(SafetyUiState())
    val state: StateFlow<SafetyUiState> = _state.asStateFlow()

    private val permissionFlow = LocationPermissionFlow()
    private var pendingShare: Pair<String, Int>? = null

    init {
        refresh()
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
            // Each list is named by the card the SERVER sent with the row it came
            // from: a trusted contact carries its own `person`, so nothing here
            // resolves a name out of the match rows any more.
            val matches = repository.matches().valueOrNull().orEmpty()
                .filterNot { it.status == "closed" }
                .map { row -> option(row.person?.userId ?: session.otherOf(row.userA, row.userB), row.person) }
                .filterNot { it.userId in removed }
                .distinctBy { it.userId }
            // A contact whose profile is gone has a null person: it keeps the
            // "Your match" fallback and STAYS in the list, so it can be removed.
            val contacts = contactsDto.items
                .map { option(it.contactId, it.person) }
                .filterNot { it.userId in removed }
            val contactIds = contacts.map { it.userId }.toSet()
            _state.update {
                it.copy(
                    loading = false,
                    contacts = contacts,
                    maxContacts = contactsDto.max.takeIf { max -> max > 0 } ?: MAX_TRUSTED_CONTACTS,
                    candidates = matches.filterNot { match -> match.userId in contactIds },
                    recipients = (contacts + matches).distinctBy { person -> person.userId },
                )
            }
            loadShares()
        }
    }

    /** The shares I am sending and the ones sent to me, both from the server. */
    private suspend fun loadShares() {
        val mine = repository.myLocationShares().valueOrNull()?.items.orEmpty().map { share ->
            ActiveShare(
                shareId = share.shareId,
                recipientId = share.recipientId,
                recipientName = share.recipient?.firstName?.takeIf { it.isNotBlank() } ?: DEFAULT_PERSON,
                expiresAt = share.expiresAt,
            )
        }
        val toMe = repository.sharedWithMe().valueOrNull()?.items.orEmpty()
            .filterNot { session.isRemoved(it.person?.userId ?: it.userId) }
            .map { share ->
                SharedWithMe(
                    shareId = share.shareId,
                    sharerName = share.person?.firstName?.takeIf { it.isNotBlank() } ?: DEFAULT_PERSON,
                    expiresAt = share.expiresAt,
                )
            }
        _state.update { it.copy(shares = mine, sharedWithMe = toMe) }
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
                    _state.update { it.copy(message = successMessage("Stopped sharing your location.")) }
                    loadShares()
                }
                is DatingResult.Failure -> {
                    // A share that already ended is gone either way: re-read rather
                    // than leave a Stop button for something the server has dropped.
                    if ((result.error as? DatingError.Refused)?.status == HTTP_NOT_FOUND) loadShares()
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
                    _state.update { it.copy(sharing = false, message = successMessage("Sharing your location for $minutes minutes.")) }
                    loadShares()
                }
                is DatingResult.Failure -> _state.update { it.copy(sharing = false, message = DatingCopy.message(result.error)) }
            }
        }
    }

    /** [person] is the server's card for [userId]; null when that profile is gone. */
    private fun option(userId: String, person: DatingPersonDto?) = PersonOption(
        userId = userId,
        name = person?.firstName?.takeIf { it.isNotBlank() } ?: DEFAULT_PERSON,
        photoUrl = urls.forPerson(person),
    )

    private fun syncLocation() = _state.update { it.copy(location = permissionFlow.step) }

    private companion object {
        const val MAX_SHARE_MINUTES = 120
        const val HTTP_NOT_FOUND = 404
        const val DEFAULT_PERSON = "Your match"
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
