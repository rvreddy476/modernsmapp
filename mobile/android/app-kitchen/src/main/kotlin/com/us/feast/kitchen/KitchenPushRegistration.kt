package com.us.feast.kitchen

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

/** Uses the shared authenticated device API, teardown and token store. */
@Singleton
class KitchenPushRegistration @Inject constructor(
    private val session: SessionManager,
    private val registrar: PushTokenRegistrar,
    private val tokens: PushTokenStore,
) {
    private val started = AtomicBoolean(false)
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val mutex = Mutex()
    private suspend fun register() = mutex.withLock {
        if (session.state.value.isAuthenticated) registrar.registerIfNeeded()
    }
    fun start() {
        if (!started.compareAndSet(false, true)) return
        scope.launch { session.state.map { it.isAuthenticated }.distinctUntilChanged().collect { if (it) register() } }
        scope.launch { tokens.tokenUpdates.collect { register() } }
        // Retry failed registrations while the process survives; successful
        // registrations are a local no-op until token rotation/sign-out.
        scope.launch { while (true) { delay(60_000); register() } }
    }
}
