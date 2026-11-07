package com.us.android.core.notifications

import javax.inject.Inject
import javax.inject.Singleton

/**
 * The notification and the foreground request card can arrive in either order.
 * Both stay visible, but only the first one owns sound for this offer's lifetime.
 * Process-local, thread-safe and monotonic: no tokens or customer details stored.
 */
@Singleton
class DoorstepOfferAlertCoordinator @Inject constructor() {
    private val soundClaims = mutableMapOf<String, Long>()

    @Synchronized
    fun claimSound(
        offerId: String,
        remainingMillis: Long,
        nowMillis: Long = System.nanoTime() / 1_000_000L,
    ): Boolean {
        soundClaims.entries.removeAll { it.value <= nowMillis }
        if (offerId.isBlank() || remainingMillis <= 0 || soundClaims.containsKey(offerId)) return false
        soundClaims[offerId] = nowMillis + remainingMillis
        return true
    }
}

/**
 * A chat push is dropped only while the app is visible AND its session socket
 * is connected: then the socket has already delivered the message to the open
 * surface. With the socket down (cold start before the first token refresh,
 * weak network, rejected credential) the push is the only signal and shows
 * (audit H1). Service offers must alert in either app state.
 */
internal fun suppressForegroundNotification(type: String?, isForeground: Boolean, chatSocketLive: Boolean): Boolean =
    isForeground && chatSocketLive && type in setOf("dm", "message_request")
