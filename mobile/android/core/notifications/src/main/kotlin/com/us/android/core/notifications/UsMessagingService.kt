package com.us.android.core.notifications

import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import com.us.android.core.notifications.data.PushTokenStore
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject

/**
 * Receives the FCM token and incoming pushes.
 *
 * Deliberately thin. It stores the token and hands messages to
 * [NotificationPresenter]; it does not call the network, because this service
 * runs whenever FCM decides to wake the app — including before sign-in and
 * while the app is dead — and a network call from here has no session to use
 * and no scope to survive in.
 */
@AndroidEntryPoint
class UsMessagingService : FirebaseMessagingService() {

    @Inject
    lateinit var tokenStore: PushTokenStore

    @Inject
    lateinit var presenter: NotificationPresenter

    /**
     * Fires on first launch, reinstall, data clear, and periodic rotation.
     *
     * This is the ONLY reliable source of the current token. Reading it once at
     * startup misses every rotation, and the device then silently stops
     * receiving push while appearing registered.
     *
     * The token is stored, not posted: there may be no session yet. The app
     * registers it when one exists.
     */
    override fun onNewToken(token: String) {
        tokenStore.setToken(token)
    }

    /**
     * A push arrived.
     *
     * Data-only messages reach this in every app state, which is what call
     * invites require. A message carrying a `notification` block is rendered by
     * the system when the app is backgrounded and never reaches here — so
     * anything that must act on delivery has to be sent data-only, and that is
     * a server-side contract, not a client setting.
     */
    override fun onMessageReceived(message: RemoteMessage) {
        presenter.present(message.data)
    }
}
