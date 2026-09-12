package com.us.android.core.feed.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Channels (Tube, 2026-09-05): the identity a long video is posted under.
 * One per user. The contract as the server agent is building it:
 *
 *  - `POST v1/channels {name, handle, about?}` → 201 the channel;
 *    409 `CHANNEL_EXISTS` / `HANDLE_TAKEN`; 400 `INVALID_NAME` /
 *    `INVALID_HANDLE` / `INVALID_ABOUT`.
 *  - `GET v1/channels/me` → the channel, or 404 `NO_CHANNEL`.
 *  - `PATCH v1/channels/me` → the channel, updated.
 *  - `GET v1/channels/{handle_or_user_id}` → a channel with `video_count`
 *    and `subscriber_count`, plus `is_subscribed` / `notify_on` when the
 *    caller is signed in.
 *  - `GET v1/channels/handle-available?handle=` → `{available, suggestion}`.
 *
 * Subscriptions (2026-09-12). Subscribe is follow plus notify in one call:
 * the SERVER makes the follow edge, so the client never sends a follow of
 * its own alongside it, and an unsubscribe removes both.
 *
 *  - `GET v1/channels/{ref}/subscription` → `{subscribed:false}` or
 *    `{subscribed:true, notify_on, subscribed_at}`.
 *  - `POST v1/channels/{ref}/subscribe {notify_on?}` →
 *    `{status:"subscribed", notify_on, follow, subscriber_count}`.
 *  - `DELETE v1/channels/{ref}/subscribe` → `{status:"unsubscribed", subscriber_count}`.
 *  - `PATCH v1/channels/{ref}/subscription {notify_on}` → `{subscribed:true, notify_on}`;
 *    400 `INVALID_NOTIFY_ON`, 404 `NOT_SUBSCRIBED`.
 *  - `GET v1/channels/subscriptions?limit&cursor` → the viewer's subscriptions,
 *    newest first, each with its channel summary.
 *
 * `POST v1/posts` with a long video and no channel answers 403
 * `CHANNEL_REQUIRED`; that one is the publish pipeline's to read.
 */
interface ChannelApi {

    @POST("v1/channels")
    suspend fun create(@Body body: CreateChannelRequest): ApiEnvelope<ChannelDto>

    @GET("v1/channels/me")
    suspend fun me(): ApiEnvelope<ChannelDto>

    @PATCH("v1/channels/me")
    suspend fun update(@Body body: UpdateChannelRequest): ApiEnvelope<ChannelDto>

    /** [key] is a handle (without `@`) or a user id; the server accepts either. */
    @GET("v1/channels/{key}")
    suspend fun get(@Path("key") key: String): ApiEnvelope<ChannelDto>

    @GET("v1/channels/handle-available")
    suspend fun handleAvailable(@Query("handle") handle: String): ApiEnvelope<HandleAvailabilityDto>

    /**
     * The viewer's own subscriptions, newest first. Declared ABOVE the
     * `{ref}` routes for the reader's sake: on the server `subscriptions`
     * is a reserved segment, not a handle, and this is the one that wins.
     */
    @GET("v1/channels/subscriptions")
    suspend fun subscriptions(
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<SubscriptionRowDto>>

    /** The viewer's edge toward one channel; `{subscribed:false}` when there is none. */
    @GET("v1/channels/{ref}/subscription")
    suspend fun subscription(@Path("ref") ref: String): ApiEnvelope<SubscriptionDto>

    /** Subscribe (the server follows too). An absent `notify_on` means the server default, "all". */
    @POST("v1/channels/{ref}/subscribe")
    suspend fun subscribe(@Path("ref") ref: String, @Body body: SubscribeRequest): ApiEnvelope<SubscribeResultDto>

    /** Unsubscribe, which also unfollows. */
    @DELETE("v1/channels/{ref}/subscribe")
    suspend fun unsubscribe(@Path("ref") ref: String): ApiEnvelope<SubscribeResultDto>

    /** The bell: `notify_on` for an existing subscription. 404 `NOT_SUBSCRIBED` when there is none. */
    @PATCH("v1/channels/{ref}/subscription")
    suspend fun updateSubscription(
        @Path("ref") ref: String,
        @Body body: NotifyOnRequest,
    ): ApiEnvelope<SubscriptionDto>
}

@Serializable
data class ChannelDto(
    @SerialName("user_id") val userId: String = "",
    val name: String = "",
    val handle: String = "",
    val about: String = "",
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
    @SerialName("avatar_url") val avatarUrl: String? = null,
    @SerialName("video_count") val videoCount: Int = 0,
    @SerialName("subscriber_count") val subscriberCount: Int = 0,
    /** Present only for a signed-in caller; null from a public read. */
    @SerialName("is_subscribed") val isSubscribed: Boolean? = null,
    /** `all` or `none`; present only when [isSubscribed] is true. */
    @SerialName("notify_on") val notifyOn: String? = null,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

@Serializable
data class HandleAvailabilityDto(
    val available: Boolean = false,
    /** The server's alternative when [available] is false; blank when it has none. */
    val suggestion: String = "",
)

/** `about` is omitted when blank — the server treats absent and empty alike, and absent is the smaller request. */
@Serializable
data class CreateChannelRequest(
    val name: String,
    val handle: String,
    val about: String? = null,
)

/** Every field optional: only what changed goes on the wire. */
@Serializable
data class UpdateChannelRequest(
    val name: String? = null,
    val handle: String? = null,
    val about: String? = null,
)

/** `GET v1/channels/{ref}/subscription` and the PATCH answer: the edge, with its bell when it exists. */
@Serializable
data class SubscriptionDto(
    val subscribed: Boolean = false,
    @SerialName("notify_on") val notifyOn: String? = null,
    @SerialName("subscribed_at") val subscribedAt: String? = null,
)

/** The subscribe body. Absent `notify_on` (the app's JSON drops nulls) lets the server pick its default. */
@Serializable
data class SubscribeRequest(
    @SerialName("notify_on") val notifyOn: String? = null,
)

/** The bell body. Always present: a PATCH with nothing to change is a bug, not a request. */
@Serializable
data class NotifyOnRequest(
    @SerialName("notify_on") val notifyOn: String,
)

/**
 * What subscribe and unsubscribe answer. `follow` is the server's word on
 * the follow edge it made or removed alongside; the client records the
 * subscription and leaves the follow graph to learn it on its next read.
 */
@Serializable
data class SubscribeResultDto(
    val status: String = "",
    @SerialName("notify_on") val notifyOn: String? = null,
    val follow: String? = null,
    @SerialName("subscriber_count") val subscriberCount: Int = 0,
)

/** One row of the viewer's subscriptions: the channel summary, the bell, when it was made. */
@Serializable
data class SubscriptionRowDto(
    val channel: SubscribedChannelDto = SubscribedChannelDto(),
    @SerialName("notify_on") val notifyOn: String? = null,
    @SerialName("subscribed_at") val subscribedAt: String = "",
)

/** The channel as the subscriptions list summarises it: identity and count, no About. */
@Serializable
data class SubscribedChannelDto(
    @SerialName("user_id") val userId: String = "",
    val name: String = "",
    val handle: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
    @SerialName("subscriber_count") val subscriberCount: Int = 0,
)
