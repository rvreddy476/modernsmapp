package com.us.doorstep.pro.partner

import kotlinx.serialization.json.*
import java.time.Instant

/** Server-owned state only. Local business selection never changes duty or assignments. */
data class PartnerWorkSession(val category: PartnerBusiness?, val revision: Long, val reservations: List<PartnerReservation>)
data class PartnerReservation(val business: PartnerBusiness, val jobId: String, val state: String, val startsAt: Instant, val endsAt: Instant, val offerExpiresAt: Instant?)
internal fun workBusiness(category: String): PartnerBusiness? = when (category) {
    "moped" -> PartnerBusiness.MOPED; "home_services" -> PartnerBusiness.HOME
    "commerce_delivery" -> PartnerBusiness.COMMERCE_DELIVERY; "food_delivery" -> PartnerBusiness.FOOD_DELIVERY
    else -> null
}
internal fun parseWorkSession(data: JsonElement?, now: Instant = Instant.now()): PartnerWorkSession {
    val root = data as? JsonObject ?: error("Missing work-session data")
    val session = root["session"] as? JsonObject ?: error("Missing work session")
    val rawCategory = session.string("category")
    val category = workBusiness(rawCategory)
    require(rawCategory.isBlank() || category != null) { "Unknown duty category" }
    val revision = (session["revision"] as? JsonPrimitive)?.longOrNull ?: error("Missing session revision")
    require(revision >= 0)
    val reservations = (root["reservations"] as? JsonArray ?: error("Missing reservations")).mapNotNull { element ->
        val row = element as? JsonObject ?: error("Invalid reservation")
        val role = workBusiness(row.string("category")) ?: error("Unknown reservation category")
        val state = row.string("state")
        if (state !in setOf("offered", "reserved", "active")) return@mapNotNull null
        val job = row.string("job_id"); require(job.isNotBlank())
        val starts = Instant.parse(row.string("starts_at")); val ends = Instant.parse(row.string("ends_at")); require(ends > starts)
        val expires = row.string("offer_expires_at").takeIf { it.isNotBlank() }?.let(Instant::parse)
        if (state == "offered" && (expires == null || expires <= now)) return@mapNotNull null
        // Never discard accepted work merely because its estimated end passed.
        PartnerReservation(role, job, state, starts, ends, expires)
    }.sortedWith(compareBy<PartnerReservation> { if (it.state == "active") 0 else if (it.state == "offered") 1 else 2 }.thenBy { it.startsAt })
    return PartnerWorkSession(category, revision, reservations)
}
