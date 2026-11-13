package com.us.android.feature.premium

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.feature.premium.data.CheckoutDto
import com.us.android.feature.premium.data.InvoiceKind
import com.us.android.feature.premium.data.InvoiceRowDto
import com.us.android.feature.premium.data.InvoicesDto
import com.us.android.feature.premium.data.PlanDto
import com.us.android.feature.premium.data.PlansDto
import com.us.android.feature.premium.data.PremiumCodes
import com.us.android.feature.premium.data.PremiumInvoice
import com.us.android.feature.premium.data.PremiumStatus
import com.us.android.feature.premium.data.PremiumWords
import com.us.android.feature.premium.data.StatusTone
import com.us.android.feature.premium.data.amountText
import com.us.android.feature.premium.data.breakdownText
import com.us.android.feature.premium.data.canBuy
import com.us.android.feature.premium.data.gstRate
import com.us.android.feature.premium.data.invoiceDate
import com.us.android.feature.premium.data.invoiceErrorMessage
import com.us.android.feature.premium.data.invoiceFileName
import com.us.android.feature.premium.data.isPremiumOff
import com.us.android.feature.premium.data.lines
import com.us.android.feature.premium.data.paidWords
import com.us.android.feature.premium.data.premiumErrorMessage
import com.us.android.feature.premium.data.priceText
import com.us.android.feature.premium.data.reversed
import com.us.android.feature.premium.data.rupees
import com.us.android.feature.premium.data.toCheckout
import com.us.android.feature.premium.data.toInvoices
import com.us.android.feature.premium.data.toPlan
import com.us.android.feature.premium.data.toPlans
import com.us.android.feature.premium.data.words
import org.junit.Test

/** What the screen says, in the web's words, from the server's numbers only. */
class PremiumWordsTest {

    private val plans = envelope("plans.json", PlansDto.serializer()).data!!.plans.toPlans()

    @Test
    fun `a pass card reads price plus GST, then the server's breakdown`() {
        val month = plans[0]
        assertThat(month.canBuy()).isTrue()
        assertThat(month.priceText()).isEqualTo("₹149")
        assertThat(month.breakdownText()).isEqualTo("₹149 + ₹26.82 GST (18%) = ₹175.82")
        assertThat(plans[1].priceText()).isEqualTo("₹1,499")
        assertThat(plans[1].breakdownText()).isEqualTo("₹1,499 + ₹269.82 GST (18%) = ₹1,768.82")
    }

    @Test
    fun `a plan without a price, without its GST, or not available is never offered`() {
        val noPrice = PlanDto(code = "p", durationDays = 30, available = true).toPlan()!!
        assertThat(noPrice.canBuy()).isFalse()
        assertThat(noPrice.priceText()).isEqualTo("Not available")
        assertThat(noPrice.breakdownText()).isEmpty()

        // A price but no GST split: never sold at the pre-tax price alone.
        val noTax = PlanDto(code = "p", durationDays = 30, pricePaise = 14_900, available = true).toPlan()!!
        assertThat(noTax.canBuy()).isFalse()

        val closed = PlanDto(
            code = "p",
            pricePaise = 14_900,
            taxablePaise = 14_900,
            gstPaise = 2_682,
            totalPaise = 17_582,
            gstRateBp = 1_800,
            available = false,
        ).toPlan()!!
        assertThat(closed.canBuy()).isFalse()
        assertThat(PlanDto(code = "").toPlan()).isNull()
    }

    @Test
    fun `rupees are integer paise with Indian grouping`() {
        assertThat(rupees(14_900)).isEqualTo("₹149")
        assertThat(rupees(2_682)).isEqualTo("₹26.82")
        assertThat(rupees(176_882)).isEqualTo("₹1,768.82")
        assertThat(rupees(10_000_000)).isEqualTo("₹1,00,000")
        assertThat(rupees(5)).isEqualTo("₹0.05")
        assertThat(gstRate(1_800)).isEqualTo("18%")
        assertThat(gstRate(1_250)).isEqualTo("12.5%")
        assertThat(gstRate(1_805)).isEqualTo("18.05%")
    }

    @Test
    fun `the confirm step is Price, GST and Total to pay, from the checkout`() {
        val checkout = envelope("checkout.json", CheckoutDto.serializer()).data!!.toCheckout()
        assertThat(checkout.lines().map { it.label to it.value }).containsExactly(
            "Price" to "₹149",
            "GST (18%)" to "₹26.82",
            "Total to pay" to "₹175.82",
        ).inOrder()
    }

    @Test
    fun `status words — active until, grace, none`() {
        val active = PremiumStatus(active = true, inGrace = false, endsAt = "2026-12-13T10:00:00Z", graceEndsAt = null)
        assertThat(active.words().tone).isEqualTo(StatusTone.ACTIVE)
        assertThat(active.words().title).isEqualTo("Premium is active until 13 Dec 2026")

        val grace = active.copy(inGrace = true, graceEndsAt = "2026-12-16T10:00:00Z")
        assertThat(grace.words().title).isEqualTo("Premium ends on 16 Dec 2026")
        assertThat(grace.words().detail).isEqualTo("Renew to keep posting on Echo.")

        val none = PremiumStatus(false, false, null, null)
        assertThat(none.words().title).isEqualTo("You don't have Premium")
        // Read in IST, as the web does: 20:00 UTC is the next day in India.
        assertThat(paidWords("2026-12-13T20:00:00Z")).isEqualTo("Premium is active until 14 Dec 2026")
        assertThat(paidWords(null)).isEqualTo("Premium is active")
    }

    @Test
    fun `invoices — newest first, credit notes as minus, named after the invoice they reverse`() {
        val rows = InvoicesDto(
            listOf(
                InvoiceRowDto("i1", "MP/26-27/000001", "invoice", "2026-11-13", 17_582, "INR", "pass-1"),
                InvoiceRowDto("c1", "MPC/26-27/000001", "credit_note", "2026-11-14", -17_582, "INR", "pass-1"),
                InvoiceRowDto("i0", "MP/26-27/000000", "invoice", "2026-10-01", 17_582, "INR", "pass-0"),
            ),
        ).invoices.toInvoices()

        assertThat(rows.map { it.id }).containsExactly("c1", "i1", "i0").inOrder()
        val note = rows[0]
        assertThat(note.kind).isEqualTo(InvoiceKind.CREDIT_NOTE)
        assertThat(note.amountText()).isEqualTo("−₹175.82")
        assertThat(rows[1].amountText()).isEqualTo("₹175.82")
        assertThat(note.reversed(rows)?.number).isEqualTo("MP/26-27/000001")
        assertThat(rows[1].reversed(rows)).isNull()
        assertThat(invoiceDate("2026-11-03")).isEqualTo("03 Nov 2026")
        assertThat(invoiceFileName("MP/26-27/000001")).isEqualTo("MP-26-27-000001.html")
        assertThat(invoiceFileName(" ")).isEqualTo("invoice.html")
    }

    @Test
    fun `a credit note is shown as a refund even if its total arrived positive`() {
        val note = PremiumInvoice("c", "MPC/1", InvoiceKind.CREDIT_NOTE, "2026-11-14", 17_582, "INR", "p")
        assertThat(note.amountText()).isEqualTo("−₹175.82")
    }

    @Test
    fun `every server code has clear words`() {
        fun server(code: String) = AppError.Server(statusCode = 503, code = code)
        fun bad(code: String) = AppError.Unknown(code = code, statusCode = 400)

        assertThat(premiumErrorMessage(server(PremiumCodes.NOT_ENABLED))).isEqualTo(PremiumWords.NOT_ENABLED)
        assertThat(premiumErrorMessage(server(PremiumCodes.PRICE_NOT_SET))).isEqualTo("This pass can't be bought yet.")
        assertThat(premiumErrorMessage(server(PremiumCodes.TAX_UNAVAILABLE))).isEqualTo(PremiumWords.TAX_UNAVAILABLE)
        assertThat(premiumErrorMessage(server(PremiumCodes.AMOUNT_MISMATCH))).isEqualTo(PremiumWords.AMOUNT_MISMATCH)
        assertThat(premiumErrorMessage(bad(PremiumCodes.UNKNOWN_PLAN))).isEqualTo(PremiumWords.UNKNOWN_PLAN)
        assertThat(premiumErrorMessage(server(PremiumCodes.PAYMENTS_UNAVAILABLE)))
            .isEqualTo(PremiumWords.PAYMENTS_UNAVAILABLE)
        assertThat(premiumErrorMessage(AppError.Server(502, PremiumCodes.SESSION_UNAVAILABLE)))
            .isEqualTo(PremiumWords.SESSION_UNAVAILABLE)
        assertThat(premiumErrorMessage(AppError.NotFound(code = "NOT_FOUND"))).isEqualTo(PremiumWords.NOT_FOUND)
        assertThat(premiumErrorMessage(server(PremiumCodes.MAINTENANCE))).isEqualTo(PremiumWords.MAINTENANCE)
        assertThat(premiumErrorMessage(bad(PremiumCodes.BAD_REQUEST))).isEqualTo(PremiumWords.BAD_REQUEST)
        assertThat(premiumErrorMessage(AppError.Server(500, PremiumCodes.INTERNAL_ERROR)))
            .isEqualTo(PremiumWords.GENERIC)
        assertThat(premiumErrorMessage(AppError.NoNetwork())).isEqualTo(PremiumWords.NETWORK)

        assertThat(server(PremiumCodes.NOT_ENABLED).isPremiumOff()).isTrue()
        assertThat(server(PremiumCodes.TAX_UNAVAILABLE).isPremiumOff()).isFalse()

        assertThat(invoiceErrorMessage(AppError.NotFound())).isEqualTo("Invoice not found")
        assertThat(invoiceErrorMessage(AppError.NoNetwork())).isEqualTo("Couldn't download this invoice. Try again.")
    }
}
