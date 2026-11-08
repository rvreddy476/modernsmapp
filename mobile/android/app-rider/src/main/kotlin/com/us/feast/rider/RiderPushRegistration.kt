package com.us.feast.rider

import com.us.android.core.auth.SessionManager
import com.us.android.core.notifications.data.PushTokenRegistrar
import com.us.android.core.notifications.data.PushTokenStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.util.concurrent.atomic.AtomicBoolean
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Registers this install's FCM token as `feast_rider` (PushApp.FEAST_RIDER)
 * once signed in, and again on every rotation, so notification-service's
 * `food_delivery_offer` reaches this phone (food audit H1). The same shape as
 * Feast Kitchen's registration. Without a google-services.json no token ever
 * arrives and this stays idle.
 */
@Singleton
class RiderPushRegistration @Inject constructor(
    private val session: SessionManager,
    private val registrar: PushTokenRegistrar,
    private val tokens: PushTokenStore,
) {
    private val started = AtomicBoolean(false)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val mutex = Mutex()

    fun start() {
        if (!started.compareAndSet(false, true)) return
        scope.launch {
            session.state.map { it.isAuthenticated }.distinctUntilChanged().collect { signedIn ->
                if (signedIn) register()
            }
        }
        scope.launch { tokens.tokenUpdates.collect { register() } }
        // A failed registration is retried while the process lives; a registered token is a local no-op.
        scope.launch {
            while (true) {
                delay(RETRY_MILLIS)
                register()
            }
        }
    }

    private suspend fun register() = mutex.withLock {
        if (session.state.value.isAuthenticated) registrar.registerIfNeeded()
    }

    private companion object {
        const val RETRY_MILLIS = 60_000L
    }
}
