@file:OptIn(ExperimentalSerializationApi::class)

package com.us.android.core.memberships.data

import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.ExperimentalSerializationApi
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive

/**
 * Wire types for paid memberships (contract pinned 2026-10-23), transcribed
 * from the Go handlers — there are no golden captures for these routes yet,
 * so every type names the struct it mirrors. Paths are under
 * `Architecture/services/monetization-service/internal/`.
 *
 * Every field has a default: Go writes zero values and `omitempty` keys are
 * absent, and a strict parser here would blank a screen over a field nothing
 * renders.
 */

/**
 * A level — `postgres.CreatorTier` (store/postgres/store.go:144). `perks` is
 * a `json.RawMessage` the service writes as an array of strings
 * (service/membership.go `normaliseTierPerks`); anything else reads as none.
 */
@Serializable
data class TierDto(
    val id: String = "",
    @SerialName("creator_id") val creatorId: String = "",
    val name: String = "",
    @SerialName("price_paise") val pricePaise: Long = 0L,
    val currency: String = "",
    val perks: JsonElement? = null,
    @SerialName("subscriber_count") val subscriberCount: Int = 0,
    @SerialName("is_active") val isActive: Boolean = false,
    @SerialName("billing_period") val billingPeriod: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
) {
    /** The perks as text, in order; blanks dropped; a non-array is none. */
    val perkList: List<String> get() = perksOf(perks)
}

/** A perks raw message as text, in order; blanks dropped; a non-array is none. */
internal fun perksOf(perks: JsonElement?): List<String> = (perks as? JsonArray).orEmpty()
    .mapNotNull { (it as? JsonPrimitive)?.takeIf { p -> p.isString }?.content?.trim() }
    .filter { it.isNotEmpty() }

/**
 * `POST /v1/monetization/tiers` — `CreateTierRequest` (http/handler.go:865).
 * Every field is sent, defaults included (`@EncodeDefault`): the platform
 * `Json` omits a value equal to its default, and the request should say
 * what it means rather than lean on the server's fallback.
 */
@Serializable
data class CreateTierRequest(
    val name: String,
    @SerialName("price_paise") val pricePaise: Long,
    @EncodeDefault(EncodeDefault.Mode.ALWAYS) val currency: String = INR,
    @EncodeDefault(EncodeDefault.Mode.ALWAYS) val perks: List<String> = emptyList(),
)

/**
 * `PATCH /v1/monetization/tiers/:id` — `UpdateTierRequest` (http/handler.go:972).
 * Every field optional; a null is left out on the wire (`explicitNulls =
 * false`), which the server reads as "unchanged".
 */
@Serializable
data class UpdateTierRequest(
    val name: String? = null,
    @SerialName("price_paise") val pricePaise: Long? = null,
    val perks: List<String>? = null,
    @SerialName("is_active") val isActive: Boolean? = null,
)

/** The PATCH answer: `{"status":"updated","tier":{...}}` (http/handler.go:1010). */
@Serializable
data class UpdateTierResponseDto(
    val status: String = "",
    val tier: TierDto? = null,
)

/** `GET .../creators/:id/membership-eligibility` — `service.Eligibility` (service/membership_payments.go:572). */
@Serializable
data class EligibilityDto(
    val eligible: Boolean = false,
    val missing: List<String> = emptyList(),
)

/** `POST /v1/monetization/subscribe/:creatorId` body — `SubscribeRequest` (http/membership_payments_handler.go:171). */
@Serializable
data class SubscribeRequest(
    @SerialName("tier_id") val tierId: String,
)

/** `POST .../subscriptions/:id/upgrade` body — `UpgradeSubscriptionRequest` (http/membership_payments_handler.go:232). */
@Serializable
data class UpgradeRequest(
    @SerialName("tier_id") val tierId: String,
)

/**
 * `POST .../subscriptions/:id/cancel` body — `CancelSubscriptionRequest`
 * (http/subscription_handler.go:94). `immediate` is always SENT, false: a
 * member cancels at the period's end and keeps access until then.
 */
@Serializable
data class CancelRequest(
    @EncodeDefault(EncodeDefault.Mode.ALWAYS) val reason: String = "",
    @EncodeDefault(EncodeDefault.Mode.ALWAYS) val immediate: Boolean = false,
)

/** The cancel answer: `{"status":"cancelled_at_period_end"}` (http/subscription_handler.go:138). */
@Serializable
data class StatusDto(
    val status: String = "",
)

/**
 * A join, renewal or paid upgrade — `service.Checkout`
 * (service/membership_payments.go:88). `client_session` is
 * `payments.ClientSession` (payments/client.go:69): provider, order_id,
 * key_id and an optional merchant_display_name — public values only. It is
 * null on the replay of a join that is already paid.
 */
@Serializable
data class CheckoutDto(
    @SerialName("subscription_id") val subscriptionId: String = "",
    @SerialName("payment_id") val paymentId: String = "",
    val kind: String = "",
    val status: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0L,
    val currency: String = "",
    @SerialName("client_session") val clientSession: JsonObject? = null,
) {
    /** The session as the string map `PaymentSession.fromClientSession` takes; empty when absent. */
    val clientSessionMap: Map<String, String>
        get() = clientSession.orEmpty().mapNotNull { (key, value) ->
            (value as? JsonPrimitive)?.takeIf { it.isString }?.let { key to it.content }
        }.toMap()
}

/**
 * The upgrade answer, one type for both shapes the route sends
 * (http/membership_payments_handler.go:270): a paid upgrade is a
 * [CheckoutDto] (201, status `pending_payment`); anything else is
 * `service.TierChange` (service/subscription.go:169, 200, status `upgraded`,
 * `scheduled` or `unchanged`, with `effective_at`).
 */
@Serializable
data class UpgradeResultDto(
    @SerialName("subscription_id") val subscriptionId: String = "",
    @SerialName("payment_id") val paymentId: String = "",
    val kind: String = "",
    val status: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0L,
    val currency: String = "",
    @SerialName("client_session") val clientSession: JsonObject? = null,
    @SerialName("charged_paise") val chargedPaise: Long = 0L,
    @SerialName("tier_id") val tierId: String = "",
    @SerialName("effective_at") val effectiveAt: String = "",
) {
    /** The checkout, when this answer is one (a payment row was opened). */
    fun checkout(): CheckoutDto? = if (paymentId.isNotBlank()) {
        CheckoutDto(subscriptionId, paymentId, kind, status, amountPaise, currency, clientSession)
    } else {
        null
    }
}

/**
 * The payer's poll — `service.PaymentView` (service/membership_payments.go:156):
 * `confirming | paid | failed`, and `refund_status` `pending | refunded |
 * failed` when money is on its way back.
 */
@Serializable
data class PaymentStatusDto(
    @SerialName("payment_id") val paymentId: String = "",
    @SerialName("subscription_id") val subscriptionId: String = "",
    val kind: String = "",
    val status: String = "",
    @SerialName("refund_status") val refundStatus: String? = null,
    @SerialName("amount_paise") val amountPaise: Long = 0L,
    val currency: String = "",
    @SerialName("late_capture") val lateCapture: Boolean = false,
)

/**
 * One row of `GET /v1/monetization/memberships` — `service.MembershipView`
 * (service/membership_reads.go:107) — and, with [MembershipTierDto.perks]
 * and [pendingDowngrade], `GET /v1/monetization/memberships/:subscriptionId`
 * — `service.MembershipDetailView` (service/membership_reads.go:182).
 */
@Serializable
data class MembershipViewDto(
    @SerialName("subscription_id") val subscriptionId: String = "",
    val creator: MembershipCreatorDto = MembershipCreatorDto(),
    val tier: MembershipTierDto = MembershipTierDto(),
    val status: String = "",
    @SerialName("current_period_end") val currentPeriodEnd: String = "",
    @SerialName("access_until") val accessUntil: String? = null,
    @SerialName("renew_available") val renewAvailable: Boolean = false,
    /** The detail only: a downgrade scheduled for the next period; null when none. */
    @SerialName("pending_downgrade") val pendingDowngrade: PendingDowngradeDto? = null,
)

/**
 * `service.MembershipPerson` (service/membership_reads.go:87): the id
 * always, the profile fields when the server's profile lookup answered.
 */
@Serializable
data class MembershipCreatorDto(
    val id: String = "",
    val username: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("avatar_url") val avatarUrl: String = "",
)

/**
 * `service.MembershipTierView` (service/membership_reads.go:118): the level at
 * its locked price; on the detail `service.MembershipDetailTier` adds its
 * perks (a JSON array, `[]` for none).
 */
@Serializable
data class MembershipTierDto(
    val id: String = "",
    val name: String = "",
    @SerialName("price_paise") val pricePaise: Long = 0L,
    val perks: JsonElement? = null,
)

/** `service.PendingDowngrade` (service/membership_reads.go:173). */
@Serializable
data class PendingDowngradeDto(
    @SerialName("tier_id") val tierId: String = "",
    @SerialName("tier_name") val tierName: String = "",
    @SerialName("price_paise") val pricePaise: Long = 0L,
    @SerialName("effective_at") val effectiveAt: String = "",
)

/**
 * One row of `GET /v1/monetization/members` (2026-10-23): a member of the
 * signed-in creator — `service.MemberView` (service/membership_reads.go:254),
 * newest first, paged by `meta.next_cursor`.
 */
@Serializable
data class MemberRowDto(
    @SerialName("subscription_id") val subscriptionId: String = "",
    val subscriber: MembershipCreatorDto = MembershipCreatorDto(),
    val tier: MembershipTierDto = MembershipTierDto(),
    val status: String = "",
    val since: String = "",
    @SerialName("access_until") val accessUntil: String? = null,
)

/** The only currency a level is offered in (service/membership.go `TierCurrency`). */
const val INR = "INR"
