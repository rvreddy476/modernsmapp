package com.us.android.core.notifications.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationAddress
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.CancellationException
import javax.inject.Inject
import javax.inject.Singleton

/** One page of the inbox, plus the cursor that continues it. */
data class NotificationPage(
    val items: List<Notification>,
    val nextCursor: String?,
)

/**
 * The notification inbox, as data — Slice D.
 *
 * ## THE SERVER OWNS READ STATE
 *
 * Every mutation here is a request, not a local edit. The unread count is
 * computed server-side across the whole inbox, not derived from the loaded
 * page: a user with 200 unread notifications and a 20-row first page would
 * otherwise see "20".
 *
 * The optimistic half of read-state lives in the ViewModel, where it can be
 * reconciled against a refresh. Putting it here would make the repository a
 * second source of truth for something the server already decides.
 */
@Singleton
open class NotificationsRepository @Inject constructor(
    private val api: NotificationsApi,
    private val errorMapper: ErrorMapper,
) {

    /**
     * One page of the inbox.
     *
     * ## WHY THIS DOES NOT USE `apiCall`
     *
     * Two reasons, both load-bearing:
     *
     *  1. `apiCall` discards `meta`, and `meta.next_cursor` IS the paging
     *     contract. There is no other signal for "more pages exist".
     *  2. `apiCall` treats `data == null` as a malformed response. An EMPTY
     *     INBOX returns exactly `{"data":null}` — verified live — because the
     *     platform envelope is `omitempty` server-side. Routed through
     *     `apiCall`, every user with no notifications would see an error
     *     screen instead of an empty inbox.
     *
     * So the envelope is interpreted here, with null data meaning "no rows"
     * for this endpoint specifically. Error and exception handling are
     * otherwise identical to `apiCall`, including rethrowing
     * [CancellationException] rather than swallowing it.
     */
    @Suppress("TooGenericExceptionCaught")
    open suspend fun page(
        limit: Int = PAGE_SIZE,
        cursor: String? = null,
    ): AppResult<NotificationPage> = try {
        val envelope = api.list(limit = limit, cursor = cursor)
        val error = envelope.error
        if (error != null) {
            AppResult.Failure(
                AppError.Unknown(
                    code = error.code,
                    statusCode = null,
                    requestId = envelope.meta?.requestId,
                ),
            )
        } else {
            AppResult.Success(
                NotificationPage(
                    items = envelope.data.orEmpty().map { it.toDomain() },
                    // Absent means "no more pages"; never parsed, never built.
                    nextCursor = envelope.meta?.nextCursor?.takeIf { it.isNotBlank() },
                ),
            )
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: Throwable) {
        AppResult.Failure(errorMapper.map(e))
    }

    /**
     * The whole-inbox unread count.
     *
     * Server-computed, never derived from the loaded page: the badge must
     * count notifications the client has not fetched.
     */
    open suspend fun unreadCount(): AppResult<Int> =
        apiCall(errorMapper) { api.unreadCount() }.map { it.count }

    open suspend fun markRead(address: NotificationAddress): AppResult<Unit> =
        apiCall(errorMapper) {
            api.markRead(MarkReadRequest(bucket = address.bucket, ts = address.ts))
        }.map { }

    open suspend fun markAllRead(): AppResult<Unit> =
        apiCall(errorMapper) { api.markAllRead() }.map { }

    companion object {
        const val PAGE_SIZE = 20
    }
}

/**
 * Wire to domain.
 *
 * The two derivations — [NotificationKind.fromWire] and
 * [NotificationTarget.parse] — happen ONCE, here. A screen that re-parsed the
 * raw strings would eventually disagree with this one about what a row means.
 */
internal fun NotificationDto.toDomain(): Notification = Notification(
    id = notificationId,
    bucket = bucket,
    ts = ts,
    kind = NotificationKind.fromWire(type),
    actorUserId = actorUserId,
    entityType = entityType,
    entityId = entityId,
    target = NotificationTarget.parse(deepLink),
    isRead = isRead,
    createdAt = createdAt,
)
