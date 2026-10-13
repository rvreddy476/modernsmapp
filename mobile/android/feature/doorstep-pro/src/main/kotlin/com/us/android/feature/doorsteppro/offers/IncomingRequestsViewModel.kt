package com.us.android.feature.doorsteppro.offers

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.notifications.DoorstepOfferAlertCoordinator
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.OfferDto
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.di.ProEventStream
import com.us.android.feature.doorsteppro.domain.OfferCountdown
import com.us.android.feature.doorsteppro.domain.OfferWindow
import com.us.android.feature.doorsteppro.domain.ProClock
import com.us.android.feature.doorsteppro.realtime.ProLiveFeed
import com.us.android.feature.doorsteppro.realtime.ProRealtimeTokenSource
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.time.Instant
import javax.inject.Inject

data class IncomingRequestsState(
    val offers: List<OfferDto> = emptyList(),
    val now: Instant = Instant.EPOCH,
    val responding: Boolean = false,
    val muted: Set<String> = emptySet(),
    val error: String? = null,
    val acceptedBookingId: String? = null,
) {
    val incoming: OfferDto? get() = offers.firstOrNull { it.id !in muted }
}

/** Only server-open, unexpired requests can ring. An unreadable deadline fails closed. */
fun openRequests(offers: List<OfferDto>, now: Instant): List<OfferDto> = offers
    .filter { it.status == "open" && OfferCountdown.of(it.expiresAt).at(now) is OfferWindow.Open }
    .distinctBy { it.id }
    .sortedBy { it.expiresAt }

/** Owned by the signed-in shell, not one tab. The UI starts/stops the feed with its lifecycle. */
@HiltViewModel
class IncomingRequestsViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val stream: ProEventStream,
    private val clock: ProClock,
    internal val offerAlerts: DoorstepOfferAlertCoordinator,
) : ViewModel() {
    private val _state = MutableStateFlow(IncomingRequestsState(now = clock.now()))
    val state = _state.asStateFlow()
    private val lock = Mutex()
    private val answered = mutableSetOf<String>()
    private var responseJob: Job? = null

    suspend fun listen(userId: String) = coroutineScope {
        val feed = ProLiveFeed(
            refresh = { refresh() },
            subscribe = { stream.events(userId, it) },
            tokenSource = ProRealtimeTokenSource(repository),
            pollMillis = 3_000L,
            reconcileMillis = 3_000L,
        )
        launch { feed.run() }
        launch {
            while (true) {
                val now = clock.now()
                _state.update { it.copy(now = now, offers = openRequests(it.offers, now)) }
                delay(250L)
            }
        }
    }

    suspend fun refresh() = lock.withLock {
        when (val result = repository.offers()) {
            is ProResult.Success -> {
                val now = clock.now()
                _state.update { it.copy(now = now, offers = openRequests(result.value, now).filterNot { o -> o.id in answered }) }
            }
            is ProResult.Failure -> Unit // Keep the current deadline; never extend an offer during an outage.
        }
    }

    fun dismiss() {
        val id = _state.value.incoming?.id ?: return
        _state.update { it.copy(muted = it.muted + id, error = null) }
    }

    fun acceptedHandled() = _state.update { it.copy(acceptedBookingId = null) }

    fun respond(accept: Boolean) {
        val offer = _state.value.incoming ?: return
        if (_state.value.responding || openRequests(listOf(offer), clock.now()).isEmpty()) return
        _state.update { it.copy(responding = true, error = null) }
        responseJob = viewModelScope.launch {
            lock.withLock {
                val result = if (accept) repository.acceptOffer(offer.id) else repository.declineOffer(offer.id, null)
                when (result) {
                    is ProResult.Success -> {
                        answered += offer.id
                        val job = result.value as? com.us.android.feature.doorsteppro.data.ProJobDto
                        _state.update { it.copy(responding = false, offers = it.offers.filterNot { o -> o.id == offer.id }, acceptedBookingId = job?.bookingId) }
                    }
                    is ProResult.Failure -> _state.update {
                        it.copy(responding = false, error = result.error.userMessage())
                    }
                }
            }
            refresh() // Re-read after conflicts or a response; never assume local acceptance.
        }
    }

    fun reset() {
        responseJob?.cancel()
        answered.clear()
        _state.value = IncomingRequestsState(now = clock.now())
    }
}
