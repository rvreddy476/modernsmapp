package com.us.android.core.model

/**
 * How much a subscriber wants to hear from a channel (Tube subscriptions,
 * 2026-09-12). Every subscriber is notified of new uploads by default; the
 * bell on the channel page turns that off per channel. Two values only,
 * matching the server's `notify_on`: there is no "personalised" middle
 * setting, because the server does not rank uploads per subscriber.
 */
enum class NotifyOn(val wire: String) {
    ALL("all"),
    NONE("none"),
    ;

    companion object {
        /**
         * Only an explicit `"none"` turns the bell off. An unknown or absent
         * value is treated as the default rather than as silence, so a server
         * that adds a third value later does not mute every subscriber on the
         * old client.
         */
        fun fromWire(raw: String?): NotifyOn = if (raw == NONE.wire) NONE else ALL
    }
}

/**
 * The viewer's subscription edge toward one channel. [subscribed] false
 * carries a [notifyOn] that means nothing; it is kept at the default so a
 * fresh subscribe starts with the bell on without a second field to reset.
 */
data class ChannelSubscription(
    val subscribed: Boolean,
    val notifyOn: NotifyOn = NotifyOn.ALL,
) {
    companion object {
        val NOT_SUBSCRIBED = ChannelSubscription(subscribed = false)
    }
}
