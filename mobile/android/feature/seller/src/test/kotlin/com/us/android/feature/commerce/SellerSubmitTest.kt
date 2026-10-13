package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.SellerRequirement
import com.us.android.feature.commerce.seller.PayoutForm
import com.us.android.feature.commerce.seller.label
import org.junit.Test

/**
 * Getting a shop approved.
 *
 * Two endpoints existed and nothing called them: `POST /onboarding/submit` and
 * `POST /products/:id/submit`. A seller opened a shop that stayed `draft` and
 * listed products that stayed `draft`, so no seller could ever be approved and
 * nothing they listed could go on sale — the catalogue stayed empty and the
 * buyer journey had nothing to sell.
 *
 * And the payout step, which the shop cannot be approved without, failed on
 * every call since it was written: its `ON CONFLICT (seller_id) WHERE
 * is_primary` matched no index.
 */
class SellerSubmitTest {

    // ─── Every requirement is shown, including unknown ones ────────────

    @Test
    fun `every requirement the server can name has copy`() {
        val known = listOf(
            SellerRequirement.StoreName,
            SellerRequirement.Email,
            SellerRequirement.PickupAddress,
            SellerRequirement.PayoutAccount,
            SellerRequirement.KycDocument,
        )
        for (requirement in known) {
            assertThat(requirement.label()).isNotEmpty()
        }
    }

    @Test
    fun `a requirement this build does not recognise is still shown`() {
        // The server may add one. Dropping it from the checklist would leave a
        // seller staring at an apparently complete list that will not submit,
        // with no way forward.
        val unknown = SellerRequirement.from("gst_certificate")
        assertThat(unknown).isInstanceOf(SellerRequirement.Unknown::class.java)
        assertThat(unknown.label()).isNotEmpty()
        assertThat(unknown.label()).doesNotContain("_")
    }

    @Test
    fun `the wire keys parse to the right requirements`() {
        assertThat(SellerRequirement.from("pickup_address"))
            .isEqualTo(SellerRequirement.PickupAddress)
        assertThat(SellerRequirement.from("payout_account"))
            .isEqualTo(SellerRequirement.PayoutAccount)
        assertThat(SellerRequirement.from("kyc_document"))
            .isEqualTo(SellerRequirement.KycDocument)
    }

    // ─── Payout: either a bank account or a UPI id ─────────────────────

    private fun payout(
        holder: String = "A Seller",
        account: String = "",
        ifsc: String = "",
        upi: String = "",
    ) = PayoutForm(
        accountHolderName = holder,
        accountNumber = account,
        ifscCode = ifsc,
        upiId = upi,
    )

    @Test
    fun `a bank account needs both halves`() {
        // An account number with no IFSC cannot be paid into. Accepting it
        // would let a shop be approved with settlement details that do not
        // work, which surfaces days later as a bounced payout.
        assertThat(payout(account = "000111222333").hasBank).isFalse()
        assertThat(payout(ifsc = "TEST0000001").hasBank).isFalse()
        assertThat(payout(account = "000111222333", ifsc = "TEST0000001").hasBank).isTrue()
    }

    @Test
    fun `a UPI id alone is enough`() {
        // Small sellers often have only one of the two, and demanding the
        // other is how a shop stalls at the last step of onboarding.
        val f = payout(upi = "seller@bank")
        assertThat(f.hasUpi).isTrue()
        assertThat(f.isComplete).isTrue()
    }

    @Test
    fun `a bank account alone is enough`() {
        assertThat(payout(account = "000111222333", ifsc = "TEST0000001").isComplete).isTrue()
    }

    @Test
    fun `neither is not enough`() {
        assertThat(payout().isComplete).isFalse()
    }

    @Test
    fun `a half-filled bank section blocks even with a valid UPI id`() {
        // A seller who typed an account number and no IFSC has not "chosen
        // UPI" — they started something and stopped, and silently ignoring the
        // field would save details they believe they entered.
        val f = payout(account = "000111222333", upi = "seller@bank")
        assertThat(f.bankPartiallyFilled).isTrue()
        assertThat(f.isComplete).isFalse()
    }

    @Test
    fun `a malformed UPI id is not a UPI id`() {
        for (bad in listOf("seller", "@bank", "seller@", "")) {
            assertThat(payout(upi = bad).hasUpi).isFalse()
        }
    }

    @Test
    fun `an account holder name is always required`() {
        // The commonest reason a payout bounces is a name that does not match
        // the bank's records, and it bounces days later.
        assertThat(payout(holder = "", upi = "seller@bank").isComplete).isFalse()
    }

    @Test
    fun `a UPI-only seller does not send their handle as an account number`() {
        // account_number is a bank column a payout run reads as digits.
        val sent = payout(upi = "seller@bank").toAccount()
        assertThat(sent.accountNumber).isEmpty()
        assertThat(sent.upiId).isEqualTo("seller@bank")
        assertThat(sent.ifscCode).isNull()
    }

    @Test
    fun `a bank seller sends the bank fields and no UPI id`() {
        val sent = payout(account = "000111222333", ifsc = "TEST0000001").toAccount()
        assertThat(sent.accountNumber).isEqualTo("000111222333")
        assertThat(sent.ifscCode).isEqualTo("TEST0000001")
        assertThat(sent.upiId).isNull()
    }
}
