package com.us.android.feature.rider.deeplink

import com.us.android.feature.rider.digilocker.DigiLockerReturn
import com.us.android.feature.rider.digilocker.DigiLockerReturnLink
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import java.net.URI
import javax.inject.Inject
import javax.inject.Singleton

/** Where a link or a push takes the rider. */
sealed interface RiderDeepLink {
    /** `/rider/offers/{offer_id}` — notification-service's `food_delivery_offer` deep_link. */
    data class Offer(val offerId: String) : RiderDeepLink

    data class DigiLocker(val link: DigiLockerReturn) : RiderDeepLink
}

object RiderDeepLinks {
    private val OFFER_PATH = Regex("^/rider/offers/([A-Za-z0-9-]{1,64})/?$")

    /**
     * An intent's data URI, or a push `deep_link` (a bare path). A path with
     * anything unexpected in it is refused, never forwarded into navigation.
     */
    fun parse(link: String?): RiderDeepLink? {
        val raw = link?.trim()?.takeIf { it.isNotEmpty() } ?: return null
        DigiLockerReturnLink.parse(raw)?.let { return RiderDeepLink.DigiLocker(it) }
        val path = if (raw.startsWith("/")) raw.substringBefore('?') else runCatching { URI(raw).path }.getOrNull()
        val match = path?.let(OFFER_PATH::matchEntire) ?: return null
        return RiderDeepLink.Offer(match.groupValues[1])
    }

    /**
     * The FCM data payload of `food_delivery_offer` (food_push.go FoodPushData):
     * `{type, order_id, offer_id, expires_at, deep_link, title, body}`. The
     * offer id wins over the deep link when both are present and disagree.
     */
    fun fromPush(data: Map<String, String?>): RiderDeepLink? {
        if (data["type"] != TYPE_DELIVERY_OFFER) return null
        val offerId = data["offer_id"]?.takeIf { OFFER_ID.matches(it) }
        return offerId?.let { RiderDeepLink.Offer(it) } ?: parse(data["deep_link"])
    }

    const val TYPE_DELIVERY_OFFER = "food_delivery_offer"
    private val OFFER_ID = Regex("^[A-Za-z0-9-]{1,64}$")
}

/**
 * Links arriving at the Activity (cold start or singleTop re-delivery), held
 * until the signed-in shell consumes them. Replays the last one, so a link that
 * arrives before the rider screens exist is not lost.
 */
@Singleton
class RiderDeepLinkBus @Inject constructor() {
    private val links = MutableSharedFlow<RiderDeepLink>(replay = 1, onBufferOverflow = BufferOverflow.DROP_OLDEST)

    val incoming: SharedFlow<RiderDeepLink> = links.asSharedFlow()

    fun publish(link: RiderDeepLink) {
        links.tryEmit(link)
    }

    /** Called once a link has been acted on, so a recomposition does not act on it twice. */
    @OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
    fun consumed() {
        links.resetReplayCache()
    }
}
