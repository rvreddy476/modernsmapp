package com.us.doorstep.pro.push

import com.us.android.core.auth.SessionManager
import com.us.android.core.notifications.data.PushTokenRegistrar
import com.us.android.core.notifications.data.PushTokenStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Registers this device's push token as `partner` once a session exists,
 * and again whenever FCM rotates it — Momentum's PushRegistrationCoordinator,
 * copied (it lives in :app, which a partner app may not depend on).
 *
 * Without a google-services.json no token is ever stored, so every call here
 * is a no-op; with one, notification-service routes business pushes to this
 * device, excluding customer/social events. Sign-out unregisters through the
 * shared PushTeardown session task.
 */
@Singleton
class ProPushRegistration @Inject constructor(
    private val sessionManager: SessionManager,
    private val registrar: PushTokenRegistrar,
    private val tokenStore: PushTokenStore,
    private val proDuty: com.us.android.feature.doorsteppro.location.ProDuty,
    private val riderDuty: com.us.android.feature.rider.location.RiderDuty,
    private val captainDuty: com.us.android.feature.mopedu.captain.location.CaptainDuty,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    fun start() {
        scope.launch {
            sessionManager.state
                .map { it.isAuthenticated }
                .distinctUntilChanged()
                .collect { authenticated ->
                    if (authenticated) registrar.registerIfNeeded()
                    else {
                        proDuty.requestStop(com.us.android.feature.doorsteppro.location.OfflineReason.SIGNED_OUT)
                        riderDuty.requestStop(com.us.android.feature.rider.location.OfflineReason.SIGNED_OUT)
                        captainDuty.requestStop(com.us.android.feature.mopedu.captain.location.OfflineReason.SIGNED_OUT)
                    }
                }
        }
        scope.launch {
            tokenStore.tokenUpdates.collect {
                if (sessionManager.state.value.isAuthenticated) registrar.registerIfNeeded()
            }
        }
    }
}
