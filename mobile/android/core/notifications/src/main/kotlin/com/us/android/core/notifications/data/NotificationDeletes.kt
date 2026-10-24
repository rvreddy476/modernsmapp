package com.us.android.core.notifications.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.NotificationAddress
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import retrofit2.http.DELETE
import retrofit2.http.Path
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Deleting ONE notification (2026-10-24): `DELETE /v1/notifications/:bucket/:ts`
 * (notification-service internal/http/handler.go:55, DeleteNotification
 * :191-218) — the same `(bucket, ts)` address the read endpoint takes, never
 * the notification id. Answers `{"data":{"status":"deleted"}}`; 400 for a
 * malformed address. Its own interface so the [NotificationsApi] fakes keep
 * their shape.
 */
interface NotificationDeleteApi {
    @DELETE("v1/notifications/{bucket}/{ts}")
    suspend fun delete(@Path("bucket") bucket: Int, @Path("ts") ts: String): ApiEnvelope<StatusDto>
}

@Singleton
class NotificationDeletes @Inject constructor(
    private val api: NotificationDeleteApi,
    private val errorMapper: ErrorMapper,
) {
    suspend fun delete(address: NotificationAddress): AppResult<Unit> =
        apiCall(errorMapper) { api.delete(address.bucket, address.ts) }.map { }
}
