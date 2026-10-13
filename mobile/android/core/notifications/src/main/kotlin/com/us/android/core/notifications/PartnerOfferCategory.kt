package com.us.android.core.notifications

/** Wire types are fixed; never derive a category or an API destination from push text. */
enum class PartnerOfferCategory(val wireType: String, val label: String) {
    MOPED("captain.offer", "Moped"),
    HOME("doorstep.pro.offer.new", "Home Services"),
    COMMERCE("commerce.delivery.offer", "Commerce Delivery"),
    FOOD("food_delivery_offer", "Food Delivery");

    companion object {
        fun forType(type: String?) = entries.firstOrNull { it.wireType == type }
        const val ACTIVITY = "com.us.doorstep.pro.partner.PartnerOfferActivity"
        val EXTRA_KEYS = listOf("type", "entity_id", "offer_id", "expires_at", "test_only")
    }
}
