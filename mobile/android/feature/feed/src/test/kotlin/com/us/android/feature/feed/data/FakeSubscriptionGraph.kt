package com.us.android.feature.feed.data

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.feed.data.ChannelApi
import com.us.android.core.feed.data.ChannelDto
import com.us.android.core.feed.data.CreateChannelRequest
import com.us.android.core.feed.data.HandleAvailabilityDto
import com.us.android.core.feed.data.NotifyOnRequest
import com.us.android.core.feed.data.SubscribeRequest
import com.us.android.core.feed.data.SubscribeResultDto
import com.us.android.core.feed.data.SubscriptionDto
import com.us.android.core.feed.data.SubscriptionGraph
import com.us.android.core.feed.data.SubscriptionRowDto
import com.us.android.core.feed.data.UpdateChannelRequest
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.serialization.json.Json
import java.io.IOException

/**
 * The channel routes as a feed test sees them: what
 * `GET v1/channels/{ref}/subscription` answers per channel, and a record
 * of every subscribe the feed sent. Only the subscription routes are
 * implemented; the channel routes fail loudly, because a reels test that
 * reaches them is testing the wrong thing.
 */
internal class RecordingChannelApi(
    /** Channel id → `notify_on` for a subscribed channel. Absent channels answer "not subscribed". */
    val subscribed: MutableMap<String, String> = mutableMapOf(),
    var subscribeFails: Boolean = false,
) : ChannelApi {
    val subscriptionReads = mutableListOf<String>()
    val subscribeRequests = mutableListOf<String>()

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

    override suspend fun unsubscribe(ref: String): ApiEnvelope<SubscribeResultDto> = error("unused")
    override suspend fun updateSubscription(ref: String, body: NotifyOnRequest): ApiEnvelope<SubscriptionDto> =
        error("unused")

    override suspend fun create(body: CreateChannelRequest): ApiEnvelope<ChannelDto> = error("unused")
    override suspend fun me(): ApiEnvelope<ChannelDto> = error("unused")
    override suspend fun update(body: UpdateChannelRequest): ApiEnvelope<ChannelDto> = error("unused")
    override suspend fun get(key: String): ApiEnvelope<ChannelDto> = error("unused")
    override suspend fun handleAvailable(handle: String): ApiEnvelope<HandleAvailabilityDto> = error("unused")
    override suspend fun subscriptions(limit: Int, cursor: String?): ApiEnvelope<List<SubscriptionRowDto>> =
        error("unused")
}

/** A [SubscriptionGraph] over the recording api, for the ViewModel tests that need one. */
internal fun subscriptionGraph(
    api: RecordingChannelApi = RecordingChannelApi(),
    session: SessionStateProvider = FakeSession(),
): SubscriptionGraph = SubscriptionGraph(
    api = api,
    errorMapper = ErrorMapper(Json { ignoreUnknownKeys = true }),
    session = session,
)
