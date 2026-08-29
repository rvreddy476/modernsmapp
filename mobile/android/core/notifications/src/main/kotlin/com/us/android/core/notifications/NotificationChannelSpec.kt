package com.us.android.core.notifications

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import androidx.core.content.getSystemService

/**
 * The notification categories the product sends.
 *
 * Separate channels are not cosmetic. From Android 8 a channel is the only
 * unit a user can silence, so collapsing everything into one means someone who
 * mutes marketing also mutes an incoming call. Once created, a channel's
 * importance CANNOT be raised by the app — the user owns it from then on — so
 * getting these right before the first release matters more than most choices.
 *
 * Ids are stable strings. Renaming one orphans the user's existing preference
 * and silently resets it to the default.
 */
enum class NotificationChannelSpec(
    val id: String,
    val title: String,
    val description: String,
    val importance: Int,
) {
    /**
     * Ringing calls. HIGH so it can interrupt, and the only channel entitled
     * to a full-screen intent.
     */
    CALLS(
        id = "calls",
        title = "Calls",
        description = "Incoming audio and video calls",
        importance = NotificationManager.IMPORTANCE_HIGH,
    ),

    MESSAGES(
        id = "messages",
        title = "Messages",
        description = "Direct messages and chat requests",
        importance = NotificationManager.IMPORTANCE_HIGH,
    ),

    /**
     * Likes, comments, follows. DEFAULT rather than HIGH: this is the highest
     * volume category, and a buzzing phone for every like is the fastest way
     * to get an app's notifications disabled wholesale.
     */
    SOCIAL(
        id = "social",
        title = "Social",
        description = "Reactions, comments, mentions and new followers",
        importance = NotificationManager.IMPORTANCE_DEFAULT,
    ),

    /** Account and security. LOW: important to see, never urgent. */
    ACCOUNT(
        id = "account",
        title = "Account",
        description = "Security alerts and account updates",
        importance = NotificationManager.IMPORTANCE_LOW,
    ),
    ;

    companion object {
        /**
         * Creates every channel. Safe to call repeatedly — the platform
         * ignores a channel that already exists, and deliberately will not let
         * a re-registration override a user's setting.
         */
        fun createAll(context: Context) {
            val manager = context.getSystemService<NotificationManager>() ?: return
            entries.forEach { spec ->
                manager.createNotificationChannel(
                    NotificationChannel(spec.id, spec.title, spec.importance).apply {
                        description = spec.description
                    },
                )
            }
        }

        /** The channel for a push, defaulting to SOCIAL for an unknown type. */
        fun forType(type: String?): NotificationChannelSpec = when (type) {
            // "incoming_call"/"incoming_video_call"/"missed_call" are what
            // notification-service's call consumer actually emits
            // (call_consumer.go); the bare "call"/"call_invite" aliases
            // predate it. Without the real types, ringing pushes landed on
            // the muteable SOCIAL channel with no full-screen entitlement.
            "call", "call_invite", "incoming_call", "incoming_video_call", "missed_call" -> CALLS
            // "dm" and "message_request" are what notification-service
            // actually sends for chat (notifTitleBody, notification.go);
            // without them chat pushes landed on the muteable SOCIAL channel.
            "message", "chat", "dm", "message_request" -> MESSAGES
            "account", "security" -> ACCOUNT
            else -> SOCIAL
        }
    }
}
