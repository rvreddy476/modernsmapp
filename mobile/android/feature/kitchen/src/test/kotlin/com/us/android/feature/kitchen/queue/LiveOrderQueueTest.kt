package com.us.android.feature.kitchen.queue

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.KitchenOrderDto
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.realtime.RealtimeTokenSource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonNull
import org.junit.Test
import java.time.Instant

@OptIn(ExperimentalCoroutinesApi::class)
class LiveOrderQueueTest {

    private var fetches = 0
    private val order = KitchenOrderDto(id = "o-1", orderNumber = "F-1001", secondsToBreach = 90)

    private val fetch: suspend () -> FoodResult<List<KitchenOrderDto>> = {
        fetches++
        FoodResult.Success(listOf(order))
    }

    /**
     * Behaves like SseClient on a token failure: the exception is swallowed and
     * the client backs off and asks again, forever. The flow never errors.
     */
    private fun sseLike(tokenSource: RealtimeTokenSource, events: Flow<RealtimeEvent>? = null): Flow<RealtimeEvent> = flow {
        while (true) {
            val token = try {
                tokenSource.token(false)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                null
            }
            if (token == null) {
                delay(1_000)
                continue
            }
            emit(RealtimeEvent.Connected(subject = "u", topics = emptyList(), resumedFrom = null))
            events?.collect { emit(it) } ?: awaitCancellation()
        }
    }

    @Test
    fun `a failing token source falls back to polling every fifteen seconds`() = runTest {
        val failing = RealtimeTokenSource { throw RealtimeTokenNotWiredException() }
        val queue = LiveOrderQueue(fetch, { sseLike(it) }, failing, { Instant.EPOCH })

        backgroundScope.launch { queue.run() }
        runCurrent()

        assertThat(queue.state.value.transport).isEqualTo(QueueTransport.POLLING)
        assertThat(queue.state.value.orders).containsExactly(order)
        assertThat(fetches).isEqualTo(1)

        advanceTimeBy(15_000)
        runCurrent()
        assertThat(fetches).isEqualTo(2)

        advanceTimeBy(30_000)
        runCurrent()
        assertThat(fetches).isEqualTo(4)
        assertThat(queue.state.value.transport).isEqualTo(QueueTransport.POLLING)
    }

    @Test
    fun `the pending B5 adapter is a failing token source, so the kitchen polls`() = runTest {
        val tokens = PendingB5RestaurantRealtimeTokens().forRestaurant("r-1")
        val queue = LiveOrderQueue(fetch, { sseLike(it) }, tokens, { Instant.EPOCH })

        backgroundScope.launch { queue.run() }
        runCurrent()
        advanceTimeBy(15_000)
        runCurrent()

        assertThat(queue.state.value.transport).isEqualTo(QueueTransport.POLLING)
        assertThat(fetches).isEqualTo(2)
    }

    @Test
    fun `a stream that ends terminally leaves polling in charge`() = runTest {
        val queue = LiveOrderQueue(
            fetch = fetch,
            subscribe = { flow { throw IllegalStateException("403 NO_TOPICS") } },
            tokenSource = { "t" },
            clock = { Instant.EPOCH },
        )

        backgroundScope.launch { queue.run() }
        runCurrent()
        advanceTimeBy(15_000)
        runCurrent()

        assertThat(queue.state.value.transport).isEqualTo(QueueTransport.POLLING)
        assertThat(fetches).isEqualTo(2)
    }

    @Test
    fun `a connected stream slows polling to the sixty second reconcile`() = runTest {
        val queue = LiveOrderQueue(fetch, { sseLike(it) }, { "token" }, { Instant.EPOCH })

        backgroundScope.launch { queue.run() }
        runCurrent()
        assertThat(queue.state.value.transport).isEqualTo(QueueTransport.LIVE)
        val baseline = fetches
        assertThat(baseline).isAtLeast(1)

        advanceTimeBy(45_000)
        runCurrent()
        assertThat(fetches).isEqualTo(baseline)

        advanceTimeBy(15_000)
        runCurrent()
        assertThat(fetches).isEqualTo(baseline + 1)
    }

    @Test
    fun `a realtime event re-reads the queue at once`() = runTest {
        val events = MutableSharedFlow<RealtimeEvent>()
        val queue = LiveOrderQueue(fetch, { sseLike(it, events) }, { "token" }, { Instant.EPOCH })

        backgroundScope.launch { queue.run() }
        runCurrent()
        val baseline = fetches

        events.emit(RealtimeEvent.Message(id = "1-0", topic = "food.restaurant.r-1.orders", eventType = "food.order.placed", data = JsonNull, emittedAt = null))
        runCurrent()

        assertThat(fetches).isEqualTo(baseline + 1)
    }
}
