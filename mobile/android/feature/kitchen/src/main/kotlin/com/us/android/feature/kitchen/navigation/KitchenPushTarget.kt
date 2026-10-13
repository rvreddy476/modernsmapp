package com.us.android.feature.kitchen.navigation

import java.net.URI
import java.net.URLDecoder
import java.util.UUID

/** Push is a navigation hint only. KitchenRoot re-reads owned restaurants first. */
data class KitchenPushTarget(val restaurantId: String?, val orderId: String?, val screen: String, val receipt: String = UUID.randomUUID().toString())

fun kitchenPushTarget(type: String?, deepLink: String?): KitchenPushTarget? {
    if (type != "food_order_new" && type != "food_kitchen_order_updated" && type != "food_kitchen_account_updated") return null
    val uri = runCatching { URI(deepLink ?: "/kitchen") }.getOrNull() ?: return null
    if (uri.isAbsolute || uri.rawAuthority != null) return null
    val restaurant = uri.rawQuery?.split('&')?.firstOrNull { it.startsWith("restaurant_id=") }
        ?.substringAfter('=')?.let { runCatching { URLDecoder.decode(it, "UTF-8") }.getOrNull() }
    if (restaurant != null && runCatching { UUID.fromString(restaurant) }.isFailure) return null
    val path = uri.path.orEmpty().trimEnd('/')
    if (path.startsWith("/kitchen/orders/")) {
        val order = path.removePrefix("/kitchen/orders/")
        if (runCatching { UUID.fromString(order) }.isFailure) return null
        return KitchenPushTarget(restaurant, order, "order")
    }
    return when (path) {
        "/kitchen" -> KitchenPushTarget(restaurant, null, "home")
        "/kitchen/application" -> KitchenPushTarget(restaurant, null, "application")
        "/kitchen/earnings" -> KitchenPushTarget(restaurant, null, "earnings")
        else -> null
    }
}
