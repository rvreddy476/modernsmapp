package com.us.android.feature.dating.onboarding

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.ConsentGate
import com.us.android.feature.dating.ConsentType
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.OnboardingGate
import com.us.android.feature.dating.OnboardingStep
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.location.Coordinates
import com.us.android.feature.dating.location.CurrentLocationSource
import com.us.android.feature.dating.location.LocationEffect
import com.us.android.feature.dating.location.LocationPermissionFlow
import com.us.android.feature.dating.location.LocationStep
import com.us.android.feature.dating.network.DatingProfileDto
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.PreferencesRequest
import com.us.android.feature.dating.network.UpsertProfileRequest
import com.us.android.feature.dating.ui.errorMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Where Dating's root is: probing access, not open to this account, or on a step of the status machine. */
sealed interface DatingRootState {
    data object Loading : DatingRootState

    /** The gateway's pilot gate answered 404: a calm screen, never an error. */
    data object NotAvailable : DatingRootState

    data class Failed(val message: String) : DatingRootState

    data class Step(
        val step: OnboardingStep,
        val profile: DatingProfileDto?,
        val preferences: PreferencesDto?,
        val identityIncomplete: Boolean,
    ) : DatingRootState
}

/**
 * Dating's entry: the access probe first, then the profile and the gate.
 *
 * The first call is `GET /consents` — it exists for every user whether or not
 * they have a profile — so a 404 there can only be the pilot allowlist. Every
 * step screen reports completion by asking this to [reload]: the server
 * advances the status, and the gate reads what it says.
 */
@HiltViewModel
class DatingRootViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
) : ViewModel() {

    private val _state = MutableStateFlow<DatingRootState>(DatingRootState.Loading)
    val state: StateFlow<DatingRootState> = _state.asStateFlow()

    init {
        reload()
    }

    fun reload() {
        viewModelScope.launch { load() }
    }

    /** Resume from a pause. The server restores the remembered step and never lifts a moderation hold. */
    fun unpause() {
        viewModelScope.launch {
            when (val result = repository.setPaused(false)) {
                is DatingResult.Success -> load()
                is DatingResult.Failure -> _state.value = DatingRootState.Failed(DatingCopy.forError(result.error))
            }
        }
    }

    private suspend fun load() {
        if (_state.value !is DatingRootState.Step) _state.value = DatingRootState.Loading
        when (val access = repository.access()) {
            is DatingResult.Failure -> {
                _state.value = if (access.error == DatingError.NotAvailable) {
                    DatingRootState.NotAvailable
                } else {
                    DatingRootState.Failed(DatingCopy.forError(access.error))
                }
                return
            }
            is DatingResult.Success -> session.setConsents(access.value)
        }
        val profile = when (val result = repository.profile()) {
            is DatingResult.Failure -> {
                _state.value = if (result.error == DatingError.NotAvailable) {
                    DatingRootState.NotAvailable
                } else {
                    DatingRootState.Failed(DatingCopy.forError(result.error))
                }
                return
            }
            is DatingResult.Success -> result.value
        }
        session.setProfile(profile)
        val preferences = if (profile?.profileStatus == OnboardingGate.STATUS_DRAFT) {
            (repository.preferences() as? DatingResult.Success)?.value
        } else {
            null
        }
        _state.value = DatingRootState.Step(
            step = OnboardingGate.stepFor(profile, preferences),
            profile = profile,
            preferences = preferences,
            identityIncomplete = profile != null && OnboardingGate.identityIncomplete(profile),
        )
    }
}

data class OnboardingUiState(
    val saving: Boolean = false,
    /** A consent the save is waiting on. Nothing it covers has been sent. */
    val consentPrompt: ConsentType? = null,
    val location: LocationStep = LocationStep.Idle,
    val message: UsMessage? = null,
    /** Bumped each time a step is saved; the root reloads the gate on a change. */
    val savedCount: Int = 0,
)

/**
 * The draft steps — create, basics, location, preferences.
 *
 * Name and birth date are never sent: identity supplies them, and the screen
 * shows them read-only. Religion and community are sent only after their
 * consent is granted IN FLOW; a declined consent drops the field from the save.
 */
@HiltViewModel
class OnboardingViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val locationSource: CurrentLocationSource,
) : ViewModel() {

    private val _state = MutableStateFlow(OnboardingUiState())
    val state: StateFlow<OnboardingUiState> = _state.asStateFlow()

    private val permissionFlow = LocationPermissionFlow()
    private var pendingBasics: UpsertProfileRequest? = null
    private val declined = mutableSetOf<ConsentType>()

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** Creates the draft profile; identity fills the name and birth date server-side. */
    fun create(intent: String) = save(UpsertProfileRequest(intent = intent))

    fun saveBasics(request: UpsertProfileRequest) {
        if (_state.value.saving) return
        val missing = ConsentGate.missingFor(request, session.consents.value).filterNot { it in declined }
        if (missing.isNotEmpty()) {
            pendingBasics = request
            _state.update { it.copy(consentPrompt = missing.first()) }
            return
        }
        pendingBasics = null
        save(ConsentGate.withoutDeclined(request, declined))
    }

    /** The answer to [OnboardingUiState.consentPrompt]. The pending save resumes either way. */
    fun onConsentAnswered(granted: Boolean) {
        val type = _state.value.consentPrompt ?: return
        val request = pendingBasics ?: return
        _state.update { it.copy(consentPrompt = null) }
        if (!granted) {
            declined += type
            saveBasics(request)
            return
        }
        viewModelScope.launch {
            _state.update { it.copy(saving = true) }
            when (val result = repository.setConsent(type.wire, granted = true)) {
                is DatingResult.Success -> {
                    session.setConsents(result.value)
                    declined -= type
                    _state.update { it.copy(saving = false) }
                    saveBasics(request)
                }
                is DatingResult.Failure -> _state.update {
                    it.copy(saving = false, message = DatingCopy.message(result.error))
                }
            }
        }
    }

    fun savePreferences(interestedIn: String, minAge: Int, maxAge: Int, distanceKm: Int) {
        if (_state.value.saving) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            val request = PreferencesRequest(
                minAge = minAge.coerceIn(MIN_AGE, MAX_AGE),
                maxAge = maxAge.coerceIn(MIN_AGE, MAX_AGE),
                distanceKm = distanceKm.coerceIn(MIN_DISTANCE_KM, MAX_DISTANCE_KM),
                interestedInGender = interestedIn,
            )
            when (val result = repository.updatePreferences(request)) {
                is DatingResult.Success -> _state.update { it.copy(saving = false, savedCount = it.savedCount + 1) }
                is DatingResult.Failure -> _state.update { it.copy(saving = false, message = preferencesFailure(result.error)) }
            }
        }
    }

    // ── Location: rationale, then the system prompt, then one fix ───────────


    /** Returns what the screen must do next. */
    fun onUseMyLocation(): LocationEffect {
        val effect = permissionFlow.onUseCurrentLocation(locationSource.hasPermission())
        syncLocation()
        if (effect == LocationEffect.FetchLocation) fetchLocation()
        return effect
    }

    fun onRationaleAccepted(): LocationEffect = permissionFlow.onRationaleAccepted().also { syncLocation() }

    fun onRationaleDismissed() {
        permissionFlow.onRationaleDismissed()
        syncLocation()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) {
        val effect = permissionFlow.onPermissionResult(granted, canAskAgain)
        syncLocation()
        if (effect == LocationEffect.FetchLocation) fetchLocation()
    }

    fun saveCity(city: String) {
        val trimmed = city.trim()
        if (trimmed.isEmpty()) {
            _state.update { it.copy(message = DatingCopy.message(DatingError.Unexpected(null, null)).copy(text = "Type your city.")) }
            return
        }
        save(UpsertProfileRequest(city = trimmed))
    }

    private fun fetchLocation() {
        viewModelScope.launch {
            val fix = locationSource.current()
            permissionFlow.onLocationResult(fix)
            syncLocation()
            if (fix != null) saveLocation(fix)
        }
    }

    private suspend fun saveLocation(fix: Coordinates) {
        val city = locationSource.cityOf(fix)
        save(UpsertProfileRequest(latitude = fix.latitude, longitude = fix.longitude, city = city))
    }

    private fun syncLocation() = _state.update { it.copy(location = permissionFlow.step) }

    // ── The one writer ───────────────────────────────────────────────────────

    private fun save(request: UpsertProfileRequest) {
        if (_state.value.saving) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            when (val result = repository.upsertProfile(request)) {
                is DatingResult.Success -> {
                    session.setProfile(result.value)
                    _state.update { it.copy(saving = false, savedCount = it.savedCount + 1) }
                }
                is DatingResult.Failure -> {
                    val error = result.error
                    if (error is DatingError.ConsentRequired) {
                        // The server's own consent check caught a field: ask, then resend.
                        val type = ConsentType.fromWire(error.consentType)
                        pendingBasics = request
                        _state.update { it.copy(saving = false, consentPrompt = type, message = if (type == null) DatingCopy.message(error) else null) }
                    } else {
                        _state.update { it.copy(saving = false, message = DatingCopy.message(error, repository.json)) }
                    }
                }
            }
        }
    }

    companion object {
        const val MIN_AGE = 18
        const val MAX_AGE = 120
        const val MIN_DISTANCE_KM = 1
        const val MAX_DISTANCE_KM = 500

        /**
         * The server validates `interested_in_gender` against
         * `woman | man | nonbinary | everyone` and refuses anything else with a
         * 400 INVALID_REQUEST — the generic code, so it is read HERE, where the
         * only field that can be invalid is the one the person just chose.
         */
        fun preferencesFailure(error: DatingError): UsMessage =
            if ((error as? DatingError.Refused)?.status == HTTP_BAD_REQUEST) {
                errorMessage("That choice isn't available any more. Pick who you want to see and try again.")
            } else {
                DatingCopy.message(error)
            }

        private const val HTTP_BAD_REQUEST = 400
    }
}
