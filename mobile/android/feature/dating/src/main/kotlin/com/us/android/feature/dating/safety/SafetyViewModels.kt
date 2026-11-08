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
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.detailsAs
import com.us.android.feature.dating.data.valueOrNull
import com.us.android.feature.dating.home.parseInstant
import com.us.android.feature.dating.location.CurrentLocationSource
import com.us.android.feature.dating.location.LocationEffect
import com.us.android.feature.dating.location.LocationPermissionFlow
import com.us.android.feature.dating.location.LocationStep
import com.us.android.feature.dating.network.DatingPersonDto
import com.us.android.feature.dating.network.PanicLocationConsentDetailsDto
import com.us.android.feature.dating.network.PanicLocationConsentDto
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
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import javax.inject.Inject

/**
 * Someone this screen can name: a trusted contact, a match, a share recipient.
 *
 * [name] is the server's `first_name` from that person's card, falling back to
 * "Your match" when the card is null — a profile that was deleted or purged.
 * [photoUrl] is already in the variant the card's `photo_state` allows.
 */
data class PersonOption(val userId: String, val name: String, val photoUrl: String? = null)

/** A share I am sending, as the SERVER lists it, so Stop works after a restart. */
data class ActiveShare(
    val shareId: String,
    val recipientId: String,
    val recipientName: String,
    val expiresAt: String,
    val createdAt: String = "",
)

/** A share someone is sending to ME. Opened by [shareId]; it carries no coordinates. */
data class SharedWithMe(val shareId: String, val sharerName: String, val expiresAt: String, val createdAt: String = "")

data class PanicUi(val incidentId: String, val repeated: Boolean)

data class SafetyUiState(
    val loading: Boolean = true,
    val contacts: List<PersonOption> = emptyList(),
    val maxContacts: Int = MAX_TRUSTED_CONTACTS,
    /**
     * Audit M4: the contacts who receive my exact location if I press panic —
     * the server's EFFECTIVE `share_location_on_panic`, per contact. Adding a
     * contact never puts them here; only their own switch does.
     */
    val panicLocationTo: Set<String> = emptySet(),
    /** The consent wording the switch asks with; null hides the switch (a server without it). */
    val panicLocationConsent: PanicLocationConsentDto? = null,
    /** The contact whose switch is being turned on: the consent dialog is up for them. */
    val askingConsentFor: String? = null,
    /** A switch change is on its way. */
    val consentBusy: Boolean = false,
    /** Matches that are not trusted contacts yet. */
    val candidates: List<PersonOption> = emptyList(),
    /** Who a location can go to: trusted contacts and matches. */
    val recipients: List<PersonOption> = emptyList(),
    /** Shares I am sending, from the server. */
    val shares: List<ActiveShare> = emptyList(),
    /** Shares sent to me. */
    val sharedWithMe: List<SharedWithMe> = emptyList(),
    val panic: PanicUi? = null,
    val panicBusy: Boolean = false,
    val location: LocationStep = LocationStep.Idle,
    val sharing: Boolean = false,
    val message: UsMessage? = null,
) {
    val canAddContact: Boolean get() = contacts.size < maxContacts && candidates.isNotEmpty()

    /** The name of the contact the consent dialog is about. */
    val consentName: String? get() = askingConsentFor?.let { id -> contacts.firstOrNull { it.userId == id }?.name }
}

const val MAX_TRUSTED_CONTACTS = 3

/** Share durations offered; the server caps a share at 120 minutes. */
val SHARE_MINUTES = listOf(15, 30, 60, 120)

/** The safety centre's words. Pure, so the tests can read them. */
object SafetyCopy {
    /**
     * Audit M4: the panic card says who sees the location — the safety team,
     * and only the trusted contacts the member chose to send it to.
     */
    const val PANIC_BODY = "This alerts our safety team and your trusted contacts straight away. " +
        "If you've allowed location, " +
        "our team sees where you are, and so do the trusted contacts you've chosen to send it to."
    const val PANIC_LOCATION_SWITCH = "Send them my location if I press panic"
    const val CONSENT_TITLE = "Send your location to %s?"
    const val CONSENT_CONFIRM = "Send my location"
    const val CONSENT_UPDATED = "The wording changed. Please read it again."

    /** Audit M1: a share is ONE point taken when it starts, so it is never called live. */
    const val SHARE_SECTION = "Share where I am now"
    const val SHARE_HINT = "Sends the place you are right now. It won't follow you if you move."
    const val SHARE_RATIONALE_TITLE = "Share where you are?"
    const val SHARE_RATIONALE_BODY = "Only the person you choose sees it, until the time runs out or you stop it. " +
        "It's the place you are now — it won't update as you move."
    const val SHARED_WITH_YOU_TITLE = "Where they were when they shared"
    const val SHARED_WITH_YOU_BODY = "This is one point, from when they shared it. It doesn't move with them. " +
        "It stays available until they stop sharing or the time runs out."

    fun sharingFor(minutes: Int) = "Shared where you are now for $minutes minutes. It won't update as you move."

    fun sharedBy(name: String) = "$name shared where they were"
}

/**
 * "Shared at 19:42 (58 min ago)" from a share's `created_at` (audit M1), or
 * null when the time is unreadable. [now] before the share (a skewed clock)
 * reads as "just now". Pure.
 */
fun sharedAtLabel(createdAt: String?, now: Instant, zone: ZoneId): String? =
    parseInstant(createdAt)?.let { "Shared at ${clockAndAgo(it, now, zone)}" }

/** "19:42 (58 min ago)", "19:42 (2 h 5 min ago)", or "19:42 (just now)" for a time not yet a minute old or ahead of [now]. Pure. */
internal fun clockAndAgo(at: Instant, now: Instant, zone: ZoneId): String {
    val clock = at.atZone(zone).format(DateTimeFormatter.ofPattern("HH:mm", Locale.ENGLISH))
    val minutes = Duration.between(at, now).toMinutes()
    val ago = when {
        minutes < 1 -> "just now"
        minutes < MINUTES_PER_HOUR -> "$minutes min ago"
        else -> "${minutes / MINUTES_PER_HOUR} h ${minutes % MINUTES_PER_HOUR} min ago"
    }
    return "$clock ($ago)"
}

private const val MINUTES_PER_HOUR = 60

/**
 * The safety centre: panic, trusted contacts (at most 3, each a match or a
 * connection) with a per-contact "send them my location if I press panic"
 * consent, and sharing where I am now with a match or a trusted contact for at
 * most 120 minutes, with Stop.
 */
@Suppress("TooManyFunctions")
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
            val visible = contactsDto.items.filterNot { it.contactId in removed }
            val contacts = visible.map { option(it.contactId, it.person) }
            val contactIds = contacts.map { it.userId }.toSet()
            _state.update {
                it.copy(
                    loading = false,
                    contacts = contacts,
                    panicLocationTo = visible.filter { c -> c.shareLocationOnPanic }.map { c -> c.contactId }.toSet(),
                    panicLocationConsent = contactsDto.panicLocationConsent
                        ?.takeIf { c -> c.version.isNotBlank() && c.text.isNotBlank() },
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
                createdAt = share.createdAt,
            )
        }
        val toMe = repository.sharedWithMe().valueOrNull()?.items.orEmpty()
            .filterNot { session.isRemoved(it.person?.userId ?: it.userId) }
            .map { share ->
                SharedWithMe(
                    shareId = share.shareId,
                    sharerName = share.person?.firstName?.takeIf { it.isNotBlank() } ?: DEFAULT_PERSON,
                    expiresAt = share.expiresAt,
                    createdAt = share.createdAt,
                )
            }
        _state.update { it.copy(shares = mine, sharedWithMe = toMe) }
    }

    /**
     * Pages the safety team (and the trusted contacts). The location goes with
     * it only if permission is ALREADY granted: nobody in trouble is shown a
     * permission dialog. Which contacts receive it is their own switch.
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
                    it.copy(
                        panicBusy = false,
                        message = errorMessage("The alert didn't send. If you're in danger, call 112."),
                    )
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

    /**
     * A contact's "send them my location if I press panic" switch (audit M4).
     * Turning it ON never happens here: it opens the consent wording, and only
     * [confirmPanicLocation] sends it. Turning it off revokes straight away.
     */
    fun setPanicLocation(contactId: String, on: Boolean) {
        val s = _state.value
        if (s.consentBusy || contactId !in s.contacts.map { it.userId }) return
        if (on) {
            if (s.panicLocationConsent == null) return
            _state.update { it.copy(askingConsentFor = contactId) }
            return
        }
        patchPanicLocation(contactId, share = false, version = null)
    }

    /** The member agreed to the wording shown: grant it for that contact, with that wording's version. */
    fun confirmPanicLocation() {
        val s = _state.value
        val contactId = s.askingConsentFor ?: return
        val consent = s.panicLocationConsent ?: return
        if (s.consentBusy) return
        patchPanicLocation(contactId, share = true, version = consent.version)
    }

    fun dismissConsent() = _state.update { if (it.consentBusy) it else it.copy(askingConsentFor = null) }

    private fun patchPanicLocation(contactId: String, share: Boolean, version: String?) {
        _state.update { it.copy(consentBusy = true) }
        viewModelScope.launch {
            when (val result = repository.setPanicLocation(contactId, share, version)) {
                is DatingResult.Success -> _state.update {
                    val effective = result.value.shareLocationOnPanic
                    it.copy(
                        consentBusy = false,
                        askingConsentFor = null,
                        panicLocationTo = if (effective) {
                            it.panicLocationTo + contactId
                        } else {
                            it.panicLocationTo - contactId
                        },
                    )
                }
                is DatingResult.Failure -> onPanicLocationRefused(result.error)
            }
        }
    }

    /**
     * 422 `PANIC_LOCATION_CONSENT_REQUIRED`: the wording changed since it was
     * read. The dialog stays up with the CURRENT wording and nothing is shared
     * until the member agrees to that. Any other failure closes the dialog and
     * leaves the switch where the server has it.
     */
    private fun onPanicLocationRefused(error: DatingError) {
        val fresh = if (error.code == CODE_CONSENT_REQUIRED) {
            error.detailsAs(repository.json, PanicLocationConsentDetailsDto.serializer())
                ?.takeIf { it.consentVersion.isNotBlank() && it.consentText.isNotBlank() }
        } else {
            null
        }
        _state.update {
            if (fresh != null) {
                it.copy(
                    consentBusy = false,
                    panicLocationConsent = PanicLocationConsentDto(
                        version = fresh.consentVersion,
                        text = fresh.consentText,
                    ),
                    message = errorMessage(SafetyCopy.CONSENT_UPDATED),
                )
            } else {
                it.copy(consentBusy = false, askingConsentFor = null, message = DatingCopy.message(error))
            }
        }
    }

    /** Shares where I am now with [recipientId]; returns what the screen must do next (the rationale comes first). */
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
                    _state.update { it.copy(sharing = false, message = successMessage(SafetyCopy.sharingFor(minutes))) }
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
        const val CODE_CONSENT_REQUIRED = "PANIC_LOCATION_CONSENT_REQUIRED"
    }
}

sealed interface SharedLocationState {
    data object Loading : SharedLocationState

    data class Live(val share: SharedLocationDto) : SharedLocationState

    data class Ended(val message: String) : SharedLocationState
}

/** Where someone was when they shared it with me: readable only while the share lasts, only by me. */
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
