package com.us.android.feature.rider.offers

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryOfferDto
import com.us.android.core.food.realtime.FoodRealtimeScope
import com.us.android.core.food.realtime.FoodRealtimeTokens
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.model.SessionState
import com.us.android.core.realtime.SseClient
import com.us.android.feature.rider.job.JobActions
import com.us.android.feature.rider.location.RiderDuty
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.emitAll
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import javax.inject.Inject
import javax.inject.Singleton

/** What the rider's offer feed last read from the server. */
data class OfferSnapshot(
    /** Pending offers, newest view of the server; empty until [loaded]. */
    val offers: List<DeliveryOfferDto> = emptyList(),
    val job: DeliveryAssignmentDto? = null,
    /** The first offers read has answered (so an absent offer is really gone). */
    val loaded: Boolean = false,
    /** The server's clock against this device's, from the last offers read (food audit L1). */
    val clock: ServerClockOffset = ServerClockOffset.NONE,
    /** Expiries from the last read's `seconds_to_expiry`, on the server clock ([OfferExpiry]). */
    val countdowns: Map<String, Instant> = emptyMap(),
    val transport: LiveTransport = LiveTransport.CONNECTING,
) {
    /** [offer]'s countdown at device time [deviceNow]. */
    fun windowOf(offer: DeliveryOfferDto, deviceNow: Instant): OfferWindow =
        OfferCountdown(OfferExpiry.of(offer, countdowns)).at(clock.serverNow(deviceNow))
}

/**
 * The rider's offers and current job, kept fresh by ONE [RiderLiveFeed] for
 * the whole process (food audit H1, L5).
 *
 * It runs only while someone holds it: the location service for as long as
 * the rider is on duty (so an offer reaches a rider whose screen is off or who
 * has Maps in front), and Home and the offer screen while they are visible.
 * With no holder the stream and the poll stop, so a backgrounded, off-duty app
 * keeps no radio awake. The current job also sets [RiderDuty.onJob] here, so
 * the cadence follows the job whichever screen is open.
 *
 * Holds are taken and released on the main thread.
 */
@Singleton
class RiderOfferWatch @Inject constructor(
    private val rider: RiderRepository,
    private val duty: RiderDuty,
    private val sseClient: SseClient,
    private val tokens: FoodRealtimeTokens,
    private val session: SessionStateProvider,
    private val clock: RiderClock,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val _state = MutableStateFlow(OfferSnapshot())
    val state: StateFlow<OfferSnapshot> = _state.asStateFlow()

    private var holders = 0
    private var running: Job? = null
    private var feed: RiderLiveFeed? = null

    /** Keeps the feed running until the returned hold is released. Releasing twice is the same as once. */
    fun hold(): OfferWatchHold {
        holders++
        if (running == null) start()
        var released = false
        return OfferWatchHold {
            if (!released) {
                released = true
                holders--
                if (holders == 0) stop()
            }
        }
    }

    /** The snapshot, holding the feed for as long as it is collected. */
    fun observe(): Flow<OfferSnapshot> = flow {
        val hold = hold()
        try {
            emitAll(state)
        } finally {
            hold.release()
        }
    }

    /** Re-read the server now (after an accept, a decline, or a push). A no-op while nobody holds the feed. */
    fun refreshNow() {
        feed?.refreshNow()
    }

    private fun start() {
        val userId = (session.sessionState.value as? SessionState.Authenticated)?.userId ?: return
        val live = RiderLiveFeed(
            refresh = { refresh() },
            subscribe = { source -> sseClient.connect(RiderRealtimeTopics.forRider(userId), source) },
            tokenSource = tokens.forScope(FoodRealtimeScope.Delivery),
            // Show a pushed offer at once; the refresh the same frame triggers stays the authority.
            onMessage = { message -> _state.update { it.copy(offers = LiveOffers.upsert(it.offers, message)) } },
        )
        feed = live
        running = scope.launch {
            launch { live.transport.collect { t -> _state.update { it.copy(transport = t) } } }
            live.run()
        }
    }

    // Offers are not kept across a stop: by the next start they are stale, and
    // an offer screen must not count down one the server has moved on.
    private fun stop() {
        running?.cancel()
        running = null
        feed = null
        _state.update { OfferSnapshot(job = it.job) }
    }

    private suspend fun refresh() {
        val offers = rider.offers()
        val receivedAt = clock.now()
        val job = rider.currentAssignment()
        _state.update { current ->
            if (offers is FoodResult.Success) {
                val serverClock = ServerClockOffset.from(offers.value.serverTime, receivedAt)
                current.copy(
                    offers = offers.value.value,
                    loaded = true,
                    clock = serverClock,
                    countdowns = OfferExpiry.fromCountdowns(offers.value.value, serverClock.serverNow(receivedAt)),
                    job = if (job is FoodResult.Success) job.value else current.job,
                )
            } else {
                current.copy(job = if (job is FoodResult.Success) job.value else current.job)
            }
        }
        if (job is FoodResult.Success) duty.setOnJob(job.value?.let { JobActions.of(it).isActive } == true)
    }
}

/** One holder's claim on [RiderOfferWatch]. */
fun interface OfferWatchHold {
    fun release()
}
