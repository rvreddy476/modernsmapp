package com.us.doorstep.pro.partner

import kotlinx.serialization.json.*

/** Selecting a business is an intent, never an approval or permission grant. */
enum class PartnerBusiness(val title: String, val description: String, val endpoint: String?) {
    MOPED("Moped", "Passenger rides", "v1/rider/partners/me"),
    HOME("Home Services", "Repairs and home services", "v1/doorstep/pro/me"),
    COMMERCE_DELIVERY("Commerce Delivery", "Shopping order deliveries", "v1/commerce/delivery/profile"),
    FOOD_DELIVERY("Food Delivery", "Restaurant pickup and customer delivery", "v1/food/delivery/profile");

    val available: Boolean get() = endpoint != null

    companion object {
        fun forNotification(type: String): PartnerBusiness? = when {
            type.startsWith("doorstep.pro.") -> HOME
            type.startsWith("captain.") -> MOPED
            type.startsWith("food_delivery_") -> FOOD_DELIVERY
            type.startsWith("commerce.delivery.") -> COMMERCE_DELIVERY
            else -> null
        }
    }
}

enum class BusinessStatus(val label: String) {
    LOADING("Checking status"), NEW("Not started"), DRAFT("Complete application"),
    REVIEW("Under review"), APPROVED("Approved"), ACTION("Action needed"),
    SUSPENDED("Restricted"), UNKNOWN("Couldn't refresh status"), UNAVAILABLE("Temporarily unavailable");
}

data class BusinessSummary(
    val business: PartnerBusiness,
    val status: BusinessStatus = BusinessStatus.LOADING,
    val registered: Boolean = false,
    val name: String = "",
) {
    val canOpen: Boolean get() = business.available && status != BusinessStatus.UNAVAILABLE
}

internal fun JsonObject.string(key: String): String = (get(key) as? JsonPrimitive)?.contentOrNull.orEmpty()

internal fun businessSummary(business: PartnerBusiness, data: JsonElement?): BusinessSummary {
    if (!business.available) return BusinessSummary(business, BusinessStatus.UNAVAILABLE)
    val record = data as? JsonObject ?: return BusinessSummary(business, BusinessStatus.UNKNOWN)
    if (record.string("id").isBlank()) return BusinessSummary(business, BusinessStatus.UNKNOWN)
    val status = when (record.string("status").lowercase()) {
        "approved", "active", "offline", "online" -> BusinessStatus.APPROVED
        "pending_review", "pending_verification", "under_review", "submitted" -> BusinessStatus.REVIEW
        "draft", "incomplete", "pending" -> BusinessStatus.DRAFT
        "rejected", "changes_requested", "changes_required", "needs_changes" -> BusinessStatus.ACTION
        "suspended", "blocked", "disabled" -> BusinessStatus.SUSPENDED
        else -> BusinessStatus.UNKNOWN
    }
    return BusinessSummary(business, status, true,
        record.string("display_name").ifBlank { record.string("full_name") })
}

/** Only the service's explicit absent-profile responses mean "Not started". */
internal fun businessFailureSummary(business: PartnerBusiness, httpStatus: Int, error: JsonObject?): BusinessSummary {
    val code = error?.string("code").orEmpty()
    val message = error?.string("message").orEmpty()
    val knownAbsent = when (business) {
        PartnerBusiness.HOME -> code in setOf("DOORSTEP_PRO_NOT_FOUND", "DOORSTEP_PRO_REQUIRED", "PRO_NOT_FOUND", "PROFESSIONAL_NOT_FOUND")
        PartnerBusiness.FOOD_DELIVERY -> code == "FOOD_DELIVERY_PROFILE_NOT_FOUND"
        PartnerBusiness.COMMERCE_DELIVERY -> code == "COMMERCE_DELIVERY_PROFILE_NOT_FOUND"
        PartnerBusiness.MOPED -> code in setOf("PARTNER_NOT_FOUND", "RIDER_PARTNER_NOT_FOUND") ||
            (httpStatus == 404 && code == "NOT_FOUND" && message == "no partner profile")
    }
    val absent = httpStatus in setOf(403, 404) && knownAbsent
    // The gateway deliberately returns this response while RIDER_PUBLIC_ENABLED
    // is false. This also covers an unavailable route, never an absent profile.
    // Do not turn it into onboarding permission or an account-review failure.
    val routeUnavailable = httpStatus == 404 &&
        code == "NOT_FOUND" && message.trim().equals("Not found", ignoreCase = true)
    return BusinessSummary(business, when {
        absent -> BusinessStatus.NEW
        routeUnavailable -> BusinessStatus.UNAVAILABLE
        else -> BusinessStatus.UNKNOWN
    })
}

/** A temporarily unavailable service must not erase a previously discovered business. */
internal fun mergeBusinessRefresh(previous: BusinessSummary, result: BusinessSummary): BusinessSummary =
    if (previous.registered && result.status in setOf(BusinessStatus.UNKNOWN, BusinessStatus.UNAVAILABLE)) {
        result.copy(registered = true, name = previous.name)
    } else result

/** Preserve real delivery/ride selections; never reinterpret merchant registration as delivery approval. */
internal fun selectedBusinesses(saved: Set<String>): Set<PartnerBusiness> = saved.mapNotNull { name ->
    when (name) {
        "DELIVERY" -> PartnerBusiness.FOOD_DELIVERY
        "RIDE" -> PartnerBusiness.MOPED
        else -> PartnerBusiness.entries.firstOrNull { it.name == name && it.available }
    }
}.toSet()
