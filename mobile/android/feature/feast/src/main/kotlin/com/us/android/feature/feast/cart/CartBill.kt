package com.us.android.feature.feast.cart

import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.FeastTotalsPaiseDto
import com.us.android.core.food.network.TaxesAndChargesDto

/** One line of a bill, exactly as the server stated it. */
data class BillLine(val label: String, val amount: Paise)

/**
 * The bill a customer sees for a cart or an order.
 *
 * EVERY figure is read from the server — `totals_paise` and
 * `taxes_and_charges` — and none is derived here. There is no addition, no
 * rate multiplication and no rounding in this file: the server priced the
 * order under GST section 9(5) rules the client does not know, and a client
 * that re-added its own lines would show a total the customer is not charged.
 * [total] is `final_amount_paise`, full stop.
 *
 * [blockedReason] is the server's `pricing_error.message` when the cart
 * cannot be priced; such a cart has no [total] and cannot proceed.
 */
data class CartBill(
    val itemTotal: Paise?,
    val addonTotal: Paise?,
    val charges: List<BillLine>,
    val taxes: List<BillLine>,
    val discount: Paise?,
    val total: Paise?,
    val adviserNotice: String?,
    val blockedReason: String?,
) {
    /** Whether the customer may pay: priced by the server, with something to pay. */
    val payable: Boolean get() = blockedReason == null && total != null && total > Paise.ZERO

    companion object {
        private const val UNPRICED = "This order can't be priced right now. Please try again shortly."

        fun from(cart: FeastCartDto): CartBill {
            val blocked = cart.pricingError?.let { it.message.ifBlank { UNPRICED } }
                ?: if (cart.totalsPaise == null && cart.items.isNotEmpty()) UNPRICED else null
            return build(cart.totalsPaise, cart.taxesAndCharges, blocked)
        }

        /** An order's bill: `money` when the server sent it, otherwise nothing to itemise. */
        fun from(order: FeastOrderDto): CartBill = build(order.money?.totalsPaise, order.money?.taxesAndCharges, null)

        private fun build(totals: FeastTotalsPaiseDto?, taxes: TaxesAndChargesDto?, blocked: String?): CartBill =
            CartBill(
                itemTotal = totals?.itemSubtotalPaise,
                addonTotal = totals?.addonTotalPaise?.takeIf { it > Paise.ZERO },
                charges = taxes?.charges.orEmpty().map { BillLine(it.label.ifBlank { it.kind }, it.amountPaise) },
                taxes = taxes?.taxes.orEmpty().map { BillLine(it.label.ifBlank { it.liability }, it.taxPaise) },
                discount = totals?.discountTotalPaise?.takeIf { it > Paise.ZERO },
                total = if (blocked == null) totals?.finalAmountPaise else null,
                adviserNotice = taxes?.adviserNotice?.takeIf { taxes.needsAdviserConfirmation && it.isNotBlank() },
                blockedReason = blocked,
            )
    }
}
