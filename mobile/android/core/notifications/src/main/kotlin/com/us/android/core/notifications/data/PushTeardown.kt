package com.us.android.core.notifications.data

import com.us.android.core.common.session.SessionTeardownTask
import javax.inject.Inject

/**
 * Unregisters this device's push token during sign-out — Slice D.
 *
 * ## WHY THIS MATTERS ON A SHARED HANDSET
 *
 * The FCM token identifies the DEVICE, not the account. Without this, signing
 * out leaves the server happily pushing the previous account's notifications to
 * a phone somebody else is now using — the token stays valid and nothing tells
 * the server the session ended.
 *
 * It runs through [SessionTeardownTask] rather than being called from the
 * sign-out UI, because it needs the access token that sign-out is about to
 * destroy, and because a feature module should not have to remember to do it.
 */
class PushTeardown @Inject constructor(
    private val registrar: PushTokenRegistrar,
) : SessionTeardownTask {

    override suspend fun onSignOut() {
        registrar.unregister()
    }
}
