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
    }
}

/** The routing triple a chat push carries. Ids only — never content. */
data class PushDestination(
    val type: String,
    val entityId: String,
    val deepLink: String,
)
