package com.us.android.feature.kitchen.queue

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.KitchenOrderDto
import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenOrderAction
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.core.food.repository.code
import com.us.android.core.realtime.SseClient
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.ui.OrderStatusText
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.info
import com.us.android.feature.kitchen.ui.success
import com.us.android.feature.kitchen.ui.userMessage
import com.us.android.feature.kitchen.ui.warning
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import java.util.UUID
import javax.inject.Inject

data class IncomingOrderUi(val order: KitchenOrderDto, val window: AcceptWindow)

data class OrdersUiState(
    val incoming: List<IncomingOrderUi> = emptyList(),
    val preparing: List<PartnerOrderDto> = emptyList(),
    val awaitingPickup: List<PartnerOrderDto> = emptyList(),
    val transport: QueueTransport = QueueTransport.CONNECTING,
    val loaded: Boolean = false,
    val error: String? = null,
    val busyOrderIds: Set<String> = emptySet(),
    val message: UsMessage? = null,
)

/**
 * The live kitchen: incoming orders with their accept countdowns, orders being
 * cooked, and orders waiting for a rider.
 *
 * Runs for as long as the Kitchen home destination is on the back stack — also
 * while the app is in the background — because a kitchen that stops listening
 * when the screen dims misses orders. The looping alert follows
 * [NewOrderAlertPolicy]: it sounds while any order can still be accepted.
 *
 * Every transition carries an Idempotency-Key that is kept only while the
 * outcome is unknown (a network failure), so a retry of the same tap is safe
 * and a new tap is a new request.
 */
@HiltViewModel
class OrderQueueViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val kitchen: KitchenRepository,
    sseClient: SseClient,
    tokens: RestaurantRealtimeTokens,
    private val clock: KitchenClock,
    private val alert: NewOrderAlert,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()

    private val queue = LiveOrderQueue(
        fetch = { kitchen.kitchenQueue(restaurantId) },
        subscribe = { source -> sseClient.connect(KitchenRealtimeTopics.forRestaurant(restaurantId), source) },
        tokenSource = tokens.forRestaurant(restaurantId),
        clock = clock,
    )

    private val countdowns = HashMap<String, AcceptCountdown>()
    private val idempotencyKeys = HashMap<Pair<String, KitchenOrderAction>, String>()
    private var queued: List<KitchenOrderDto> = emptyList()
    private var lastFetchedAt: Instant? = null

    private val _state = MutableStateFlow(OrdersUiState())
    val state: StateFlow<OrdersUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch { queue.run() }
        viewModelScope.launch { queue.state.collect { onSnapshot(it) } }
        viewModelScope.launch {
            while (true) {
                delay(TICK_MILLIS)
                publishIncoming()
            }
        }
    }

    fun refresh() {
        queue.refreshNow()
        viewModelScope.launch { refreshActive() }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    fun accept(orderId: String) {
        val countdown = countdowns[orderId]
        if (countdown != null && !countdown.canRespond(clock.now())) {
            _state.update { it.copy(message = warning("Too late to accept — this order's time ran out.")) }
            publishIncoming()
            return
        }
        respond(orderId, KitchenOrderAction.ACCEPT, reason = null, done = "Accepted. It's in the kitchen now.") { now ->
            countdowns[orderId]?.confirmAccepted(now)
        }
    }

    fun reject(orderId: String, reason: String) {
        respond(orderId, KitchenOrderAction.REJECT, reason = reason, done = "Order rejected.") { now ->
            countdowns[orderId]?.confirmRejected(now)
        }
    }

    fun markReady(orderId: String) {
        respond(orderId, KitchenOrderAction.MARK_READY, reason = null, done = "Marked ready. A rider is being found.") { }
    }

    override fun onCleared() {
        alert.silence()
    }

    private suspend fun onSnapshot(snapshot: QueueSnapshot) {
        val fetchedAt = snapshot.fetchedAt
        if (fetchedAt != null && fetchedAt != lastFetchedAt) {
            lastFetchedAt = fetchedAt
            val now = clock.now()
            queued = snapshot.orders
            snapshot.orders.forEach { order ->
                val deadline = KitchenDeadline.of(order, fetchedAt)
                val existing = countdowns[order.id]
                if (existing == null) {
                    countdowns[order.id] = AcceptCountdown(deadline, now)
                } else {
                    existing.reschedule(deadline, now)
                }
            }
            countdowns.keys.retainAll(snapshot.orders.map { it.id }.toSet())
            refreshActive()
        }
        _state.update {
            it.copy(transport = snapshot.transport, loaded = snapshot.loaded, error = snapshot.error?.userMessage())
        }
        publishIncoming()
    }

    private fun publishIncoming() {
        val now = clock.now()
        val incoming = queued.map { IncomingOrderUi(it, countdowns[it.id]?.tick(now) ?: AcceptWindow.NoDeadline) }
        _state.update { it.copy(incoming = incoming) }
        val windows = incoming.map { it.window }
        if (NewOrderAlertPolicy.shouldRing(windows)) {
            alert.ring(windows.count { it.canRespond })
        } else {
            alert.silence()
        }
    }

    private suspend fun refreshActive() {
        val result = kitchen.orders(restaurantId)
        if (result is FoodResult.Success) {
            _state.update { current ->
                current.copy(
                    preparing = result.value.filter { it.status == OrderStatusText.PREPARING },
                    awaitingPickup = result.value.filter { it.status in OrderStatusText.AWAITING_PICKUP },
                )
            }
        }
    }

    private fun respond(
        orderId: String,
        action: KitchenOrderAction,
        reason: String?,
        done: String,
        onSuccess: (Instant) -> Unit,
    ) {
        if (orderId in _state.value.busyOrderIds) return
        _state.update { it.copy(busyOrderIds = it.busyOrderIds + orderId) }
        viewModelScope.launch {
            val key = idempotencyKeys.getOrPut(orderId to action) { UUID.randomUUID().toString() }
            val result = kitchen.transition(orderId, action, key, reason)
            val outcomeUnknown = result is FoodResult.Failure && result.error is FoodError.Network
            if (!outcomeUnknown) idempotencyKeys.remove(orderId to action)
            _state.update { current ->
                current.copy(
                    busyOrderIds = current.busyOrderIds - orderId,
                    message = when (result) {
                        is FoodResult.Success -> success(done)
                        is FoodResult.Failure -> transitionMessage(result.error)
                    },
                )
            }
            if (result is FoodResult.Success) {
                onSuccess(clock.now())
                publishIncoming()
            }
            queue.refreshNow()
            refreshActive()
        }
    }

    private fun transitionMessage(error: FoodError): UsMessage = when (error.code) {
        "FOOD_ORDER_TRANSITION_NOT_ALLOWED", "FOOD_ORDER_STATUS_CONFLICT" -> info("This order has already moved on. Refreshing.")
        else -> error.asMessage()
    }

    private companion object {
        const val TICK_MILLIS = 1_000L
    }
}
