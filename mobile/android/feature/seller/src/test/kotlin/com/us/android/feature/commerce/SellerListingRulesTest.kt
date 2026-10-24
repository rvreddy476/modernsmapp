package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.FieldProblem
import com.us.android.feature.commerce.seller.PRICE_GOES_LIVE_COPY
import com.us.android.feature.commerce.seller.SKU_LOCKED_COPY
import com.us.android.feature.commerce.seller.SubmitOffer
import com.us.android.feature.commerce.seller.describeForSeller
import com.us.android.feature.commerce.seller.gapLine
import com.us.android.feature.commerce.seller.isVariantPaused
import com.us.android.feature.commerce.seller.listingGaps
import com.us.android.feature.commerce.seller.reviewRemark
import com.us.android.feature.commerce.seller.skuEditable
import com.us.android.feature.commerce.seller.submitOffer
import com.us.android.feature.commerce.seller.variantStatusFor
import com.us.android.feature.commerce.ui.describe
import org.junit.Test

/**
 * The listing rules commerce-service enforces (f6dce2470), as the app holds
 * them. Each is a table: the server's own set, every state it names, and the
 * states it does not.
 */
class SellerListingRulesTest {

    // ─── A variant's switch ──────────────────────────────────────────

    @Test
    fun `pausing a variant sends inactive, never paused`() {
        // `paused` is a PRODUCT status. On a variant the server answers 400
        // VARIANT_STATUS_INVALID, so the switch did nothing but show an error.
        assertThat(variantStatusFor(paused = true)).isEqualTo("inactive")
        assertThat(variantStatusFor(paused = false)).isEqualTo("active")
        for (paused in listOf(true, false)) {
            assertThat(variantStatusFor(paused)).isIn(listOf("active", "inactive", "out_of_stock"))
        }
    }

    @Test
    fun `only inactive reads as paused`() {
        assertThat(isVariantPaused("inactive")).isTrue()
        assertThat(isVariantPaused("active")).isFalse()
        // Out of stock is about units, not the seller's switch.
        assertThat(isVariantPaused("out_of_stock")).isFalse()
        assertThat(isVariantPaused("paused")).isFalse()
    }

    // ─── Submit and resubmit ─────────────────────────────────────────

    private fun product(approval: String, remark: String? = null) = SellerProduct(
        id = "p-1",
        title = "Kettle",
        status = "draft",
        approvalStatus = approval,
        rejectionReason = remark,
        imageUrl = null,
    )

    @Test
    fun `a draft is offered a first submit`() {
        assertThat(product("draft").submitOffer()).isEqualTo(SubmitOffer.Submit)
        // `pending` is the legacy default; the server submits it like a draft.
        assertThat(product("pending").submitOffer()).isEqualTo(SubmitOffer.Submit)
    }

    @Test
    fun `a listing a reviewer sent back is offered a resubmit`() {
        // The server's submittableApprovalStatuses. It used to be draft only
        // on the phone, so a rejected listing was a dead end.
        for (state in listOf("rejected", "changes_requested", "flagged")) {
            assertThat(product(state).submitOffer()).isEqualTo(SubmitOffer.Resubmit)
        }
        assertThat(SubmitOffer.Resubmit.label).isEqualTo("Resubmit for review")
        assertThat(SubmitOffer.Submit.label).isEqualTo("Submit for review")
    }

    @Test
    fun `nothing is offered where the server refuses a submit`() {
        for (state in listOf("submitted", "under_review", "approved", "live", "hidden", "archived", "", "new_state")) {
            assertThat(product(state).submitOffer()).isNull()
        }
    }

    @Test
    fun `the reviewer's remark is shown for a listing they sent back`() {
        assertThat(product("rejected", "  Prohibited item ").reviewRemark()).isEqualTo("Prohibited item")
        assertThat(product("changes_requested", "Add a clearer photo").reviewRemark())
            .isEqualTo("Add a clearer photo")
        assertThat(product("flagged", "Check the brand name").reviewRemark()).isEqualTo("Check the brand name")
    }

    @Test
    fun `no remark is shown when there is none, or the listing has moved on`() {
        assertThat(product("rejected").reviewRemark()).isNull()
        assertThat(product("rejected", "   ").reviewRemark()).isNull()
        // An approved listing keeps the old column; it is not news any more.
        assertThat(product("approved", "Add a clearer photo").reviewRemark()).isNull()
        assertThat(product("submitted", "Add a clearer photo").reviewRemark()).isNull()
    }

    // ─── The SKU ─────────────────────────────────────────────────────

    @Test
    fun `the SKU is editable while the listing is unapproved`() {
        for (state in listOf("draft", "pending", "rejected", "changes_requested", "flagged")) {
            assertThat(skuEditable(state)).isTrue()
        }
    }

    @Test
    fun `the SKU is read-only once the listing is approved, in review, or unknown`() {
        for (state in listOf("approved", "live", "submitted", "under_review", "hidden", "archived", "", "new_state")) {
            assertThat(skuEditable(state)).isFalse()
        }
        assertThat(skuEditable(null)).isFalse()
    }

    // ─── Refusals in the seller's words ──────────────────────────────

    private fun refusal(code: String, fields: List<FieldProblem> = emptyList()) =
        CommerceError.Unexpected(code, "server text for the log", httpStatus = 400, fields = fields)

    @Test
    fun `the listing refusals read as plain sentences`() {
        assertThat(refusal("SKU_LOCKED").describeForSeller())
            .isEqualTo("This SKU can't change once the listing is live.")
        assertThat(refusal("PRICE_ABOVE_MRP").describeForSeller())
            .isEqualTo("Selling price can't be higher than the MRP.")
        val generic = CommerceError.Unexpected("WEIRD", "x").describe()
        for (code in listOf(
            "VARIANT_STATUS_INVALID",
            "RETURN_POLICY_INVALID",
            "REVALIDATION_REQUIRED",
            "PRODUCT_INCOMPLETE",
            "PRODUCT_NOT_SUBMITTABLE",
            "SELLER_NOT_APPROVED",
            "PRODUCT_NOT_EDITABLE",
            "SKU_TAKEN",
        )) {
            val copy = refusal(code).describeForSeller()
            assertThat(copy).isNotEqualTo(generic)
            assertThat(copy).doesNotContain(code)
            assertThat(copy).doesNotContain("_")
            assertThat(copy).endsWith(".")
        }
    }

    @Test
    fun `an unknown code and a typed error fall back to the shared copy`() {
        assertThat(refusal("SOMETHING_NEW").describeForSeller())
            .isEqualTo(CommerceError.Unexpected("SOMETHING_NEW", "").describe())
        assertThat(CommerceError.TryAgain.describeForSeller()).isEqualTo(CommerceError.TryAgain.describe())
    }

    @Test
    fun `every gap the server names is one line the seller can act on`() {
        val error = refusal(
            "PRODUCT_INCOMPLETE",
            listOf(
                FieldProblem("listing.variant", "Variants", "r"),
                FieldProblem("listing.price", "Price", "r"),
                FieldProblem("listing.stock", "Stock", "r"),
                FieldProblem("listing.image", "Product image", "r"),
                FieldProblem("listing.tax_class", "GST tax class", "r"),
                FieldProblem("hsn_code", "HSN Code", "Electronics requires this field; it has no value"),
                FieldProblem("fabric_type", "", "r"),
            ),
        )
        assertThat(error.listingGaps()).containsExactly(
            "Add at least one variant.",
            "Give every variant a selling price.",
            "Add stock to at least one variant.",
            "Add at least one product photo.",
            "Choose a GST tax class.",
            "HSN Code is required.",
            "Fabric type is required.",
        ).inOrder()
    }

    @Test
    fun `a refusal without fields has no gaps`() {
        assertThat(refusal("PRICE_ABOVE_MRP").listingGaps()).isEmpty()
        assertThat(CommerceError.TryAgain.listingGaps()).isEmpty()
        assertThat(gapLine(FieldProblem("listing.future", "", "r"))).isEqualTo("Future is required.")
    }

    @Test
    fun `nothing about a price change says it is under review`() {
        // f6dce2470: a variant price edit goes live at once.
        assertThat(PRICE_GOES_LIVE_COPY.lowercase()).doesNotContain("review")
        assertThat(PRICE_GOES_LIVE_COPY).contains("live")
        assertThat(SKU_LOCKED_COPY).isEqualTo(refusal("SKU_LOCKED").describeForSeller())
    }
}
