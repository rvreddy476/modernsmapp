package com.us.android.push

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * One pending "the user tapped a notification" destination.
 *
 * Both tap paths land here with the same keys: a background tap arrives as
 * launch-intent extras (FCM attaches the data payload), a foreground tap via
 * [com.us.android.core.notifications.NotificationPresenter]'s content intent.
 * MainActivity offers; the nav host consumes ONCE the session allows it — a
 * tap while signed out survives the login and then routes, instead of being
 * dropped or, worse, routed before there is a session to authorize the load.
 */
@Singleton
class PushDestinations @Inject constructor() {

    private val _pending = MutableStateFlow<PushDestination?>(null)
    val pending: StateFlow<PushDestination?> = _pending.asStateFlow()

    fun offer(type: String?, entityId: String?, deepLink: String?) {
        if (type.isNullOrBlank()) return
        _pending.value = PushDestination(
            type = type,
            entityId = entityId.orEmpty(),
            deepLink = deepLink.orEmpty(),
        )
    }

    fun consume() {
        _pending.value = null
    }

    /**
     * An App Link the activity was opened with. Only the group invite link is
     * routed here today — `https://atpost.app/chat/join/{code}` — and it rides
     * the same pending slot as a notification tap so it waits through the
     * login like one. Any other URL is ignored.
     */
    fun offerLink(uri: android.net.Uri?) {
        val link = uri?.toString() ?: return
        val code = joinCodeOf(link) ?: return
        _pending.value = PushDestination(type = TYPE_CHAT_JOIN, entityId = code, deepLink = link)
    }

    companion object {
        const val TYPE_CHAT_JOIN = "chat_join"
        private const val LINK_HOST = "atpost.app"

        /**
         * The code in `https://atpost.app/chat/join/{code}`; null for any other
         * link. Plain string work rather than `Uri` so the rule is unit-testable
         * off-device (the app's tests stub Android to defaults).
         */
        fun joinCodeOf(link: String?): String? {
            val trimmed = link?.trim()?.takeIf { it.isNotBlank() } ?: return null
            val withoutScheme = trimmed.substringAfter("https://", missingDelimiterValue = "")
            if (!withoutScheme.startsWith("$LINK_HOST/", ignoreCase = true)) return null
            val path = withoutScheme.substringAfter('/').substringBefore('?').substringBefore('#')
            val segments = path.split('/').filter { it.isNotBlank() }
            if (segments.size != JOIN_SEGMENTS || segments[0] != "chat" || segments[1] != "join") return null
            return segments[2]
        }

        private const val JOIN_SEGMENTS = 3

        // Dating pushes (notification-service dating_consumer.go / chat_consumer.go).
        const val TYPE_DATING_SPARK = "dating.spark.created"
        const val TYPE_DATING_MATCH = "dating.match.formed"
        const val TYPE_DATING_MESSAGE = "dating.match.new_message"
        const val TYPE_DATING_FIRST_MESSAGE = "dating.match.first_message"

        // Pulse safety pushes (notification-service dating_pulse.go): a scam
        // alert opens the safety page; a date check-in opens the match with
        // its "how did it go?" sheet.
        const val TYPE_DATING_SCAM_ALERT = "dating_scam_alert"
        const val TYPE_DATING_DATE_CHECKIN = "dating_date_checkin"

        /**
         * Where a dating push lands, or null when it is not one / carries no
         * usable id. Pure string work, like [joinCodeOf].
         *
         *  - a spark opens the incoming sparks list;
         *  - a formed match opens that match (its `deep_link`
         *    `/dating/matches/{match_id}`, else `entity_id`, which is the match id);
         *  - a message opens the match and continues into its chat. Only the
         *    deep link is trusted here: the two consumers put DIFFERENT ids in
         *    `entity_id` (a match id or a conversation id).
         */
        fun datingTargetOf(destination: PushDestination): DatingPushTarget? = when (destination.type) {
            TYPE_DATING_SPARK -> DatingPushTarget.IncomingSparks
            TYPE_DATING_MATCH ->
                (datingMatchIdOf(destination.deepLink) ?: destination.entityId.trim().takeIf { it.isNotEmpty() })
                    ?.let { DatingPushTarget.Match(it, openChat = false) }
            TYPE_DATING_MESSAGE, TYPE_DATING_FIRST_MESSAGE ->
                datingMatchIdOf(destination.deepLink)?.let { DatingPushTarget.Match(it, openChat = true) }
            TYPE_DATING_SCAM_ALERT -> DatingPushTarget.Safety
            TYPE_DATING_DATE_CHECKIN ->
                (datingMatchIdOf(destination.deepLink) ?: destination.entityId.trim().takeIf { it.isNotEmpty() })
                    ?.let { DatingPushTarget.Match(it, openChat = false, checkIn = true) }
            else -> null
        }

        /** The id in `/dating/matches/{id}` (query and fragment ignored); null for anything else. */
        fun datingMatchIdOf(deepLink: String?): String? {
            val path = deepLink?.trim()?.substringBefore('?')?.substringBefore('#') ?: return null
            val segments = path.split('/').filter { it.isNotBlank() }
            if (segments.size != MATCH_SEGMENTS || segments[0] != "dating" || segments[1] != "matches") return null
            return segments[2]
        }

        private const val MATCH_SEGMENTS = 3

        // Mopedu pushes (2026-09-18): the customer's ride progress and payment.
        // Every one opens the ride screen, which asks the server for the active
        // ride (or its receipt) rather than trusting the push's payload.
        val RIDE_TYPES: Set<String> = setOf(
            "ride.assigned",
            "ride.arriving",
            "ride.arrived",
            "ride.started",
            "ride.completed",
            "ride.cancelled",
            "ride.payment.paid",
        )

        fun isRidePush(type: String?): Boolean = type in RIDE_TYPES

        // Doorstep pushes (2026-10-04): the 17 Momentum types of the registry
        // (contracts/doorstep/asyncapi.yaml x-push-types.momentum), sent by
        // notification-service on the doorstep_updates channel. The
        // professionals' `doorstep.pro.*` types belong to their own app and are
        // deliberately NOT here.
        const val TYPE_DOORSTEP_OUTSTANDING_DUE = "doorstep.outstanding.due"

        val DOORSTEP_TYPES: Set<String> = setOf(
            "doorstep.booking.confirmed",
            "doorstep.booking.assigned",
            "doorstep.booking.reassigned",
            "doorstep.booking.pro_en_route",
            "doorstep.booking.pro_arrived",
            "doorstep.booking.started",
            "doorstep.booking.extras_proposed",
            "doorstep.booking.extras_payment_due",
            "doorstep.booking.completed",
            "doorstep.booking.cancelled",
            "doorstep.booking.expired",
            "doorstep.booking.refund_issued",
            "doorstep.booking.reminder",
            "doorstep.booking.pro_no_show",
            TYPE_DOORSTEP_OUTSTANDING_DUE,
            "doorstep.message.new",
            "doorstep.rework.updated",
        )

        fun isDoorstepPush(type: String?): Boolean = type in DOORSTEP_TYPES

        /**
         * Where a Doorstep push lands, or null when it is not one / carries no
         * usable id. Pure string work, like [joinCodeOf].
         *
         *  - the dues push opens the dues screen;
         *  - every other type opens the booking: the id in the deep link
         *    (`/doorstep/bookings/{id}[/extras|/rate|/chat]`, bare or with the
         *    registry's `momentum://` scheme), else `entity_id` (the booking id).
         *
         * The screen then reads the booking from the server; nothing in the
         * push payload is trusted as state.
         */
        fun doorstepTargetOf(destination: PushDestination): DoorstepPushTarget? {
            if (destination.type !in DOORSTEP_TYPES) return null
            if (destination.type == TYPE_DOORSTEP_OUTSTANDING_DUE) return DoorstepPushTarget.Outstanding
            val id = doorstepBookingIdOf(destination.deepLink) ?: destination.entityId.trim().takeIf { it.isValidId() }
            return id?.let { DoorstepPushTarget.Booking(it) }
        }

        /** The id in `[momentum://]/doorstep/bookings/{id}[/…]` (query and fragment ignored); null for anything else. */
        fun doorstepBookingIdOf(deepLink: String?): String? {
            val path = deepLink?.trim()
                ?.removePrefix(MOMENTUM_SCHEME)
                ?.substringBefore('?')
                ?.substringBefore('#')
                ?: return null
            val segments = path.split('/').filter { it.isNotBlank() }
            if (segments.size < BOOKING_SEGMENTS || segments[0] != "doorstep" || segments[1] != "bookings") return null
            return segments[2].takeIf { it.isValidId() }
        }

        private fun String.isValidId(): Boolean = isNotEmpty() && all { it.isLetterOrDigit() || it == '-' }

        private const val MOMENTUM_SCHEME = "momentum://"
        private const val BOOKING_SEGMENTS = 3
    }
}

/** Where a Doorstep notification tap lands. */
sealed interface DoorstepPushTarget {
    data class Booking(val bookingId: String) : DoorstepPushTarget

    data object Outstanding : DoorstepPushTarget
}

/** Where a dating notification tap lands. */
sealed interface DatingPushTarget {
    data object IncomingSparks : DatingPushTarget

    data object Safety : DatingPushTarget

    data class Match(val matchId: String, val openChat: Boolean, val checkIn: Boolean = false) : DatingPushTarget
}

/** The routing triple a chat push carries. Ids only — never content. */
data class PushDestination(
    val type: String,
    val entityId: String,
    val deepLink: String,
)
