package com.us.android.feature.kitchen.ui

import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.food.repository.FoodError
import com.us.android.feature.kitchen.queue.KitchenClock
import java.time.LocalDate
import java.time.ZoneId

/** What a partner reads for a failure. Never a raw server string for a 5xx, never a submitted value. */
fun FoodError.userMessage(): String = when (this) {
    is FoodError.InvalidField -> message.ifBlank { "Check this field and try again." }
    is FoodError.NotReady -> "A few setup steps are still missing."
    FoodError.NotFound -> "We couldn't find that. It may have been removed."
    FoodError.NotDraft -> "This kitchen has already been submitted for review."
    FoodError.NotLive -> "Your kitchen can take orders once it has been approved."
    FoodError.FssaiRequired -> "An approved, unexpired FSSAI licence is needed before you take orders."
    FoodError.DocumentExpired -> "This document has expired. Upload a current one."
    FoodError.PiiNotConfigured -> "Secure storage for tax and bank details isn't switched on yet. Please try again later."
    FoodError.BankVerificationUnavailable -> "Bank verification is unavailable right now. Please try again later."
    FoodError.InvalidBody -> "The app sent something the server couldn't read. Please update Feast Kitchen."
    FoodError.Unauthorized -> "Your session has ended. Please sign in again."
    FoodError.NotAvailable -> "This isn't available on the server yet."
    is FoodError.Network -> "You're offline. Check your connection and try again."
    // A 5xx message is server internals, never shown.
    is FoodError.Unexpected -> message?.takeIf { it.isNotBlank() && (status ?: 0) < SERVER_ERROR }
        ?: "Something went wrong on our side. Please try again."
}

fun FoodError.asMessage(): UsMessage = UsMessage(userMessage(), UsMessageType.Error)

fun success(text: String): UsMessage = UsMessage(text, UsMessageType.Success)

fun info(text: String): UsMessage = UsMessage(text, UsMessageType.Info)

fun warning(text: String): UsMessage = UsMessage(text, UsMessageType.Warning)

/** Restaurant onboarding and trading states (food-service `restaurants.status`). */
object RestaurantStatusText {
    const val DRAFT = "DRAFT"
    const val PENDING_REVIEW = "PENDING_REVIEW"
    const val ACTIVE = "ACTIVE"
    const val REJECTED = "REJECTED"

    fun label(status: String): String = when (status) {
        DRAFT -> "Setting up"
        PENDING_REVIEW -> "In review"
        ACTIVE -> "Live"
        REJECTED -> "Changes needed"
        "SUSPENDED" -> "Paused by Feast"
        else -> humanise(status)
    }

    fun tone(status: String): PillTone = when (status) {
        ACTIVE -> PillTone.Positive
        PENDING_REVIEW -> PillTone.Warning
        REJECTED, "SUSPENDED" -> PillTone.Danger
        else -> PillTone.Neutral
    }
}

/** food-service orderstate, as a kitchen reads it. */
object OrderStatusText {
    const val CONFIRMED = "CONFIRMED"
    const val PREPARING = "PREPARING"

    /** Ready, and a rider is being found or is on the way: the pickup code applies. */
    val AWAITING_PICKUP: Set<String> = setOf("READY_FOR_PICKUP", "DELIVERY_ASSIGNING", "DELIVERY_ASSIGNED")

    fun label(status: String): String = when (status) {
        CONFIRMED -> "Awaiting accept"
        PREPARING -> "Preparing"
        "READY_FOR_PICKUP" -> "Ready for pickup"
        "DELIVERY_ASSIGNING" -> "Finding a rider"
        "DELIVERY_ASSIGNED" -> "Rider on the way"
        "PICKED_UP" -> "Picked up"
        "OUT_FOR_DELIVERY" -> "Out for delivery"
        "DELIVERED" -> "Delivered"
        "RESTAURANT_REJECTED" -> "Rejected"
        "CANCELLED_BY_CUSTOMER" -> "Cancelled by customer"
        "CANCELLED_BY_RESTAURANT" -> "Cancelled"
        "CANCELLED_BY_ADMIN" -> "Cancelled by Feast"
        "REFUND_PENDING" -> "Refund pending"
        "REFUNDED" -> "Refunded"
        else -> humanise(status)
    }

    fun tone(status: String): PillTone = when (status) {
        CONFIRMED -> PillTone.Accent
        PREPARING -> PillTone.Warning
        in AWAITING_PICKUP -> PillTone.Accent
        "PICKED_UP", "OUT_FOR_DELIVERY", "DELIVERED" -> PillTone.Positive
        else -> if (status.startsWith("CANCELLED") || status.startsWith("REFUND") || status == "RESTAURANT_REJECTED") {
            PillTone.Danger
        } else {
            PillTone.Neutral
        }
    }
}

/** India Standard Time: food-service validates dates and hours in Asia/Kolkata. */
object IndiaTime {
    val ZONE: ZoneId = ZoneId.of("Asia/Kolkata")

    fun today(clock: KitchenClock): LocalDate = clock.now().atZone(ZONE).toLocalDate()
}

/** `2026-09-13T06:35:00Z` or Postgres text → `2026-09-13`, for display only. */
fun datePart(raw: String?): String = raw?.trim()?.take(DATE_LENGTH).orEmpty()

internal fun humanise(code: String): String =
    code.lowercase().replace('_', ' ').replaceFirstChar { it.uppercase() }

private const val SERVER_ERROR = 500
private const val DATE_LENGTH = 10
