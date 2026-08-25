package com.us.android.core.notifications.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Query

/**
 * The notification inbox — Slice D.
 *
 * ## THE CONTRACT, CAPTURED LIVE
 *
 * Verified on 2026-08-22 through the real gateway against a real notification
 * produced by a real comment event:
 *
 * ```
 * GET  /v1/notifications?limit=20&cursor=…   → {"data":[Notification], "meta":{"next_cursor":…}}
 * GET  /v1/notifications/unread-count        → {"data":{"count":1}}
 * POST /v1/notifications/read {bucket,ts}    → {"data":{"status":"ok"}}
 * PATCH /v1/notifications/read-all           → {"data":{"status":"ok"}}
 * ```
 *
 * `data` is null — not `[]` — when the inbox is empty. That is the platform's
 * `omitempty` convention and the reason [NotificationDto] lists are read
 * through a nullable field with an empty fallback.
 */
interface NotificationsApi {

    @GET("v1/notifications")
    suspend fun list(
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<NotificationDto>>

    @GET("v1/notifications/unread-count")
    suspend fun unreadCount(): ApiEnvelope<UnreadCountDto>

    @POST("v1/notifications/read")
    suspend fun markRead(@Body body: MarkReadRequest): ApiEnvelope<StatusDto>

    @PATCH("v1/notifications/read-all")
    suspend fun markAllRead(): ApiEnvelope<StatusDto>
}

/**
 * One inbox row, exactly as `scylla.Notification` serialises it.
 *
 * ## WHY A NOTIFICATION IS ADDRESSED BY (bucket, ts) AND NOT BY ID
 *
 * It carries a `notification_id`, but that is NOT its address. The Scylla
 * clustering key is `(user_id, bucket, ts)`, and `POST /read` takes exactly
 * that pair. Marking one read by `notification_id` is not possible.
 *
 * This matters because it is easy to look at the payload, see an id, and build
 * a client keyed on it — which then cannot mark anything read. The id is useful
 * only as a stable identity for list diffing.
 *
 * Every field is defaulted so a new server-side member cannot break decoding,
 * and so a partially-populated row still renders rather than failing the page.
 */
@Serializable
data class NotificationDto(
    @SerialName("notification_id") val notificationId: String = "",
    val bucket: Int = 0,
    val ts: String = "",
    val type: String = "",
    @SerialName("actor_user_id") val actorUserId: String = "",
    @SerialName("entity_type") val entityType: String = "",
    @SerialName("entity_id") val entityId: String = "",
    /** Server-authored target, e.g. `/post/{id}?focusComment={id}` or `/u/{id}`. */
    @SerialName("deep_link") val deepLink: String = "",
    @SerialName("is_read") val isRead: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class UnreadCountDto(val count: Int = 0)

@Serializable
data class StatusDto(val status: String = "")

/**
 * The read request. Both members are required by the server.
 *
 * NEITHER CARRIES A KOTLIN DEFAULT, deliberately. The app's `Json` leaves
 * `encodeDefaults` false, so a property equal to its declared default is
 * omitted from the body — and `bucket` is an Int whose natural default is 0,
 * which is a plausible-looking value that would simply vanish from the wire.
 * The server would then 400 on a missing required tag.
 *
 * This exact defect has shipped three times on this codebase. See
 * `NotificationWireTest`, which fails if either field stops being serialised.
 */
@Serializable
data class MarkReadRequest(
    val bucket: Int,
    val ts: String,
)
