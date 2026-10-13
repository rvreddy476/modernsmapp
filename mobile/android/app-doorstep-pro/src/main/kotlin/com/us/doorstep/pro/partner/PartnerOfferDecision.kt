package com.us.doorstep.pro.partner

import com.us.android.core.notifications.PartnerOfferCategory
import com.us.android.feature.rider.offers.ServerTime
import kotlinx.serialization.json.*
import retrofit2.Response
import retrofit2.Retrofit
import retrofit2.http.*
import java.time.Instant
import java.util.UUID
import javax.inject.Inject

internal data class PartnerOfferRequest(
    val category: PartnerOfferCategory, val id: String, val expiresAt: Instant, val testOnly: Boolean,
) {
    fun secondsLeft(now: Instant = Instant.now()): Long =
        ((java.time.Duration.between(now, expiresAt).toMillis() + 999) / 1000).coerceAtLeast(0)

    companion object {
        fun parse(data: Map<String, String>): PartnerOfferRequest? {
            val category = PartnerOfferCategory.forType(data["type"]) ?: return null
            val id = data["offer_id"] ?: data["entity_id"] ?: return null
            if (runCatching { UUID.fromString(id) }.isFailure) return null
            val expiry = data["expires_at"]?.let(ServerTime::parse) ?: return null
            return PartnerOfferRequest(category, id, expiry, data["test_only"] == "true")
        }
    }
}

/** Only constant, application-owned relative paths are permitted. Shared Retrofit supplies authentication. */
internal interface PartnerOfferDecisionApi {
    @GET suspend fun read(@Url path: String): Response<JsonObject>
    @POST suspend fun decide(@Url path: String, @Body body: JsonObject): Response<JsonObject>
}

internal fun PartnerOfferCategory.offersPath(): String = when (this) {
    PartnerOfferCategory.HOME -> "v1/doorstep/pro/offers"
    PartnerOfferCategory.MOPED -> "v1/rider/offers/incoming"
    PartnerOfferCategory.FOOD -> "v1/food/delivery/offers/me"
    PartnerOfferCategory.COMMERCE -> "v1/commerce/delivery/offers"
}
internal fun PartnerOfferRequest.decisionPath(accept: Boolean): String {
    val base = when (category) {
        PartnerOfferCategory.HOME -> "v1/doorstep/pro/offers"
        PartnerOfferCategory.MOPED -> "v1/rider/offers"
        PartnerOfferCategory.FOOD -> "v1/food/delivery/offers"
        PartnerOfferCategory.COMMERCE -> "v1/commerce/delivery/offers"
    }
    val action = if (accept) "accept" else if (category in setOf(PartnerOfferCategory.MOPED, PartnerOfferCategory.FOOD)) "reject" else "decline"
    return "$base/$id/$action"
}

internal class PartnerOfferDecision @Inject constructor(retrofit: Retrofit) {
    private val api = retrofit.create(PartnerOfferDecisionApi::class.java)

    suspend fun isBusy(): Boolean {
        val response = api.read("v1/partner/work-session")
        ensureSuccess(response)
        return parseWorkSession(response.body()?.get("data")).reservations.any { it.state in setOf("reserved", "active") }
    }

    suspend fun list(category: PartnerOfferCategory): List<JsonObject> {
        val response = api.read(category.offersPath())
        ensureSuccess(response)
        val data = response.body()?.get("data")
        if (data == JsonNull) return emptyList() // Legacy endpoints serialize a nil slice as null.
        val rows = when (data) {
            is JsonArray -> data
            is JsonObject -> (data["items"] ?: data["offers"]) as? JsonArray
            else -> null
        }
        // A successful empty list is not a network or account failure.
        check(rows != null) { "Couldn't read requests. Please refresh Work." }
        check(rows.all { it is JsonObject }) { "Couldn't read request details. Please refresh Work." }
        return rows.filterIsInstance<JsonObject>()
    }

    suspend fun read(request: PartnerOfferRequest): JsonObject {
        check(!request.testOnly) { "Practice requests cannot access live work." }
        val offer = list(request.category).firstOrNull { it.string("id") == request.id }
            ?: error("This request is no longer available. Check Work for your current assignment.")
        val expiry = ServerTime.parse(offer.string("expires_at"))
        check(expiry != null && expiry > Instant.now() && request.secondsLeft() > 0) { "This request has expired." }
        val status = offer.string("status")
        check(status in setOf("", "open", "pending", "offered", "sent")) { "This request is no longer available." }
        return offer
    }

    suspend fun submit(request: PartnerOfferRequest, accept: Boolean) {
        // Both actions require a fresh authenticated readback, not a claim made in a push.
        read(request)
        check(request.secondsLeft() > 0) { "This request has expired." }
        val response = api.decide(request.decisionPath(accept), buildJsonObject {})
        ensureSuccess(response)
        // Food replies include the authoritative decision; never turn a closed offer into success.
        if (request.category == PartnerOfferCategory.FOOD) {
            val status = (response.body()?.get("data") as? JsonObject)?.string("status")
            check(status == if (accept) "accepted" else "rejected") { "The request changed. Check Work for the latest status." }
        }
    }

    private fun ensureSuccess(response: Response<JsonObject>) {
        check(response.isSuccessful) {
            when (response.code()) {
                401, 403 -> "Sign in and check your partner access before responding."
                404, 409, 410 -> "This request is no longer available. Check Work for the latest status."
                else -> "Couldn't confirm your response. Check Work before trying again."
            }
        }
    }
}
