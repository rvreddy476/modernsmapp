package com.us.android.feature.feast.tracking

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.FeastTrackingDto
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.feature.feast.cart.CartBill
import com.us.android.feature.feast.ui.errorMessage
import com.us.android.feature.feast.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import javax.inject.Inject

data class TrackingUiState(
    val loading: Boolean = true,
    val model: TrackingModel = TrackingModel(),
    val timeline: TimelineView? = null,
    val bill: CartBill? = null,
    /** Non-null only while PICKED_UP / OUT_FOR_DELIVERY. */
    val deliveryCode: String? = null,
    /** The SSE stream is connected. When false the screen is kept fresh by polling. */
    val live: Boolean = false,
    val cancelling: Boolean = false,
    val error: String? = null,
    val message: UsMessage? = null,
) {
    val order: FeastOrderDto? get() = model.order
    val canCancel: Boolean get() = order?.status in CANCELLABLE
}

private val CANCELLABLE = setOf("PLACED", "PAYMENT_PENDING", "CONFIRMED")

/**
 * One order, live: the status timeline, the rider's position and ETA from
 * `rider.location` frames, and the delivery code while the rider has the food.
 *
 * Realtime is the fast path; a poll keeps it honest (SSE reconnects silently,
 * so a dropped stream is otherwise invisible) and stands in when the stream is
 * refused. Any non-location event on the order topic re-reads the order rather
 * than patching it, so a missed event cannot leave a wrong status on screen.
 */
@HiltViewModel
class OrderTrackingViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: FeastRepository,
    private val stream: OrderEventStream,
    private val clock: FeastClock,
) : ViewModel() {

    private val orderId: String = checkNotNull(savedStateHandle.get<String>("orderId")) {
        "navigation argument 'orderId' is missing"
    }

    private val _state = MutableStateFlow(TrackingUiState())
    val state: StateFlow<TrackingUiState> = _state.asStateFlow()

    private var streamJob: Job? = null
    private var pollJob: Job? = null

    init {
        viewModelScope.launch {
            refresh()
            if (isLive()) {
                subscribe()
                poll()
            }
        }
    }

    fun refreshNow() {
        viewModelScope.launch { refresh() }
    }

    fun cancel() {
        if (!_state.value.canCancel || _state.value.cancelling) return
        _state.update { it.copy(cancelling = true) }
        viewModelScope.launch {
            when (val result = repository.cancelOrder(orderId, "Cancelled by customer")) {
                is FoodResult.Success -> {
                    applyOrder(result.value, tracking = null)
                    _state.update { it.copy(cancelling = false, message = successMessage("Order cancelled")) }
                }
                is FoodResult.Failure -> _state.update {
                    it.copy(cancelling = false, message = errorMessage(describe(result.error)))
                }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    private suspend fun refresh() {
        val order = viewModelScope.async { repository.order(orderId) }
        val tracking = viewModelScope.async { repository.tracking(orderId) }
        when (val o = order.await()) {
            is FoodResult.Failure -> _state.update {
                it.copy(loading = false, error = if (it.order == null) describe(o.error) else null)
            }
            is FoodResult.Success -> applyOrder(o.value, (tracking.await() as? FoodResult.Success)?.value)
        }
    }

    private fun applyOrder(order: FeastOrderDto, tracking: FeastTrackingDto?) {
        val now = clock.now()
        _state.update { current ->
            val readRider = tracking?.deliveryLocation?.let { point ->
                val lat = point.latitude
                val lng = point.longitude
                val at = point.recordedAt?.let(::parseServerTimestamp)
                if (lat != null && lng != null && at != null) RiderPosition(lat, lng, null, at) else null
            }
            val rider = listOfNotNull(current.model.rider, readRider).maxByOrNull { it.recordedAt }
            val model = current.model.copy(
                order = order,
                rider = rider,
                etaAt = order.etaAt?.let(::instantOrNull) ?: tracking?.etaAt?.let(::instantOrNull),
                etaSource = order.etaSource ?: tracking?.etaSource,
                customerLatitude = tracking?.customerLocation?.latitude ?: current.model.customerLatitude,
                customerLongitude = tracking?.customerLocation?.longitude ?: current.model.customerLongitude,
                lastUpdatedAt = now,
            )
            current.copy(
                loading = false,
                error = null,
                model = model,
                timeline = OrderTimeline.of(order),
                bill = CartBill.from(order),
                deliveryCode = DeliveryCodeRule.visibleCode(order),
            )
        }
        if (!OrderTimeline.isLive(order.status)) {
            streamJob?.cancel()
            pollJob?.cancel()
            _state.update { it.copy(live = false) }
        }
    }

    @Suppress("TooGenericExceptionCaught")
    private fun subscribe() {
        streamJob?.cancel()
        streamJob = viewModelScope.launch {
            try {
                stream.events(orderId).collect(::onEvent)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // Refused or ended: the poll keeps the screen fresh.
                _state.update { it.copy(live = false) }
            }
        }
    }

    private fun onEvent(event: RealtimeEvent) {
        if (event is RealtimeEvent.Connected) {
            _state.update { it.copy(live = true) }
            return
        }
        val frame = TrackingFrames.parse(orderId, event)
        if (frame != null) {
            _state.update { it.copy(model = TrackingFrames.apply(it.model, frame, clock.now())) }
            return
        }
        if (TrackingFrames.isOrderChange(orderId, event)) refreshNow()
    }

    private fun poll() {
        pollJob?.cancel()
        pollJob = viewModelScope.launch {
            while (true) {
                delay(if (_state.value.live) RECONCILE_MILLIS else POLL_MILLIS)
                refresh()
                if (!isLive()) break
            }
        }
    }

    private fun isLive(): Boolean = _state.value.order?.let { OrderTimeline.isLive(it.status) } ?: true

    private fun describe(error: FoodError): String = when (error) {
        is FoodError.Network -> "Check your connection and try again."
        FoodError.NotFound -> "We couldn't find that order."
        else -> error.serverMessage ?: "Something went wrong. Please try again."
    }

    private companion object {
        const val POLL_MILLIS = 10_000L
        const val RECONCILE_MILLIS = 30_000L
    }
}

/** RFC 3339, or Postgres `timestamptz::text` (`2026-09-13 06:30:00.123+00`) as the tracking read sends it. */
internal fun parseServerTimestamp(text: String): Instant? {
    instantOrNull(text)?.let { return it }
    val normalised = text.trim().replace(' ', 'T').let { t ->
        when {
            Regex("[+-]\\d{2}$").containsMatchIn(t) -> "$t:00"
            else -> t
        }
    }
    return runCatching { java.time.OffsetDateTime.parse(normalised).toInstant() }.getOrNull()
}
