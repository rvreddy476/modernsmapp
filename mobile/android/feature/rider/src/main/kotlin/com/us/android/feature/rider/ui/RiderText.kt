package com.us.android.feature.rider.ui

import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.food.repository.FoodError
import com.us.android.feature.rider.location.OfflineReason

/** What a rider reads for a failure. Never a raw server string for a 5xx, never a submitted value. */
fun FoodError.userMessage(): String = when (this) {
    is FoodError.InvalidField -> message.ifBlank { "Check this field and try again." }
    is FoodError.NotReady -> "A few verification steps are still missing."
    FoodError.NotFound -> "We couldn't find that. It may have moved on."
    FoodError.NotDraft, FoodError.NotLive, FoodError.FssaiRequired -> "This isn't available for your account."
    FoodError.DocumentExpired -> "This document has expired. Upload a current one."
    FoodError.PiiNotConfigured -> "Secure storage for identity and bank details isn't switched on yet. Please try again later."
    FoodError.BankVerificationUnavailable -> "Bank verification is unavailable right now. Please try again later."
    FoodError.InvalidBody -> "The app sent something the server couldn't read. Please update Feast Rider."
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

fun error(text: String): UsMessage = UsMessage(text, UsMessageType.Error)

/** delivery_partners.status as a rider reads it. */
object PartnerStatusText {
    /** The statuses food-service lets toggle availability (store SetDeliveryAvailability). */
    val CAN_GO_ONLINE: Set<String> = setOf("APPROVED", "ACTIVE", "OFFLINE")

    fun label(status: String): String = when (status) {
        "PENDING_REVIEW" -> "Verification in progress"
        "APPROVED" -> "Approved"
        "ACTIVE" -> "Online"
        "OFFLINE" -> "Offline"
        "REJECTED" -> "Not approved"
        "SUSPENDED" -> "Paused by Feast"
        else -> humanise(status)
    }

    fun tone(status: String): PillTone = when (status) {
        "APPROVED", "ACTIVE" -> PillTone.Positive
        "OFFLINE" -> PillTone.Neutral
        "PENDING_REVIEW" -> PillTone.Warning
        "REJECTED", "SUSPENDED" -> PillTone.Danger
        else -> PillTone.Neutral
    }
}

object DocumentStatusText {
    fun label(status: String): String = when (status) {
        "PENDING" -> "In review"
        "APPROVED" -> "Approved"
        "REJECTED" -> "Rejected"
        else -> humanise(status)
    }

    fun tone(status: String): PillTone = when (status) {
        "APPROVED" -> PillTone.Positive
        "PENDING" -> PillTone.Warning
        "REJECTED" -> PillTone.Danger
        else -> PillTone.Neutral
    }
}

/** Why the rider is offline, when they did not tap the toggle themselves. */
fun OfflineReason.explanation(): String = when (this) {
    OfflineReason.TOGGLED_OFF -> "You're offline."
    OfflineReason.SIGNED_OUT -> "You were signed out, so you went offline."
    OfflineReason.PERMISSION_REVOKED -> "Location access was turned off, so you went offline. Allow it to go online again."
    OfflineReason.NO_FIX -> "We lost your location for two minutes, so you went offline. Check GPS and go online again."
    OfflineReason.SERVER_REFUSED -> "Feast couldn't put you online. Your account may still be in review."
}

/** `2026-09-13T06:35:00Z` or Postgres text → `2026-09-13`, for display only. */
fun datePart(raw: String?): String = raw?.trim()?.take(DATE_LENGTH).orEmpty()

internal fun humanise(code: String): String =
    code.lowercase().replace('_', ' ').replaceFirstChar { it.uppercase() }

private const val SERVER_ERROR = 500
private const val DATE_LENGTH = 10
