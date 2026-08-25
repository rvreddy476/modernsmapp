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
}

/** The routing triple a chat push carries. Ids only — never content. */
data class PushDestination(
    val type: String,
    val entityId: String,
    val deepLink: String,
)
