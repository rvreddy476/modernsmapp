package com.us.android.feature.doorsteppro.offers

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.OfferDto
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.domain.DeclineReason
import com.us.android.feature.doorsteppro.domain.OfferCountdown
import com.us.android.feature.doorsteppro.domain.OfferWindow
import com.us.android.feature.doorsteppro.domain.ProClock
import com.us.android.feature.doorsteppro.model.Paise
import com.us.android.feature.doorsteppro.model.toRupeeText
import com.us.android.feature.doorsteppro.navigation.requireArg
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LabeledValue
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.MessagePane
import com.us.android.feature.doorsteppro.ui.Pill
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.countdownText
import com.us.android.feature.doorsteppro.ui.distanceText
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.humanise
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.slotRangeText
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import javax.inject.Inject

/** Why an offer can no longer be answered. */
enum class OfferGone(val title: String, val body: String) {
    TAKEN("Someone else took this job", "Another professional accepted it first. New offers appear on Home."),
    EXPIRED("This offer has expired", "Its time ran out and it went to the next professional."),
    MISSING("This offer is no longer open", "It was answered, withdrawn or has expired."),
}

data class OfferUiState(
    val loading: Boolean = true,
    val offer: OfferDto? = null,
    val now: Instant = Instant.EPOCH,
    val gone: OfferGone? = null,
    val accepting: Boolean = false,
    val declining: Boolean = false,
    /** The accepted job's booking id: the screen moves to the job. */
    val acceptedBookingId: String? = null,
    val declined: Boolean = false,
    val message: UsMessage? = null,
) {
    val window: OfferWindow get() = offer?.let { OfferCountdown.of(it.expiresAt).at(now) } ?: OfferWindow.Unknown
}

/**
 * One offer, from a push, the realtime topic or Home: locality only (never
 * the address before acceptance), the slot, the distance, the earning
 * estimate and a countdown to `expires_at`. Accept is row-locked on the
 * server (409 DOORSTEP_OFFER_TAKEN, 410 DOORSTEP_OFFER_EXPIRED); decline
 * names a reason and dispatch moves on.
 */
@HiltViewModel
class OfferViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepProRepository,
    private val clock: ProClock,
) : ViewModel() {

    private val offerId = savedStateHandle.requireArg("offerId")

    private val _state = MutableStateFlow(OfferUiState(now = clock.now()))
    val state: StateFlow<OfferUiState> = _state.asStateFlow()

    init {
        load()
        viewModelScope.launch {
            while (true) {
                delay(TICK_MILLIS)
                _state.update { it.copy(now = clock.now()) }
            }
        }
    }

    fun load() {
        viewModelScope.launch {
            when (val result = repository.offers()) {
                is ProResult.Success -> {
                    val offer = result.value.firstOrNull { it.id == offerId }
                    _state.update { it.copy(loading = false, offer = offer, gone = if (offer == null) OfferGone.MISSING else null) }
                }
                is ProResult.Failure -> _state.update { it.copy(loading = false, message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun accept() {
        val s = _state.value
        if (s.accepting || s.offer == null) return
        if (!s.window.canRespond) {
            _state.update { it.copy(gone = OfferGone.EXPIRED) }
            return
        }
        _state.update { it.copy(accepting = true) }
        viewModelScope.launch {
            when (val result = repository.acceptOffer(offerId)) {
                is ProResult.Success -> _state.update { it.copy(accepting = false, acceptedBookingId = result.value.bookingId) }
                is ProResult.Failure -> when (result.error.code) {
                    ProCodes.OFFER_TAKEN -> _state.update { it.copy(accepting = false, gone = OfferGone.TAKEN) }
                    ProCodes.OFFER_EXPIRED -> _state.update { it.copy(accepting = false, gone = OfferGone.EXPIRED) }
                    ProCodes.OFFER_NOT_FOUND -> _state.update { it.copy(accepting = false, gone = OfferGone.MISSING) }
                    else -> _state.update { it.copy(accepting = false, message = errorMessage(result.error.userMessage())) }
                }
            }
        }
    }

    fun decline(reason: DeclineReason) {
        if (_state.value.declining) return
        _state.update { it.copy(declining = true) }
        viewModelScope.launch {
            when (val result = repository.declineOffer(offerId, reason.wire)) {
                is ProResult.Success -> _state.update { it.copy(declining = false, declined = true) }
                is ProResult.Failure -> _state.update { it.copy(declining = false, message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    private companion object {
        const val TICK_MILLIS = 1_000L
    }
}

@Composable
fun OfferScreen(onBack: () -> Unit, onAccepted: (String) -> Unit, viewModel: OfferViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var choosingReason by remember { mutableStateOf(false) }
    LaunchedEffect(state.acceptedBookingId) { state.acceptedBookingId?.let(onAccepted) }
    LaunchedEffect(state.declined) { if (state.declined) onBack() }
    if (choosingReason) {
        AlertDialog(
            onDismissRequest = { choosingReason = false },
            title = { Text("Why are you declining?") },
            text = {
                Column {
                    DeclineReason.entries.forEach { reason ->
                        TextButton(onClick = {
                            choosingReason = false
                            viewModel.decline(reason)
                        }) { Text(reason.label) }
                    }
                }
            },
            confirmButton = {},
            dismissButton = { TextButton(onClick = { choosingReason = false }) { Text("Keep the offer") } },
        )
    }
    ProScreen(title = "Job offer", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        val offer = state.offer
        val gone = state.gone
        when {
            state.loading -> LoadingPane()
            gone != null -> MessagePane(title = gone.title, body = gone.body, primaryLabel = "Back to home", onPrimary = onBack)
            offer == null -> MessagePane(title = "Couldn't load the offer", body = "", primaryLabel = "Try again", onPrimary = viewModel::load)
            else -> Column(
                modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                val window = state.window
                ProCard(highlighted = true) {
                    CardHeading(offer.serviceName, humanise(offer.categorySlug))
                    when (window) {
                        is OfferWindow.Open -> {
                            Text(
                                countdownText(window.remainingSeconds),
                                style = MaterialTheme.typography.displaySmall,
                                fontWeight = FontWeight.SemiBold,
                                color = if (window.urgent) UsTheme.extended.statusDanger else UsTheme.extended.textPrimary,
                            )
                            Text("left to answer", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        }
                        OfferWindow.Expired -> Pill("Expired", Tone.Danger)
                        OfferWindow.Unknown -> Unit
                    }
                }
                ProCard {
                    LabeledValue("When", slotRangeText(offer.slotStart, offer.slotEnd))
                    LabeledValue("Where", offer.locality)
                    LabeledValue("Distance", distanceText(offer.distanceM))
                    LabeledValue("You earn", "about ${Paise(offer.earningEstimatePaise).toRupeeText()}")
                    InfoNote("You'll see the full address and can chat with the customer once you accept.")
                }
                UsButton(
                    text = "Accept job",
                    onClick = viewModel::accept,
                    loading = state.accepting,
                    enabled = window.canRespond,
                    modifier = Modifier.fillMaxWidth(),
                )
                UsSecondaryButton(
                    text = "Decline",
                    onClick = { choosingReason = true },
                    enabled = window.canRespond && !state.declining,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
    }
}
