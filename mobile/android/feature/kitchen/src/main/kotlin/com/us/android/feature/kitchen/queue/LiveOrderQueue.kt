package com.us.android.feature.kitchen.queue

import com.us.android.core.food.network.KitchenOrderDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.realtime.RealtimeTokenSource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant

/** How the queue is currently kept fresh. */
enum class QueueTransport {
    /** Before the stream has either connected or failed. Polls at the fallback rate. */
    CONNECTING,

    /** The SSE stream is connected: events trigger refreshes, and a slow poll reconciles. */
    LIVE,

    /** The stream is unavailable (token refused, stream ended). Polls every 15 s. */
    POLLING,
}

data class QueueSnapshot(
    val orders: List<KitchenOrderDto> = emptyList(),
    /** Device time the orders were fetched; deadlines are relative to it. */
    val fetchedAt: Instant? = null,
    val transport: QueueTransport = QueueTransport.CONNECTING,
    /** The last fetch's failure, cleared by the next success. Orders already shown stay. */
    val error: FoodError? = null,
    val loaded: Boolean = false,
)

/**
 * The kitchen's incoming-order queue: realtime when it can be, polling when it
 * cannot, and never neither.
 *
 * The SSE stream (`:core:realtime`) is the fast path — any event on the
 * restaurant's topics triggers a re-read of `GET …/kitchen-queue`. The server
 * state is always re-read rather than patched from event payloads, so a missed
 * or reordered event cannot leave a wrong queue on screen.
 *
 * FALLBACK: when the token source throws — the scoped restaurant token route
 * refused ([ScopedRestaurantRealtimeTokens]) — or the stream ends,
 * [QueueTransport.POLLING] re-reads every
 * [pollFallbackMillis]. While LIVE a slower [reconcileMillis] poll continues,
 * because `SseClient` reconnects silently and a dropped stream is otherwise
 * invisible here until the next token refresh.
 *
 * All fetches go through one conflated channel, so events and ticks arriving
 * together cause one request, never a burst.
 */
class LiveOrderQueue(
    private val fetch: suspend () -> FoodResult<List<KitchenOrderDto>>,
    private val subscribe: (RealtimeTokenSource) -> Flow<RealtimeEvent>,
    private val tokenSource: RealtimeTokenSource,
    private val clock: KitchenClock,
    private val pollFallbackMillis: Long = POLL_FALLBACK_MILLIS,
    private val reconcileMillis: Long = RECONCILE_MILLIS,
) {
    private val _state = MutableStateFlow(QueueSnapshot())
    val state: StateFlow<QueueSnapshot> = _state.asStateFlow()

    private val refreshRequests = Channel<Unit>(Channel.CONFLATED)

    /** Asks for a re-read now — after an accept, a pull-to-refresh, an event. */
    fun refreshNow() {
        refreshRequests.trySend(Unit)
    }

    /** Runs until the calling scope is cancelled. */
    suspend fun run() {
        coroutineScope {
            launch { refresher() }
            launch { streamLoop() }
            launch { pollLoop() }
        }
    }

    private suspend fun refresher() {
        for (request in refreshRequests) {
            val result = fetch()
            val now = clock.now()
            _state.update { current ->
                when (result) {
                    is FoodResult.Success -> current.copy(
                        orders = result.value,
                        fetchedAt = now,
                        error = null,
                        loaded = true,
                    )
                    is FoodResult.Failure -> current.copy(error = result.error, loaded = true)
                }
            }
        }
    }

    private suspend fun pollLoop() {
        refreshNow()
        var sinceLastPoll = 0L
        while (true) {
            delay(pollFallbackMillis)
            sinceLastPoll += pollFallbackMillis
            val live = _state.value.transport == QueueTransport.LIVE
            if (!live || sinceLastPoll >= reconcileMillis) {
                refreshNow()
                sinceLastPoll = 0L
            }
        }
    }

    @Suppress("TooGenericExceptionCaught")
    private suspend fun streamLoop() {
        val observed = RealtimeTokenSource { forceRefresh ->
            try {
                tokenSource.token(forceRefresh)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // SseClient swallows this and backs off; this is the only
                // moment the queue can learn the stream is not coming.
                setTransport(QueueTransport.POLLING)
                throw e
            }
        }
        try {
            subscribe(observed).collect { event ->
                when (event) {
                    is RealtimeEvent.Connected -> {
                        setTransport(QueueTransport.LIVE)
                        refreshNow()
                    }
                    is RealtimeEvent.Message -> refreshNow()
                    is RealtimeEvent.Unparsed -> refreshNow()
                }
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // A terminal refusal (RealtimeException) — polling carries on.
        }
        setTransport(QueueTransport.POLLING)
    }

    private fun setTransport(transport: QueueTransport) {
        _state.update { if (it.transport == transport) it else it.copy(transport = transport) }
    }

    companion object {
        const val POLL_FALLBACK_MILLIS = 15_000L
        const val RECONCILE_MILLIS = 60_000L
    }
}
