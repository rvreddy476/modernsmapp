package com.us.android.feature.rider.digilocker

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.KycCheckDto
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.deeplink.RiderDeepLink
import com.us.android.feature.rider.deeplink.RiderDeepLinkBus
import com.us.android.feature.rider.ui.error
import com.us.android.feature.rider.ui.success
import com.us.android.feature.rider.ui.userMessage
import com.us.android.feature.rider.ui.warning
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

data class DigiLockerUiState(
    val loading: Boolean = true,
    val verified: Boolean = false,
    val checks: List<KycCheckDto> = emptyList(),
    val starting: Boolean = false,
    val completing: Boolean = false,
    val message: UsMessage? = null,
)

/** Persists the pending `state` across the browser round trip, in app-private storage. */
@Singleton
class SharedPrefsDigiLockerStateStore @Inject constructor(
    @ApplicationContext context: Context,
) : DigiLockerStateStore {
    private val prefs = context.getSharedPreferences("rider_digilocker", Context.MODE_PRIVATE)

    override fun save(pending: PendingDigiLocker) {
        prefs.edit().putString(KEY_STATE, pending.state).putString(KEY_EXPIRES, pending.expiresAt).apply()
    }

    override fun load(): PendingDigiLocker? {
        val state = prefs.getString(KEY_STATE, null) ?: return null
        return PendingDigiLocker(state, prefs.getString(KEY_EXPIRES, null).orEmpty())
    }

    override fun clear() {
        prefs.edit().clear().apply()
    }

    private companion object {
        const val KEY_STATE = "state"
        const val KEY_EXPIRES = "expires_at"
    }
}

/**
 * Aadhaar through DigiLocker: `start` → browser → return link → `callback`.
 *
 * The return link arrives on [RiderDeepLinkBus] (the Activity publishes it);
 * this ViewModel consumes it, checks the echoed state ([DigiLockerReturnPolicy])
 * and only then posts `{state, code}`.
 */
@HiltViewModel
class DigiLockerViewModel @Inject constructor(
    private val rider: RiderRepository,
    private val store: DigiLockerStateStore,
    private val deepLinks: RiderDeepLinkBus,
) : ViewModel() {

    private val _state = MutableStateFlow(DigiLockerUiState())
    val state: StateFlow<DigiLockerUiState> = _state.asStateFlow()

    private val browser = Channel<String>(Channel.CONFLATED)

    /** Authorize URLs for the screen to open in the browser. */
    val openBrowser: Flow<String> = browser.receiveAsFlow()

    init {
        viewModelScope.launch { loadStatus() }
        viewModelScope.launch {
            deepLinks.incoming.collect { link ->
                if (link is RiderDeepLink.DigiLocker) {
                    deepLinks.consumed()
                    complete(link.link)
                }
            }
        }
    }

    fun start() {
        if (_state.value.starting) return
        _state.update { it.copy(starting = true) }
        viewModelScope.launch {
            when (val result = rider.startDigiLocker()) {
                is FoodResult.Success -> {
                    store.save(PendingDigiLocker(result.value.state, result.value.expiresAt))
                    _state.update { it.copy(starting = false) }
                    browser.trySend(result.value.authorizeUrl)
                }
                is FoodResult.Failure -> {
                    val outcome = DigiLockerOutcome.fromError(result.error)
                    _state.update { it.copy(starting = false, message = messageFor(outcome)) }
                }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    private suspend fun complete(link: DigiLockerReturn) {
        when (val check = DigiLockerReturnPolicy.check(link, store.load())) {
            is ReturnCheck.Complete -> {
                _state.update { it.copy(completing = true) }
                val outcome = DigiLockerOutcome.from(rider.completeDigiLocker(state = check.state, code = check.code))
                if (outcome is DigiLockerOutcome.Verified || outcome.startAgain) store.clear()
                _state.update { it.copy(completing = false, message = messageFor(outcome)) }
                loadStatus()
            }
            ReturnCheck.NothingPending -> _state.update {
                it.copy(message = warning("That DigiLocker link isn't from a verification started on this phone. Start again."))
            }
            ReturnCheck.StateMismatch -> _state.update {
                it.copy(message = error("That DigiLocker link doesn't match your verification, so it was ignored. Start again."))
            }
            is ReturnCheck.Declined -> {
                store.clear()
                _state.update { it.copy(message = warning("DigiLocker didn't share your documents. Start again when you're ready.")) }
            }
            ReturnCheck.MissingCode -> {
                store.clear()
                _state.update { it.copy(message = error("DigiLocker came back without a result. Start again.")) }
            }
        }
    }

    private suspend fun loadStatus() {
        val result = rider.kycStatus()
        _state.update { current ->
            when (result) {
                is FoodResult.Success -> current.copy(
                    loading = false,
                    verified = "aadhaar_digilocker" !in result.value.missing,
                    checks = result.value.checks,
                )
                is FoodResult.Failure -> current.copy(loading = false)
            }
        }
    }

    private fun messageFor(outcome: DigiLockerOutcome): UsMessage = when (outcome) {
        is DigiLockerOutcome.Verified -> success("Verified with DigiLocker.")
        DigiLockerOutcome.StartedByAnotherAccount ->
            error("This verification was started by another account on this phone. Start again from this account.")
        DigiLockerOutcome.LinkUsed -> error("That DigiLocker link was already used. Start again.")
        DigiLockerOutcome.LinkExpired -> error("That DigiLocker link expired. Start again.")
        DigiLockerOutcome.LinkInvalid -> error("That DigiLocker link isn't recognised. Start again.")
        DigiLockerOutcome.ProviderFailed -> error("DigiLocker couldn't complete the verification. Start again.")
        DigiLockerOutcome.DocumentInUse ->
            error("A licence or RC in your DigiLocker is already registered to another rider. Contact Feast support.")
        DigiLockerOutcome.NotAvailable -> warning("DigiLocker verification isn't available yet. Please try again later.")
        is DigiLockerOutcome.Failed -> error(outcome.error.userMessage())
    }
}
