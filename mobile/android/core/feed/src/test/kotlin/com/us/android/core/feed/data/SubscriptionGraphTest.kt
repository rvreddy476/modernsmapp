package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.NotifyOn
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.io.IOException

/**
 * When a channel page offers "Subscribe", and what the graph does about it
 * (Tube subscriptions, 2026-09-12).
 *
 * The rules mirror the follow graph's, because the button has the same
 * failure modes: never on the viewer's own channel, never while the answer
 * is unknown, and an optimistic flip that is put back when the server
 * refuses, so the page never shows a subscription the server does not have.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SubscriptionGraphTest {

    // ── The rule the channel page reads ──────────────────────────────

    @Test
    fun `subscribe is offered only for a known not-subscribed other channel`() {
        assertThat(offersSubscribe("me", "ada", ChannelSubscription.NOT_SUBSCRIBED)).isTrue()
    }

    @Test
    fun `the viewer's own channel never offers subscribe`() {
        assertThat(offersSubscribe("me", "me", ChannelSubscription.NOT_SUBSCRIBED)).isFalse()
    }

    @Test
    fun `an unknown edge does not offer subscribe`() {
        assertThat(offersSubscribe("me", "ada", edge = null)).isFalse()
    }

    @Test
    fun `a subscribed channel and a blank id offer nothing`() {
        assertThat(offersSubscribe("me", "ada", ChannelSubscription(subscribed = true))).isFalse()
        assertThat(offersSubscribe("me", "", ChannelSubscription.NOT_SUBSCRIBED)).isFalse()
    }

    // ── Learning the edges ───────────────────────────────────────────

    @Test
    fun `each channel is asked for once, never the viewer, and the bell is decoded`() = runTest {
        val api = RecordingChannelApi(subscribed = mutableMapOf("bob" to "none"))
        val graph = subscriptionGraph(api)

        graph.ensureKnown(listOf("ada", "bob", "ada", "me", ""))
        graph.ensureKnown(listOf("ada", "bob"))

        assertThat(api.subscriptionReads).containsExactly("ada", "bob")
        assertThat(graph.edges.value).containsExactly(
            "ada", ChannelSubscription.NOT_SUBSCRIBED,
            "bob", ChannelSubscription(subscribed = true, notifyOn = NotifyOn.NONE),
        )
    }

    @Test
    fun `nothing is asked before the session resolves`() = runTest {
        val api = RecordingChannelApi()
        val graph = subscriptionGraph(api, session = FakeSession(userId = null))

        graph.ensureKnown(listOf("ada"))

        assertThat(api.subscriptionReads).isEmpty()
    }

    @Test
    fun `a recorded edge from a channel read is not asked for again`() = runTest {
        val api = RecordingChannelApi()
        val graph = subscriptionGraph(api)

        graph.record("ada", ChannelSubscription(subscribed = true))
        graph.ensureKnown(listOf("ada"))

        assertThat(api.subscriptionReads).isEmpty()
        assertThat(graph.edges.value["ada"]?.subscribed).isTrue()
    }

    // ── Subscribing ──────────────────────────────────────────────────

    @Test
    fun `a subscribe flips the edge with the bell on and sends the request`() = runTest {
        val api = RecordingChannelApi()
        val graph = subscriptionGraph(api)
        graph.ensureKnown(listOf("ada"))

        val result = graph.subscribe("ada")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(api.subscribeRequests).containsExactly("ada")
        assertThat(graph.edges.value["ada"]).isEqualTo(ChannelSubscription(subscribed = true, notifyOn = NotifyOn.ALL))
    }

    @Test
    fun `an optimistic subscribe rolls back when the request throws`() = runTest {
        val api = RecordingChannelApi(subscribeFails = true)
        val graph = subscriptionGraph(api)
        graph.ensureKnown(listOf("ada"))

        val result = graph.subscribe("ada")

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat(graph.edges.value["ada"]).isEqualTo(ChannelSubscription.NOT_SUBSCRIBED)
    }

    @Test
    fun `an unsubscribe clears the edge and rolls back on failure`() = runTest {
        val api = RecordingChannelApi(subscribed = mutableMapOf("ada" to "all"))
        val graph = subscriptionGraph(api)
        graph.ensureKnown(listOf("ada"))

        graph.unsubscribe("ada")
        assertThat(api.unsubscribeRequests).containsExactly("ada")
        assertThat(graph.edges.value["ada"]).isEqualTo(ChannelSubscription.NOT_SUBSCRIBED)

        api.unsubscribeFails = true
        graph.record("ada", ChannelSubscription(subscribed = true, notifyOn = NotifyOn.NONE))
        graph.unsubscribe("ada")
        assertThat(graph.edges.value["ada"]).isEqualTo(ChannelSubscription(subscribed = true, notifyOn = NotifyOn.NONE))
    }

    // ── The bell ─────────────────────────────────────────────────────

    @Test
    fun `the bell patches notify_on and keeps the server's answer`() = runTest {
        val api = RecordingChannelApi(subscribed = mutableMapOf("ada" to "all"))
        val graph = subscriptionGraph(api)
        graph.ensureKnown(listOf("ada"))

        graph.setNotifyOn("ada", NotifyOn.NONE)

        assertThat(api.notifyRequests).containsExactly("ada" to "none")
        assertThat(graph.edges.value["ada"]).isEqualTo(ChannelSubscription(subscribed = true, notifyOn = NotifyOn.NONE))
    }

    @Test
    fun `a refused bell change restores the previous setting`() = runTest {
        val api = RecordingChannelApi(subscribed = mutableMapOf("ada" to "all"), notifyFails = true)
        val graph = subscriptionGraph(api)
        graph.ensureKnown(listOf("ada"))

        val result = graph.setNotifyOn("ada", NotifyOn.NONE)

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat(graph.edges.value["ada"]).isEqualTo(ChannelSubscription(subscribed = true, notifyOn = NotifyOn.ALL))
    }

    // ── The wire value ───────────────────────────────────────────────

    @Test
    fun `only an explicit none turns the bell off`() {
        assertThat(NotifyOn.fromWire("none")).isEqualTo(NotifyOn.NONE)
        assertThat(NotifyOn.fromWire("all")).isEqualTo(NotifyOn.ALL)
        assertThat(NotifyOn.fromWire(null)).isEqualTo(NotifyOn.ALL)
        assertThat(NotifyOn.fromWire("personalised")).isEqualTo(NotifyOn.ALL)
    }
}

/**
 * The channel API as the graph sees it: what each channel's subscription
 * read answers, and a record of every write. Only the subscription routes
 * are implemented; the channel routes fail loudly, because a graph test
 * that reaches them is testing the wrong thing.
 */
internal class RecordingChannelApi(
    /** Channel id → `notify_on` for a subscribed channel. Absent channels answer "not subscribed". */
    val subscribed: MutableMap<String, String> = mutableMapOf(),
    var subscribeFails: Boolean = false,
    var unsubscribeFails: Boolean = false,
    var notifyFails: Boolean = false,
) : ChannelApi {
    val subscriptionReads = mutableListOf<String>()
    val subscribeRequests = mutableListOf<String>()
    val unsubscribeRequests = mutableListOf<String>()
    val notifyRequests = mutableListOf<Pair<String, String>>()

    override suspend fun subscription(ref: String): ApiEnvelope<SubscriptionDto> {
        subscriptionReads += ref
        val bell = subscribed[ref]
        return ApiEnvelope(data = SubscriptionDto(subscribed = bell != null, notifyOn = bell), meta = null)
    }

    override suspend fun subscribe(ref: String, body: SubscribeRequest): ApiEnvelope<SubscribeResultDto> {
        subscribeRequests += ref
        if (subscribeFails) throw IOException("offline")
        val bell = body.notifyOn ?: "all"
        subscribed[ref] = bell
        return ApiEnvelope(
            data = SubscribeResultDto(status = "subscribed", notifyOn = bell, follow = "followed", subscriberCount = 1),
            meta = null,
        )
    }

    override suspend fun unsubscribe(ref: String): ApiEnvelope<SubscribeResultDto> {
        unsubscribeRequests += ref
        if (unsubscribeFails) throw IOException("offline")
        subscribed -= ref
        return ApiEnvelope(data = SubscribeResultDto(status = "unsubscribed", subscriberCount = 0), meta = null)
    }

    override suspend fun updateSubscription(ref: String, body: NotifyOnRequest): ApiEnvelope<SubscriptionDto> {
        notifyRequests += ref to body.notifyOn
        if (notifyFails) throw IOException("offline")
        subscribed[ref] = body.notifyOn
        return ApiEnvelope(data = SubscriptionDto(subscribed = true, notifyOn = body.notifyOn), meta = null)
    }

    override suspend fun subscriptions(limit: Int, cursor: String?): ApiEnvelope<List<SubscriptionRowDto>> =
        error("unused")

    override suspend fun create(body: CreateChannelRequest): ApiEnvelope<ChannelDto> = error("unused")
    override suspend fun me(): ApiEnvelope<ChannelDto> = error("unused")
    override suspend fun update(body: UpdateChannelRequest): ApiEnvelope<ChannelDto> = error("unused")
    override suspend fun get(key: String): ApiEnvelope<ChannelDto> = error("unused")
    override suspend fun handleAvailable(handle: String): ApiEnvelope<HandleAvailabilityDto> = error("unused")
}

/** A [SubscriptionGraph] over the recording api, signed in as "me" unless told otherwise. */
internal fun subscriptionGraph(
    api: RecordingChannelApi = RecordingChannelApi(),
    session: FakeSession = FakeSession(),
): SubscriptionGraph = SubscriptionGraph(
    api = api,
    errorMapper = ErrorMapper(Json { ignoreUnknownKeys = true }),
    session = session,
)
