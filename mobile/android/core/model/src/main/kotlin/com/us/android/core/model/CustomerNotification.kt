package com.us.android.core.model

/** Customer-only links. Push content is a refresh/navigation hint, never booking state. */
object CustomerNotification {
    enum class Service { HOME, FOOD, COMMERCE, MOPED }

    fun target(link: String): NotificationTarget {
        val path = link.trim().let { if (it.startsWith("momentum://")) "/" + it.removePrefix("momentum://").removePrefix("/") else it }
        if (!path.startsWith('/') || path.startsWith("//") || path.any { it.isWhitespace() || it == '\\' }) return NotificationTarget.None
        val parts = path.substringBefore('?').substringBefore('#').split('/').drop(1)
        if (parts == listOf("doorstep", "outstanding")) return NotificationTarget.CustomerDues
        if (parts.size !in 3..4 || !validId(parts[2])) return NotificationTarget.None
        val service = when (parts.take(2)) {
            listOf("doorstep", "bookings") -> Service.HOME
            listOf("feast", "orders") -> Service.FOOD
            listOf("shop", "orders") -> Service.COMMERCE
            listOf("mopedu", "booking") -> Service.MOPED
            else -> return NotificationTarget.None
        }
        if (parts.size == 4 && parts[3] !in setOf("extras", "rate", "chat", "tracking")) return NotificationTarget.None
        return NotificationTarget.Customer(service, parts[2])
    }

    fun resolve(type: String, entityId: String, link: String): NotificationTarget {
        val service = service(type) ?: return NotificationTarget.None
        val linked = target(link)
        if (linked is NotificationTarget.Customer && linked.service == service) return linked
        if (type == "doorstep.outstanding.due") return NotificationTarget.CustomerDues
        // Only fall back when the link is absent. Never reinterpret a malformed or merchant link.
        if (link.isNotBlank() || !validId(entityId)) return NotificationTarget.None
        return NotificationTarget.Customer(service, entityId)
    }

    fun service(type: String): Service? = when {
        type.startsWith("doorstep.booking.") || type in setOf("doorstep.message.new", "doorstep.rework.updated", "doorstep.outstanding.due") -> Service.HOME
        type == "food_order_status" -> Service.FOOD
        type in setOf("order_confirmed", "order_shipped", "order_delivered", "order_cancelled", "order_refunded", "order_payment_failed") -> Service.COMMERCE
        type in setOf("ride.assigned", "ride.arriving", "ride.arrived", "ride.started", "ride.completed", "ride.cancelled", "ride.payment.paid") -> Service.MOPED
        else -> null
    }

    fun description(type: String): String? {
        val category = when (service(type)) {
            Service.HOME -> "Home Services booking"
            Service.FOOD -> "Food order"
            Service.COMMERCE -> "Shopping order"
            Service.MOPED -> "Moped ride"
            null -> return null
        }
        val action = when (type.substringAfterLast('.').substringAfterLast('_')) {
            "confirmed" -> "confirmed"
            "assigned", "reassigned" -> "partner assigned"
            "arrived" -> "partner arrived"
            "started" -> "started"
            "shipped" -> "on the way"
            "delivered", "completed" -> "completed"
            "cancelled" -> "cancelled"
            "refunded" -> "refund updated"
            "paid" -> "payment confirmed"
            else -> "updated — open for the latest details"
        }
        return "$category: $action"
    }

    private fun validId(value: String) = value.isNotEmpty() && value.length <= 128 && value.all { it in 'a'..'z' || it in 'A'..'Z' || it in '0'..'9' || it == '-' }
}
