package com.us.android.feature.feast.orders

import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.RefundRequestDto
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import com.us.android.feature.feast.ui.serverTotal

sealed interface RefundOutcome {
    data class Requested(val refund: RefundRequestDto) : RefundOutcome

    data class Refused(val message: String) : RefundOutcome
}

/**
 * "Request a refund" once the kitchen has started (founder decision 31 Oct):
 * a support ticket for the order, then `POST /v1/food/refunds` for the
 * server's own total, filed under that ticket. Feast support decides; no money
 * moves here, and only payments-service's signed event ever settles a refund.
 *
 * One flow per order screen. The ticket id is kept across retries, so a refund
 * call that failed is resent under the ticket already opened rather than
 * opening another. The refund route takes no Idempotency-Key; a resend after a
 * lost answer is refused by the server's remaining-balance check (422
 * FOOD_REFUND_AMOUNT_INVALID, which counts open requests), never doubled.
 */
class RefundRequestFlow(private val repository: FeastRepository) {

    private var ticketId: String? = null

    suspend fun submit(order: FeastOrderDto, detail: String?): RefundOutcome {
        val note = detail?.trim()?.takeIf { it.isNotEmpty() }
        val ticket = ticketId ?: when (val opened = repository.createRefundTicket(order.id, order.orderNumber, note)) {
            is FoodResult.Success -> opened.value.id.also { ticketId = it }
            is FoodResult.Failure -> return RefundOutcome.Refused(refusalMessage(opened.error))
        }
        val reason = note ?: DEFAULT_REASON
        return when (val result = repository.requestRefund(order.id, ticket, order.serverTotal(), reason)) {
            is FoodResult.Success -> RefundOutcome.Requested(result.value)
            is FoodResult.Failure -> RefundOutcome.Refused(refusalMessage(result.error))
        }
    }

    companion object {
        const val DEFAULT_REASON = "The customer asked for a refund after the kitchen started."

        const val ALREADY_REQUESTED = "A refund for this order is already being reviewed, or nothing is left to refund."
        const val NOT_ELIGIBLE = "This order has no captured payment to refund."
        const val NOT_YOURS = "This order isn't on your account."

        fun refusalMessage(error: FoodError): String = when {
            error is FoodError.InvalidField && error.code == AMOUNT_INVALID -> ALREADY_REQUESTED
            error is FoodError.Unexpected && error.code == NOT_ELIGIBLE_CODE -> NOT_ELIGIBLE
            error is FoodError.Unexpected && error.code == FORBIDDEN_CODE -> NOT_YOURS
            error is FoodError.Network -> "Check your connection and try again."
            else -> error.serverMessage ?: "Your refund request couldn't be sent. Please try again."
        }

        private const val AMOUNT_INVALID = "FOOD_REFUND_AMOUNT_INVALID"
        private const val NOT_ELIGIBLE_CODE = "FOOD_REFUND_NOT_ELIGIBLE"
        private const val FORBIDDEN_CODE = "FOOD_SUPPORT_ORDER_FORBIDDEN"
    }
}
