package com.us.android.core.notifications.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.POST
import retrofit2.http.Path

/**
 * Push-device registration.
 *
 * The canonical path is `/v1/notifications/devices`, NOT `/v1/devices`. The
 * root path appears in older module docs and is not registered — a live probe
 * returned a plain-text router 404. The ruling in the contract capture is to
 * keep the notifications prefix and fix the stale prose, because `/v1/devices`
 * collides conceptually with auth-service's TRUSTED devices, which are 2FA and
 * a completely different thing.
 *
 * Verified live on 2026-08-17: create returns 201 with the persisted device;
 * delete returns 200 `{"status":"removed"}` and is idempotent — a repeated
 * delete returns the same body rather than a 404, which is deliberate so a
 * caller cannot enumerate device ids.
 */
interface DeviceApi {

    @POST("v1/notifications/devices")
    suspend fun registerDevice(@Body body: RegisterDeviceRequest): ApiEnvelope<DeviceDto>

    @DELETE("v1/notifications/devices/{deviceId}")
    suspend fun unregisterDevice(@Path("deviceId") deviceId: String): ApiEnvelope<RemovedDto>
}

/**
 * Both fields are required; `{}` returns 400 naming each missing tag.
 *
 * Neither carries a Kotlin default, deliberately: the app's `Json` leaves
 * `encodeDefaults` false, so a property equal to its declared default is
 * OMITTED from the body. A defaulted required field would serialize to `{}`
 * and be rejected — the same trap that bit the repost request.
 */
@Serializable
data class RegisterDeviceRequest(
    val platform: String,
    @SerialName("push_token") val pushToken: String,
)

@Serializable
data class DeviceDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
    val platform: String = "",
    @SerialName("push_token") val pushToken: String = "",
    @SerialName("is_active") val isActive: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

@Serializable
data class RemovedDto(val status: String = "")
