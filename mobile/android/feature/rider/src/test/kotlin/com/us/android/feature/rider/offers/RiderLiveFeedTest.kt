package com.us.android.feature.rider.offers

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.RealtimeTokenRequest
import com.us.android.core.food.realtime.FoodRealtimeScope
import com.us.android.core.food.realtime.FoodRealtimeTokens
import com.us.android.core.food.realtime.RealtimeTokenIssuer
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.core.realtime.RealtimeTokenSource
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class RiderLiveFeedTest {

    private var refreshes = 0

    /** Behaves like SseClient: a token failure is swallowed and retried after a backoff. */
    private fun sseLike(tokenSource: RealtimeTokenSource): Flow<RealtimeEvent> = flow {
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
            awaitCancellation()
        }
    }

    @Test
    fun `the rider asks for a delivery token with no id, on the rider's own topic`() = runTest {
        val requests = mutableListOf<RealtimeTokenRequest>()
        val issuer = RealtimeTokenIssuer { requests += it; FoodResult.Failure(FoodError.NotFound) }

        runCatching { FoodRealtimeTokens(issuer).forScope(FoodRealtimeScope.Delivery).token(false) }

        assertThat(NetworkModule.provideJson().encodeToString(RealtimeTokenRequest.serializer(), requests.single()))
            .isEqualTo("""{"scope":"delivery"}""")
        assertThat(RiderRealtimeTopics.forRider("u-1")).containsExactly("food.delivery_partner.u-1.assignments")
    }

    @Test
    fun `a refused delivery token leaves offers polling every fifteen seconds`() = runTest {
        val refused = FoodRealtimeTokens { FoodResult.Failure(FoodError.NotFound) }.forScope(FoodRealtimeScope.Delivery)
        val feed = RiderLiveFeed(refresh = { refreshes++ }, subscribe = { sseLike(it) }, tokenSource = refused)

        backgroundScope.launch { feed.run() }
        runCurrent()
        assertThat(feed.transport.value).isEqualTo(LiveTransport.POLLING)
        assertThat(refreshes).isEqualTo(1)

        advanceTimeBy(15_000)
        runCurrent()
        assertThat(refreshes).isEqualTo(2)
    }

    @Test
    fun `a live stream slows polling to the reconcile interval`() = runTest {
        val feed = RiderLiveFeed(refresh = { refreshes++ }, subscribe = { sseLike(it) }, tokenSource = { "tok" })

        backgroundScope.launch { feed.run() }
        runCurrent()
        assertThat(feed.transport.value).isEqualTo(LiveTransport.LIVE)
        val baseline = refreshes

        advanceTimeBy(45_000)
        runCurrent()
        assertThat(refreshes).isEqualTo(baseline)
    }
}
