package com.us.android.core.notifications.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Registers this device's push token with the server.
 *
 * Registration is deliberately NOT fire-and-forget from the FCM callback. A
 * token can arrive before the user has signed in — the callback fires on first
 * app launch — and posting it then would either 401 or, worse, attach the
 * device to nobody. So the token is held and the caller registers it once a
 * session exists.
 *
 * The token also rotates: FCM reissues on app reinstall, data clear, and
 * occasionally on its own. `onNewToken` is the only reliable signal, which is
 * why the service writes through here rather than the app reading a token once
 * at startup.
 */
@Singleton
class PushTokenRegistrar @Inject constructor(
    private val api: DeviceApi,
    private val store: PushTokenStore,
    private val errorMapper: ErrorMapper,
) {

    /**
     * Sends the stored token, if there is one and it has not already been
     * accepted.
     *
     * Returns the server's device id so the caller can unregister on sign-out.
     * A null result means there was nothing to send — not a failure.
     */
    suspend fun registerIfNeeded(): AppResult<String?> {
        val token = store.token() ?: return AppResult.Success(null)
        if (store.registeredToken() == token) return AppResult.Success(store.deviceId())

        return apiCall(errorMapper) {
            api.registerDevice(RegisterDeviceRequest(platform = ANDROID, pushToken = token))
        }.map { device ->
            store.markRegistered(token = token, deviceId = device.id)
            device.id
        }
    }

    /**
     * Removes this device on sign-out.
     *
     * Without it, a shared or resold handset keeps receiving the previous
     * account's notifications — the token stays valid, and the server has no
     * way to know the session ended.
     */
    suspend fun unregister(): AppResult<Unit> {
        val deviceId = store.deviceId() ?: return AppResult.Success(Unit)
        return apiCall(errorMapper) { api.unregisterDevice(deviceId) }
            .map { store.clearRegistration() }
    }

    private companion object {
        const val ANDROID = "android"
    }
}

/**
 * Where the token and its registration state live.
 *
 * Both halves are needed. The token alone cannot tell you whether the server
 * has it — re-posting an already-registered token on every cold start is a
 * write per launch for nothing.
 */
interface PushTokenStore {
    fun token(): String?
    fun setToken(token: String)
    fun registeredToken(): String?
    fun deviceId(): String?
    fun markRegistered(token: String, deviceId: String)
    fun clearRegistration()

    /**
     * Emits every stored token, including rotations that happen while the
     * app is running. Session-transition registration alone misses those: a
     * token FCM reissues mid-session was stored and never posted, and the
     * device silently stopped receiving push until the next sign-in.
     */
    val tokenUpdates: kotlinx.coroutines.flow.SharedFlow<String>
}
