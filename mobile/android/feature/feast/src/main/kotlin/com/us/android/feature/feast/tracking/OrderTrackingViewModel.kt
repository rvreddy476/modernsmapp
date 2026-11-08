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
import com.us.android.feature.feast.orders.OrderActions
import com.us.android.feature.feast.orders.RefundOutcome
import com.us.android.feature.feast.orders.RefundRequestFlow
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
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.withLock
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
    /** A refund request is being sent, or was accepted for review on this visit. */
    val refunding: Boolean = false,
    val refundRequested: Boolean = false,
    val error: String? = null,
    val message: UsMessage? = null,
) {
    val order: FeastOrderDto? get() = model.order
    val canCancel: Boolean get() = order?.let(OrderActions::canCancel) == true
    val canPayNow: Boolean get() = order?.let(OrderActions::canPayNow) == true
    val canRequestRefund: Boolean get() = !refundRequested && order?.let(OrderActions::canRequestRefund) == true
}

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
    updates: com.us.android.core.notifications.NotificationUpdates = com.us.android.core.notifications.NotificationUpdates(),
) : ViewModel() {
    val trackingUpdates = updates.events
    private val readMutex = kotlinx.coroutines.sync.Mutex()

    private val orderId: String = checkNotNull(savedStateHandle.get<String>("orderId")) {
        "navigation argument 'orderId' is missing"
    }

    private val _state = MutableStateFlow(TrackingUiState())
    val state: StateFlow<TrackingUiState> = _state.asStateFlow()

    private var streamJob: Job? = null
    private var pollJob: Job? = null
    private val refunds = RefundRequestFlow(repository)

    init {
        viewModelScope.launch {
            refresh()
            followWhileShown()
        }
    }

    /**
     * The stream and the poll run only while the screen collects [state]
     * (collectAsStateWithLifecycle stops at ON_STOP), so tracking left open in
     * the background stops using the radio (food audit L5). Coming back
     * re-reads the order before following again.
     */
    private suspend fun followWhileShown() {
        var firstShow = true
        _state.subscriptionCount.map { it > 0 }.distinctUntilChanged().collectLatest { shown ->
            if (!shown) {
                streamJob?.cancel()
                pollJob?.cancel()
                _state.update { it.copy(live = false) }
                return@collectLatest
            }
            if (!firstShow) refresh()
            firstShow = false
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
                is FoodResult.Failure -> {
                    val afterPreparing = (result.error as? FoodError.Unexpected)?.code == CANCEL_AFTER_PREPARING
                    _state.update {
                        it.copy(
                            cancelling = false,
                            message = errorMessage(if (afterPreparing) KITCHEN_STARTED else describe(result.error)),
                        )
                    }
                    // The server's can_cancel is now false: re-read so Cancel gives way to a refund request.
                    if (afterPreparing) refresh()
                }
            }
        }
    }

    /** Files a refund request for the server's total under a support ticket. Feast support decides. */
    fun requestRefund(detail: String?) {
        val current = _state.value
        val order = current.order ?: return
        if (!current.canRequestRefund || current.refunding) return
        _state.update { it.copy(refunding = true) }
        viewModelScope.launch {
            when (val outcome = refunds.submit(order, detail)) {
                is RefundOutcome.Requested -> _state.update {
                    it.copy(refunding = false, refundRequested = true, message = successMessage(REFUND_REQUESTED))
                }
                is RefundOutcome.Refused -> _state.update {
                    it.copy(refunding = false, message = errorMessage(outcome.message))
                }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    private suspend fun refresh() = readMutex.withLock {
        val order = viewModelScope.async { repository.order(orderId) }
        val tracking = viewModelScope.async { repository.tracking(orderId) }
        when (val o = order.await()) {
            is FoodResult.Failure -> _state.update {
                it.copy(loading = false, live = false, deliveryCode = null, error = if (it.order == null) describe(o.error) else "Updates unavailable. Showing your last loaded order.")
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
        if (event is RealtimeEvent.Disconnected) { _state.update { it.copy(live = false) }; return }
        if (event is RealtimeEvent.Connected) {
            _state.update { it.copy(live = true) }
            refreshNow()
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

    companion object {
        const val KITCHEN_STARTED =
            "The kitchen has already started on this order, so it can't be cancelled. You can request a refund instead."
        const val REFUND_REQUESTED = "Refund requested. Feast support will review it and let you know."

        private const val CANCEL_AFTER_PREPARING = "FOOD_CANCEL_AFTER_PREPARING"
        private const val POLL_MILLIS = 10_000L
        private const val RECONCILE_MILLIS = 30_000L
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
