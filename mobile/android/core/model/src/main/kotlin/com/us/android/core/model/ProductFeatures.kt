package com.us.android.core.model

/**
 * The parked products the api-gateway can open or close (founder decision C,
 * 24 Oct 2026). [wire] is the key in `GET /v1/features`
 * (api-gateway cmd/server/features.go `featureProductPrefixes`); [title] is
 * what a "Not available" screen calls it.
 *
 * Android chat's "Communities" ride on [BROADCAST_CHANNELS]
 * (`/v1/broadcast-channels`); [COMMUNITIES] is the separate community-service
 * product, which Android has no screen for. [GROUPS] is `/v1/groups`, also
 * not used on Android: the chat Groups tab is message-service group
 * conversations, so it is not this product.
 */
enum class Product(val wire: String, val title: String) {
    BROADCAST_CHANNELS("broadcast_channels", "Communities"),
    COMMUNITIES("communities", "Communities"),
    GROUPS("groups", "Groups"),
    QA("qa", "Ask"),
    PAGES("pages", "Pages"),
    MEMORIES("memories", "Memories"),
    FOOD("food", "Feast"),
    RIDER("rider", "Ride"),
    DATING("dating", "Match"),
    DOORSTEP("doorstep", "Doorstep"),
    ;

    companion object {
        fun fromWire(wire: String): Product? = entries.firstOrNull { it.wire == wire }
    }
}

/**
 * Closed when the app has no answer from `GET /v1/features` at all (never
 * read, or every read failed): broadcast channels and communities. Every
 * other product follows the route alone.
 */
val HIDDEN_WITHOUT_FEATURES: Set<Product> = setOf(Product.BROADCAST_CHANNELS, Product.COMMUNITIES)

/** How old the last read may be before a resume reads `GET /v1/features` again. */
const val FEATURES_REFRESH_MS: Long = 10L * 60L * 1000L

/**
 * Is [product] open? [open] is the set of products the last good answer
 * said `enabled: true` for, or null when there has been no good answer.
 * Pure: every surface decides through this one rule.
 */
fun productOpen(product: Product, open: Set<Product>?): Boolean =
    if (open == null) product !in HIDDEN_WITHOUT_FEATURES else product in open

/**
 * Should the app read `GET /v1/features` now? Once per app session (never
 * read yet), then again only when a resume finds the last read at least
 * [FEATURES_REFRESH_MS] old; never while a read is already on its way.
 * [lastReadAtMs] is when the last read STARTED, so a failing route is not
 * hammered on every resume.
 */
fun shouldReadFeatures(lastReadAtMs: Long?, nowMs: Long, inFlight: Boolean): Boolean =
    !inFlight && (lastReadAtMs == null || nowMs - lastReadAtMs >= FEATURES_REFRESH_MS)

/**
 * The product a notification or deep-link target lands in, or null for one
 * that belongs to no parked product. A tap into a closed product lands on
 * "Not available" instead.
 */
fun productOf(target: NotificationTarget): Product? = when (target) {
    is NotificationTarget.Customer -> productOf(target.service)
    NotificationTarget.CustomerDues -> Product.DOORSTEP
    else -> null
}

/** The product a customer notification's service belongs to; Shop is never parked. */
fun productOf(service: CustomerNotification.Service): Product? = when (service) {
    CustomerNotification.Service.HOME -> Product.DOORSTEP
    CustomerNotification.Service.FOOD -> Product.FOOD
    CustomerNotification.Service.MOPED -> Product.RIDER
    CustomerNotification.Service.COMMERCE -> null
}
