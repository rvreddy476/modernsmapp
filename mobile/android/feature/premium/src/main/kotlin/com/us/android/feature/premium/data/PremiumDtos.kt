package com.us.android.feature.premium.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/*
 * Momentum Premium on the wire (monetization-service internal/http/premium_handler.go,
 * internal/service/premium.go, premium_invoices.go). Golden bodies:
 * monetization-service testdata/contracts/premium, copied byte for byte into
 * src/test/resources/contracts/premium.
 *
 * Every money field is nullable on purpose: a plan with no configured price
 * answers null price/GST/total, and a pass opened before 13 Nov 2026 has a
 * null GST split. A missing number is never read as zero.
 */

/** `GET /premium/plans` → `{"plans": [...]}`. */
@Serializable
data class PlansDto(val plans: List<PlanDto> = emptyList())

/** One plan (`premium.Plan`). price_paise is the taxable value; total_paise is what the buyer pays. */
@Serializable
data class PlanDto(
    val code: String = "",
    val name: String = "",
    @SerialName("duration_days") val durationDays: Int = 0,
    @SerialName("price_paise") val pricePaise: Long? = null,
    @SerialName("taxable_paise") val taxablePaise: Long? = null,
    @SerialName("gst_paise") val gstPaise: Long? = null,
    @SerialName("total_paise") val totalPaise: Long? = null,
    @SerialName("gst_rate_bp") val gstRateBp: Int? = null,
    @SerialName("prices_exclude_gst") val pricesExcludeGst: Boolean = false,
    @SerialName("tax_provisional") val taxProvisional: Boolean = false,
    val currency: String = "",
    val available: Boolean = false,
)

/**
 * `POST /premium/checkout`'s body: the plan code and NOTHING else. The server
 * decodes with DisallowUnknownFields and refuses any other field with 400;
 * an amount from the client would break the money rule. Built only by
 * [PremiumRepository.checkout].
 */
@Serializable
data class CheckoutRequest(val plan: String)

/** `POST /premium/checkout` → 201 (`service.PremiumCheckout`). amount_paise = total_paise, the intent amount. */
@Serializable
data class CheckoutDto(
    @SerialName("pass_id") val passId: String = "",
    val plan: String = "",
    @SerialName("duration_days") val durationDays: Int = 0,
    val status: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0L,
    val currency: String = "",
    @SerialName("intent_id") val intentId: String = "",
    /** `payments.ClientSession`: provider, order_id, key_id (and an optional merchant_display_name). */
    @SerialName("client_session") val clientSession: Map<String, String>? = null,
    @SerialName("taxable_paise") val taxablePaise: Long? = null,
    @SerialName("gst_paise") val gstPaise: Long? = null,
    @SerialName("total_paise") val totalPaise: Long? = null,
    @SerialName("gst_rate_bp") val gstRateBp: Int? = null,
    @SerialName("tax_provisional") val taxProvisional: Boolean = false,
    @SerialName("prices_exclude_gst") val pricesExcludeGst: Boolean = false,
)

/** `GET /premium/me` (`service.PremiumStatusView`). */
@Serializable
data class StatusDto(
    val active: Boolean = false,
    @SerialName("in_grace") val inGrace: Boolean = false,
    @SerialName("ends_at") val endsAt: String? = null,
    @SerialName("grace_ends_at") val graceEndsAt: String? = null,
    @SerialName("renewal_plans") val renewalPlans: List<PlanDto> = emptyList(),
)

/** `GET /premium/passes/:id` (`service.PremiumPassView`): the app's poll after the sheet closes. */
@Serializable
data class PassDto(
    @SerialName("pass_id") val passId: String = "",
    val plan: String = "",
    /** confirming | paid | failed | refunded. */
    val status: String = "",
    @SerialName("amount_paise") val amountPaise: Long = 0L,
    val currency: String = "",
    @SerialName("starts_at") val startsAt: String? = null,
    @SerialName("ends_at") val endsAt: String? = null,
    /** The pass's tax invoice once issued (on the signed capture), else null. */
    @SerialName("invoice_id") val invoiceId: String? = null,
    @SerialName("taxable_paise") val taxablePaise: Long? = null,
    @SerialName("gst_paise") val gstPaise: Long? = null,
    @SerialName("total_paise") val totalPaise: Long? = null,
    @SerialName("gst_rate_bp") val gstRateBp: Int? = null,
    @SerialName("tax_provisional") val taxProvisional: Boolean = false,
    @SerialName("prices_exclude_gst") val pricesExcludeGst: Boolean = false,
)

/** `GET /premium/invoices` → `{"invoices": [...]}`, the caller's own, newest first. */
@Serializable
data class InvoicesDto(val invoices: List<InvoiceRowDto> = emptyList())

/** One row. total_paise is signed: a credit note's is negative. */
@Serializable
data class InvoiceRowDto(
    val id: String = "",
    val number: String = "",
    /** invoice | credit_note. */
    val kind: String = "",
    /** The IST calendar date, "2026-11-13". */
    val date: String = "",
    @SerialName("total_paise") val totalPaise: Long = 0L,
    val currency: String = "",
    @SerialName("pass_id") val passId: String = "",
)

/** `GET /premium/invoices/:id`: one document, as far as the app reads it. */
@Serializable
data class InvoiceDto(
    val id: String = "",
    val number: String = "",
    val kind: String = "",
    val date: String = "",
    @SerialName("pass_id") val passId: String = "",
    @SerialName("buyer_name") val buyerName: String? = null,
    @SerialName("taxable_paise") val taxablePaise: Long = 0L,
    @SerialName("cgst_paise") val cgstPaise: Long = 0L,
    @SerialName("sgst_paise") val sgstPaise: Long = 0L,
    @SerialName("igst_paise") val igstPaise: Long = 0L,
    @SerialName("total_paise") val totalPaise: Long = 0L,
    val currency: String = "",
    @SerialName("amount_in_words") val amountInWords: String = "",
    @SerialName("original_invoice") val originalInvoice: OriginalInvoiceDto? = null,
)

/** The invoice a credit note reverses. */
@Serializable
data class OriginalInvoiceDto(
    val id: String = "",
    val number: String = "",
    val date: String = "",
)
