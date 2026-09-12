package com.us.android.core.feed.data

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.NotifyOn
import com.us.android.core.model.SessionState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The viewer's subscription edges toward the channels on screen (Tube
 * subscriptions, 2026-09-12), what decides whether a channel page offers
 * "Subscribe" or "Subscribed" with a bell.
 *
 * ## WHY A SECOND GRAPH BESIDE [FollowGraph]
 *
 * Subscribe is follow PLUS notify, made by the server in one call: the
 * client never sends a follow of its own beside a subscribe, and an
 * unsubscribe removes both. But the two edges are not the same edge. A
 * viewer can follow a creator without subscribing to their channel, and
 * the Subscriptions page shows only the channels they chose. So the
 * subscription is its own map, keyed by channel (the creator's user id),
 * read from `GET v1/channels/{ref}/subscription` once per channel and kept
 * for the life of the process, the way the follow graph keeps its edges.
 *
 * Writes are optimistic: the edge flips at once and is put back if the
 * server says no, so the button never lags the tap. The follow graph is
 * NOT touched here; it learns the follow the server made on its next
 * read, and a channel page shows the subscription, not the follow.
 */
@Singleton
class SubscriptionGraph @Inject constructor(
    private val api: ChannelApi,
    private val errorMapper: ErrorMapper,
    private val session: SessionStateProvider,
) {
    private val _edges = MutableStateFlow<Map<String, ChannelSubscription>>(emptyMap())

    /** Channel (user) id → the viewer's subscription toward it. Absent means not yet known. */
    val edges: StateFlow<Map<String, ChannelSubscription>> = _edges.asStateFlow()

    private val inFlight = mutableSetOf<String>()
    private val lock = Mutex()

    /** The signed-in user, or blank before the session resolves. */
    val ownId: String
        get() = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    /**
     * Fetches the edge for every channel not yet known. Each id is asked for
     * once even when two callers arrive at the same moment; a failed lookup
     * is simply not recorded, so the control stays hidden rather than
     * guessing.
     */
    suspend fun ensureKnown(channelIds: Collection<String>) {
        val viewer = ownId
        if (viewer.isBlank()) return
        val wanted = lock.withLock {
            channelIds.asSequence()
                .filter { it.isNotBlank() && it != viewer }
                .filter { it !in _edges.value && it !in inFlight }
                .distinct()
                .toList()
                .also { inFlight += it }
        }
        if (wanted.isEmpty()) return
        try {
            coroutineScope {
                wanted.map { id -> async { id to apiCall(errorMapper) { api.subscription(id) } } }.awaitAll()
            }.forEach { (id, result) ->
                if (result is AppResult.Success) {
                    _edges.update { it + (id to result.data.toDomain()) }
                }
            }
        } finally {
            lock.withLock { inFlight -= wanted.toSet() }
        }
    }

    /**
     * Records what a channel read already told us (`is_subscribed` /
     * `notify_on` ride on `GET v1/channels/{ref}` for a signed-in caller),
     * so the page does not make a second request for an answer it has.
     */
    fun record(channelId: String, subscription: ChannelSubscription) {
        if (channelId.isBlank()) return
        _edges.update { it + (channelId to subscription) }
    }

    /**
     * Subscribes to [ref], optimistically, with the bell on, since every
     * subscriber is notified by default (founder). Returns the server's
     * verdict; on failure the edge is what it was before the tap.
     */
    suspend fun subscribe(ref: String): AppResult<Unit> {
        val before = _edges.value[ref] ?: ChannelSubscription.NOT_SUBSCRIBED
        _edges.update { it + (ref to ChannelSubscription(subscribed = true)) }
        return when (val result = apiCall(errorMapper) { api.subscribe(ref, SubscribeRequest()) }) {
            is AppResult.Success -> {
                _edges.update {
                    it + (ref to ChannelSubscription(subscribed = true, notifyOn = NotifyOn.fromWire(result.data.notifyOn)))
                }
                AppResult.Success(Unit)
            }
            is AppResult.Failure -> {
                _edges.update { it + (ref to before) }
                result
            }
        }
    }

    /** Unsubscribes from [ref], optimistically. The server removes the follow with it. */
    suspend fun unsubscribe(ref: String): AppResult<Unit> {
        val before = _edges.value[ref] ?: ChannelSubscription(subscribed = true)
        _edges.update { it + (ref to ChannelSubscription.NOT_SUBSCRIBED) }
        return when (val result = apiCall(errorMapper) { api.unsubscribe(ref) }) {
            is AppResult.Success -> AppResult.Success(Unit)
            is AppResult.Failure -> {
                _edges.update { it + (ref to before) }
                result
            }
        }
    }

    /**
     * The bell: sets [notifyOn] on an existing subscription, optimistically.
     * On failure the previous setting is restored, whatever it was, so a
     * refused "none" does not leave the bell drawn off while the server
     * still notifies.
     */
    suspend fun setNotifyOn(ref: String, notifyOn: NotifyOn): AppResult<Unit> {
        val before = _edges.value[ref] ?: ChannelSubscription(subscribed = true)
        _edges.update { it + (ref to ChannelSubscription(subscribed = true, notifyOn = notifyOn)) }
        return when (val result = apiCall(errorMapper) { api.updateSubscription(ref, NotifyOnRequest(notifyOn.wire)) }) {
            is AppResult.Success -> {
                _edges.update { it + (ref to result.data.toDomain()) }
                AppResult.Success(Unit)
            }
            is AppResult.Failure -> {
                _edges.update { it + (ref to before) }
                result
            }
        }
    }

    private fun SubscriptionDto.toDomain() = ChannelSubscription(
        subscribed = subscribed,
        notifyOn = NotifyOn.fromWire(notifyOn),
    )
}

/**
 * Whether a channel page should offer "Subscribe" for [channelId].
 *
 * Only when the answer is KNOWN to be "not subscribed": never for the
 * viewer's own channel, never once subscribed, and never while the edge is
 * still unknown; a Subscribe button that appears and then flips to
 * "Subscribed" when the real answer lands is worse than one that arrives
 * late. The same rule as [offersFollow], for the same reason.
 */
fun offersSubscribe(ownId: String, channelId: String, edge: ChannelSubscription?): Boolean =
    channelId.isNotBlank() && channelId != ownId && edge?.subscribed == false
