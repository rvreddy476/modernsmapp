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
 * Registers this device's push token as `doorstep_pro` once a session exists,
 * and again whenever FCM rotates it — Momentum's PushRegistrationCoordinator,
 * copied (it lives in :app, which a partner app may not depend on).
 *
 * Without a google-services.json no token is ever stored, so every call here
 * is a no-op; with one, notification-service routes doorstep.pro.* pushes to
 * this device and nothing of Momentum's. Sign-out unregisters through the
 * shared PushTeardown session task.
 */
@Singleton
class ProPushRegistration @Inject constructor(
    private val sessionManager: SessionManager,
    private val registrar: PushTokenRegistrar,
    private val tokenStore: PushTokenStore,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    fun start() {
        scope.launch {
            sessionManager.state
                .map { it.isAuthenticated }
                .distinctUntilChanged()
                .collect { authenticated -> if (authenticated) registrar.registerIfNeeded() }
        }
        scope.launch {
            tokenStore.tokenUpdates.collect {
                if (sessionManager.state.value.isAuthenticated) registrar.registerIfNeeded()
            }
        }
    }
}
