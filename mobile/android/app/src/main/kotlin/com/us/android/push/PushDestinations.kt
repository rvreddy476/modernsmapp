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
    }
}

/** Where a dating notification tap lands. */
sealed interface DatingPushTarget {
    data object IncomingSparks : DatingPushTarget

    data class Match(val matchId: String, val openChat: Boolean) : DatingPushTarget
}

/** The routing triple a chat push carries. Ids only — never content. */
data class PushDestination(
    val type: String,
    val entityId: String,
    val deepLink: String,
)
