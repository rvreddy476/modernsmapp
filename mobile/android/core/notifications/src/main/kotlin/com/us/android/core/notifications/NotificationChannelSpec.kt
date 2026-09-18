package com.us.android.core.notifications

import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.Context
import android.media.AudioAttributes
import android.media.RingtoneManager
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
    /**
     * Plays the device's alarm tone instead of the default notification
     * sound. Only for a channel whose post must be heard across a loud
     * kitchen. The channel cannot LOOP a sound — that is the posted
     * notification's FLAG_INSISTENT and the in-app alert player.
     */
    val alertSound: Boolean = false,
    /**
     * Hides the notification's content on a secure lock screen (the channel's
     * lockscreen visibility is PRIVATE). For a channel whose mere content —
     * who sparked you — is private.
     */
    val privateOnLockScreen: Boolean = false,
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

    /**
     * Uploads from subscribed channels (Tube subscriptions, 2026-09-12).
     * Its own channel rather than SOCIAL: a subscriber who wants likes
     * quiet and uploads loud, or the reverse, can only say so if the two
     * are separate switches. DEFAULT, like SOCIAL: an upload is worth a
     * shade entry, not a buzz.
     */
    NEW_VIDEOS(
        id = "new_videos",
        title = "New videos",
        description = "Uploads from channels you subscribe to",
        importance = NotificationManager.IMPORTANCE_DEFAULT,
    ),

    /** Account and security. LOW: important to see, never urgent. */
    ACCOUNT(
        id = "account",
        title = "Account",
        description = "Security alerts and account updates",
        importance = NotificationManager.IMPORTANCE_LOW,
    ),

    /**
     * Feast Kitchen (A3, 2026-09-13) and Feast Rider (A4): changes to an order
     * the partner is already handling — a customer cancelling, a rider
     * arriving. HIGH: an order cancelled mid-cook or mid-ride is food and money
     * wasted. The description is app-neutral because both partner apps
     * register this channel.
     */
    FOOD_ORDERS(
        id = "food_orders",
        title = "Order updates",
        description = "Changes to orders you are handling",
        importance = NotificationManager.IMPORTANCE_HIGH,
    ),

    /**
     * Feast Kitchen: a new order waiting to be accepted before its deadline.
     * HIGH, with the alarm tone. Missing one is an auto-rejected order and a
     * refund, so it must be audible over a kitchen.
     */
    KITCHEN_NEW_ORDER(
        id = "kitchen_new_order",
        title = "New orders",
        description = "New orders waiting for your kitchen to accept",
        importance = NotificationManager.IMPORTANCE_HIGH,
        alertSound = true,
    ),

    /**
     * Feast Rider (A4, 2026-09-13): a delivery job offered to this rider, with
     * a short window to accept (notification-service `food_delivery_offer`).
     * HIGH with the alarm tone: an unheard offer goes to the next rider.
     */
    RIDER_JOB_OFFER(
        id = "rider_job_offer",
        title = "Job offers",
        description = "Delivery jobs offered to you, with time to accept",
        importance = NotificationManager.IMPORTANCE_HIGH,
        alertSound = true,
    ),

    /**
     * Feast Rider: the ongoing "you are on duty and sharing your location"
     * notification of the location foreground service. LOW: always visible
     * while online, never buzzing.
     */
    RIDER_ON_DUTY(
        id = "rider_on_duty",
        title = "On duty",
        description = "Shown while you are online and sharing your location",
        importance = NotificationManager.IMPORTANCE_LOW,
    ),

    /**
     * Dating (Wave 3, 2026-09-16): a new spark, a match, a message from a
     * match. Its own channel so someone can silence dating without silencing
     * chat, and the reverse. DEFAULT, not HIGH: a spark is not a call, and the
     * importance can never be raised later — the user can raise it themselves.
     * PRIVATE on the lock screen: who is sparking you is nobody else's business.
     */
    DATING(
        id = "dating",
        title = "Dating",
        description = "New sparks, matches and messages from your matches",
        importance = NotificationManager.IMPORTANCE_DEFAULT,
        privateOnLockScreen = true,
    ),

    /**
     * Mopedu (2026-09-18): the customer's ride — captain assigned, arriving,
     * arrived, started, completed, cancelled, payment received. HIGH: "your
     * captain has arrived" is time-critical, and the customer is often not
     * looking at the app while they wait.
     */
    RIDE_UPDATES(
        id = "ride_updates",
        title = "Ride updates",
        description = "Your captain's progress and your ride's payment",
        importance = NotificationManager.IMPORTANCE_HIGH,
    ),

    /**
     * Mopedu Captain: a ride offered to this captain, with a short window to
     * accept (notification-service `captain.offer`). HIGH with the alarm
     * tone: an unheard offer goes to the next captain.
     */
    CAPTAIN_OFFER(
        id = "captain_offer",
        title = "Ride offers",
        description = "Rides offered to you, with time to accept",
        importance = NotificationManager.IMPORTANCE_HIGH,
        alertSound = true,
    ),

    /**
     * Mopedu Captain: the ongoing "you are online and sharing your location"
     * notification of the location foreground service. LOW: always visible
     * while online, never buzzing.
     */
    CAPTAIN_ON_DUTY(
        id = "captain_on_duty",
        title = "On duty",
        description = "Shown while you are online and sharing your location",
        importance = NotificationManager.IMPORTANCE_LOW,
    ),

    /**
     * Mopedu Captain: money landed — a customer's online payment for a ride
     * was confirmed. DEFAULT: worth a sound, never urgent.
     */
    CAPTAIN_EARNINGS(
        id = "captain_earnings",
        title = "Earnings",
        description = "Customer payments received for your rides",
        importance = NotificationManager.IMPORTANCE_DEFAULT,
    ),
    ;

    companion object {
        /**
         * Momentum's channels — exactly the set the app registered before the
         * Feast partner apps existed (2026-09-13).
         *
         * Named rather than derived from [entries]: Feast Kitchen and Feast
         * Rider are separate installs with their own channel lists, and an
         * app must register only the channels it can actually post to. A
         * Kitchen install showing "Calls" and "New videos" switches in system
         * settings would be noise the user can neither use nor remove. Adding
         * a new enum entry therefore adds it to NO app until someone puts it
         * in that app's set on purpose. Pinned by MomentumChannelSetTest.
         */
        val MOMENTUM: Set<NotificationChannelSpec> = setOf(
            CALLS,
            MESSAGES,
            SOCIAL,
            NEW_VIDEOS,
            ACCOUNT,
            // Dating ships only in Momentum (Wave 3, 2026-09-16).
            DATING,
            // The Mopedu customer flow ships only in Momentum (2026-09-18).
            RIDE_UPDATES,
        )

        /**
         * Feast Kitchen's channels (A3, 2026-09-13) — and nothing of Momentum's.
         * A kitchen tablet has no calls, chat or videos to be switched off.
         */
        val KITCHEN: Set<NotificationChannelSpec> = setOf(
            FOOD_ORDERS,
            KITCHEN_NEW_ORDER,
        )

        /**
         * Feast Rider's channels (A4, 2026-09-13) — nothing of Momentum's or
         * the kitchen's new-order alarm.
         */
        val RIDER: Set<NotificationChannelSpec> = setOf(
            FOOD_ORDERS,
            RIDER_JOB_OFFER,
            RIDER_ON_DUTY,
        )

        /**
         * Mopedu Captain's channels (2026-09-18) — nothing of Momentum's, the
         * kitchen's or the Feast rider's. A captain has offers and the on-duty
         * notification; the customer's ride updates go to Momentum.
         */
        val CAPTAIN: Set<NotificationChannelSpec> = setOf(
            CAPTAIN_OFFER,
            CAPTAIN_ON_DUTY,
            CAPTAIN_EARNINGS,
        )

        /**
         * Creates the given app's channels. Safe to call repeatedly — the
         * platform ignores a channel that already exists, and deliberately
         * will not let a re-registration override a user's setting.
         *
         * No default for [specs]: each application passes its own set
         * explicitly, so a new app cannot inherit Momentum's by omission.
         */
        fun createAll(context: Context, specs: Set<NotificationChannelSpec>) {
            val manager = context.getSystemService<NotificationManager>() ?: return
            specs.forEach { spec ->
                manager.createNotificationChannel(
                    NotificationChannel(spec.id, spec.title, spec.importance).apply {
                        description = spec.description
                        if (spec.privateOnLockScreen) {
                            lockscreenVisibility = android.app.Notification.VISIBILITY_PRIVATE
                        }
                        if (spec.alertSound) {
                            // Alarm tone where the device has one. A null URI
                            // passed to setSound would SILENCE the channel, so
                            // with neither tone available the default stays.
                            val tone = RingtoneManager.getDefaultUri(RingtoneManager.TYPE_ALARM)
                                ?: RingtoneManager.getDefaultUri(RingtoneManager.TYPE_NOTIFICATION)
                            if (tone != null) {
                                setSound(
                                    tone,
                                    AudioAttributes.Builder()
                                        .setUsage(AudioAttributes.USAGE_NOTIFICATION_EVENT)
                                        .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                                        .build(),
                                )
                            }
                            enableVibration(true)
                        }
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
            // The two upload types notification-service emits for a
            // subscribed channel (2026-09-12). Before this they fell through
            // to SOCIAL, so muting likes muted uploads too.
            "creator_uploaded_video", "creator_uploaded_flick" -> NEW_VIDEOS
            "account", "security" -> ACCOUNT
            // What notification-service's dating and chat consumers emit
            // (dating_consumer.go, chat_consumer.go), Wave 3 2026-09-16.
            "dating.spark.created", "dating.match.formed", "dating.match.new_message", "dating.match.first_message" -> DATING
            // Mopedu (2026-09-18): the customer's ride progress and payment,
            // and the captain's offer.
            "ride.assigned", "ride.arriving", "ride.arrived", "ride.started", "ride.completed", "ride.cancelled",
            "ride.payment.paid",
            -> RIDE_UPDATES
            "captain.offer" -> CAPTAIN_OFFER
            "captain.payment.received" -> CAPTAIN_EARNINGS
            else -> SOCIAL
        }
    }
}
