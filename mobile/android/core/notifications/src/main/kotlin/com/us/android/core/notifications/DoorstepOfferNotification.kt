package com.us.android.core.notifications

import java.time.Duration
import java.time.Instant

/** Pure expiry policy shared by foreground/background push delivery. Never trust a stale push. */
sealed interface DoorstepOfferNotification {
    data object NotOffer : DoorstepOfferNotification
    data object Invalid : DoorstepOfferNotification
    data class Open(val id: String, val remainingMillis: Long) : DoorstepOfferNotification
    data class Expired(val id: String) : DoorstepOfferNotification

    companion object {
        fun evaluate(data: Map<String, String>, now: Instant = Instant.now()): DoorstepOfferNotification {
            val type = data["type"]
            if (type !in setOf("doorstep.pro.offer.new", "doorstep.pro.offer.expired", "captain.offer", "food_delivery_offer", "commerce.delivery.offer")) return NotOffer
            val id = (if (type == "food_delivery_offer") data["offer_id"] else data["offer_id"] ?: data["entity_id"])?.takeIf { it.isNotBlank() } ?: return Invalid
            if (type == "doorstep.pro.offer.expired") return Expired(id)
            val expiry = runCatching { Instant.parse(data["expires_at"]) }.getOrNull() ?: return Invalid
            val remaining = Duration.between(now, expiry).toMillis()
            return if (remaining > 0) Open(id, remaining) else Expired(id)
        }
    }
}
