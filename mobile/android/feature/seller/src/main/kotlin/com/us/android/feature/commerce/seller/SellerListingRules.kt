package com.us.android.feature.commerce.seller

import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.FieldProblem
import com.us.android.feature.commerce.ui.describe

/**
 * What a seller may do to a listing, and what they are told when the server
 * refuses — the rules commerce-service enforces (f6dce2470), said once and
 * pure, so each is a table test rather than a branch buried in a screen.
 */

// ─── A variant's own switch ──────────────────────────────────────────

/**
 * The values a seller may write to `product_variants.status`
 * (`variantSettableStatuses` in reviewedcontent.go). `paused` is a PRODUCT
 * status (`products.status`: draft, active, paused, archived) and is refused
 * on a variant with 400 VARIANT_STATUS_INVALID; `archived` is DELETE.
 */
const val VARIANT_ACTIVE = "active"
const val VARIANT_INACTIVE = "inactive"
const val VARIANT_OUT_OF_STOCK = "out_of_stock"

/**
 * What the "On sale" switch sends. The seller reads "Paused"; the wire says
 * `inactive`, the only variant value that means "the seller switched it off".
 */
fun variantStatusFor(paused: Boolean): String = if (paused) VARIANT_INACTIVE else VARIANT_ACTIVE

/**
 * Whether the switch shows the variant as paused. Only `inactive` is the
 * seller's own switch: `out_of_stock` is about units, and showing it as
 * "paused" offered a Resume that could not put anything on sale.
 */
fun isVariantPaused(status: String): Boolean = status == VARIANT_INACTIVE

// ─── Submitting a listing for review ─────────────────────────────────

/**
 * The approval states a submit may move FROM, and the states a SKU may still
 * change in: the server holds both to the same set (`submittableApprovalStatuses`
 * in onboarding.go, the editable-without-revalidation arm of
 * `ProductEditability`). `pending` is the legacy default, a draft by another
 * name.
 */
private val FIRST_SUBMISSION = setOf("draft", "pending")
private val RESUBMISSION = setOf("rejected", "changes_requested", "flagged")
private val UNAPPROVED_EDITABLE = FIRST_SUBMISSION + RESUBMISSION

/** Which submit button a listing row offers. */
enum class SubmitOffer(val label: String) {
    Submit("Submit for review"),

    /** A reviewer has seen it and sent it back; the seller has fixed it. */
    Resubmit("Resubmit for review"),
}

/**
 * The button a row offers, or null when there is nothing to submit: a
 * listing in front of a reviewer, live, hidden or archived. A state this
 * build does not know offers nothing, rather than a button the server refuses.
 */
fun SellerProduct.submitOffer(): SubmitOffer? = when (approvalStatus) {
    in FIRST_SUBMISSION -> SubmitOffer.Submit
    in RESUBMISSION -> SubmitOffer.Resubmit
    else -> null
}

/**
 * The reviewer's remark, for a listing they sent back. Stored in
 * `rejection_reason` for a rejection and a change request alike.
 */
fun SellerProduct.reviewRemark(): String? =
    rejectionReason?.trim()?.takeIf { it.isNotEmpty() && approvalStatus in RESUBMISSION }

// ─── The SKU ─────────────────────────────────────────────────────────

/**
 * Whether an existing variant's SKU may change. Editable while the listing is
 * unapproved; locked from approval on, because order lines, invoices and the
 * bulk importer hold a variant by it (409 SKU_LOCKED). The same rule as the
 * web editor. Null — the listing's state could not be read — is locked: the
 * field the seller cannot be sure of is the one shown, not offered.
 */
fun skuEditable(approvalStatus: String?): Boolean =
    approvalStatus != null && approvalStatus in UNAPPROVED_EDITABLE

/** Said under a locked SKU. */
const val SKU_LOCKED_COPY = "This SKU can't change once the listing is live."

// ─── Price ───────────────────────────────────────────────────────────

/**
 * Said beside the price fields. A variant's price is not reviewed content: a
 * change goes live at once and the listing stays on sale (f6dce2470).
 */
const val PRICE_GOES_LIVE_COPY = "Price changes go live straight away. Your listing stays on sale."

// ─── Refusals ────────────────────────────────────────────────────────

/** Codes the seller console can meet, in the seller's terms. */
private val SELLER_COPY = mapOf(
    "SKU_LOCKED" to SKU_LOCKED_COPY,
    "PRICE_ABOVE_MRP" to "Selling price can't be higher than the MRP.",
    "VARIANT_STATUS_INVALID" to "That status can't be set here. A variant is on sale, paused, or out of stock.",
    "RETURN_POLICY_INVALID" to "Choose a return window: no returns, 7, 15 or 30 days, or custom.",
    // Price edits no longer need it (f6dce2470); option and image edits on a
    // live listing still do, and the app sends neither. Kept so a server that
    // asks is answered in words, not "Something went wrong".
    "REVALIDATION_REQUIRED" to
        "This change sends the listing back for review, and it stays off sale until it's approved again.",
    "PRODUCT_INCOMPLETE" to "This listing isn't ready for review yet.",
    "PRODUCT_NOT_SUBMITTABLE" to
        "This listing can't be submitted now. It's already being reviewed, already live, or hidden.",
    "PRODUCT_NOT_EDITABLE" to "This listing can't be changed while it's being reviewed, hidden or archived.",
    "SELLER_NOT_APPROVED" to "Your shop is still being reviewed. You can submit listings once it's approved.",
    "SKU_TAKEN" to "That SKU is already in use. Each SKU has to be unique.",
    "PRICE_NOT_POSITIVE" to "Enter a price above zero.",
    // Migration 045: a books-only shop lists only books, at GST 0% (create, edit, submit).
    "SELLER_SCOPE_BOOKS_ONLY" to BOOKS_ONLY_LISTING_COPY,
)

/**
 * One sentence for a refusal on a seller screen. A code the seller console
 * knows gets its own; anything else is the shared commerce copy.
 */
fun CommerceError.describeForSeller(): String =
    (this as? CommerceError.Unexpected)?.let { SELLER_COPY[it.code] } ?: describe()

/**
 * What still has to be filled in, one line per gap, from PRODUCT_INCOMPLETE's
 * `fields`. Empty for any other refusal.
 */
fun CommerceError.listingGaps(): List<String> =
    (this as? CommerceError.Unexpected)?.fields.orEmpty().map(::gapLine)

/**
 * A gap in the seller's words. The built-ins (a code with a dot, which no
 * attribute code may carry) get a sentence that says what to do; a category
 * field is named by its label. The server's `reason` is written for the log.
 */
fun gapLine(gap: FieldProblem): String = when (gap.code) {
    "listing.variant" -> "Add at least one variant."
    "listing.price" -> "Give every variant a selling price."
    "listing.stock" -> "Add stock to at least one variant."
    "listing.image" -> "Add at least one product photo."
    "listing.tax_class" -> "Choose a GST tax class."
    else -> "${gap.label.ifBlank { humanise(gap.code) }} is required."
}

private fun humanise(code: String): String =
    code.substringAfterLast('.').replace('_', ' ').replaceFirstChar { it.uppercase() }

/** A refusal of a listing's submit, as the hub shows it under that row. */
data class SubmitProblem(
    val productId: String,
    val message: String,
    val gaps: List<String> = emptyList(),
)

fun submitProblem(productId: String, error: CommerceError) = SubmitProblem(
    productId = productId,
    message = error.describeForSeller(),
    gaps = error.listingGaps(),
)
