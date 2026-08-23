package com.us.android.push

import com.us.android.core.auth.SessionManager
import com.us.android.core.notifications.data.PushTokenRegistrar
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.launch
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Registers this device's push token once a session exists — Slice D.
 *
 * ## THE GAP THIS CLOSES
 *
 * `PushTokenRegistrar` and `UsMessagingService` were both written, both
 * correct, and neither was ever reached. Nothing called the registrar, and the
 * messaging service was missing from the manifest — so no FCM token was ever
 * received, none was ever posted, and the server had no device to push to. The
 * whole notification path stopped at the database. This is the missing call.
 *
 * ## WHY IT LIVES IN :app
 *
 * It joins two modules that must not know about each other: `:core:auth` owns
 * the session, `:core:notifications` owns the token. `:app` is the composition
 * root and the only place allowed to know both.
 *
 * ## WHY IT WATCHES THE SESSION RATHER THAN REGISTERING AT STARTUP
 *
 * The FCM token usually arrives before sign-in — the callback fires on first
 * launch. Posting it then either 401s or attaches the device to nobody. So the
 * token is stored by the messaging service and posted here, on the transition
 * INTO an authenticated session, which also covers a token that rotated while
 * signed out.
 *
 * Sign-OUT is not handled here: unregistering needs the access token that
 * sign-out is about to destroy, so it runs inside the sign-out itself via
 * `SessionTeardownTask`. See `PushTeardown`.
 */
@Singleton
class PushRegistrationCoordinator @Inject constructor(
    private val sessionManager: SessionManager,
    private val registrar: PushTokenRegistrar,
) {

    // Application-scoped and deliberately never cancelled: registration must
    // survive every screen. SupervisorJob so one failed attempt does not stop
    // the collector from seeing the next sign-in.
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    fun start() {
        scope.launch {
            sessionManager.state
                .map { it.isAuthenticated }
                .distinctUntilChanged()
                .collect { authenticated ->
                    // registerIfNeeded is a no-op when there is no stored token
                    // or when the server already has this one, so re-entering
                    // an authenticated state costs nothing.
                    if (authenticated) registrar.registerIfNeeded()
                }
        }
    }
}
