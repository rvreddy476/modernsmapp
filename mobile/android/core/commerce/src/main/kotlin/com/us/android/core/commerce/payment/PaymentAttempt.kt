package com.us.android.core.commerce.payment

/**
 * One payment-sheet opening, for one commerce order.
 *
 * C3-LB-4. The order id alone is not enough: a buyer whose first attempt
 * failed can retry the SAME order, and the first attempt's late callback must
 * not be applied to the second. [id] is minted per opening.
 *
 * The handoff bus that used to live beside this type moved to `:core:payments`
 * (`PaymentHandoff`, 2026-09-15) so MStore and Feast share one bus. Checkout
 * converts this attempt to the sheet's application-scoped attempt
 * (`toSheetAttempt()` in `:feature:commerce`) before publishing or matching.
 */
data class PaymentAttempt(
    val orderId: String,
    val id: String,
)
