package com.us.android.feature.feast.orders

import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.RefundRequestDto
import com.us.android.core.food.network.SupportTicketDto
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.outcomeUnknown
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
 * One flow per order screen, and one Idempotency-Key per step of the
 * customer's decision (food server lane, 9 Nov 2026): the ticket's and the
 * refund's keys are minted when the customer first asks and REUSED on every
 * retry, so a resend after a lost answer gets the first ticket or request back
 * rather than a second one. An unknown outcome (no answer, a 5xx, the same key
 * still in flight) keeps the key; only a definite refusal frees it, because a
 * refused request left nothing under it. The ticket id is kept too, so a
 * refund that failed is resent under the ticket already opened.
 */
class RefundRequestFlow(private val repository: FeastRepository) {

    private var ticketId: String? = null
    private var ticketKey: String? = null
    private var refundKey: String? = null

    suspend fun submit(order: FeastOrderDto, detail: String?): RefundOutcome {
        val note = detail?.trim()?.takeIf { it.isNotEmpty() }
        val ticket = ticketId ?: when (val opened = openTicket(order, note)) {
            is FoodResult.Success -> opened.value.id.also { ticketId = it }
            is FoodResult.Failure -> return RefundOutcome.Refused(refusalMessage(opened.error))
        }
        val key = refundKey ?: repository.newIdempotencyKey().also { refundKey = it }
        val reason = note ?: DEFAULT_REASON
        return when (val result = repository.requestRefund(order.id, ticket, order.serverTotal(), reason, key)) {
            is FoodResult.Success -> RefundOutcome.Requested(result.value)
            is FoodResult.Failure -> {
                if (!result.error.outcomeUnknown) refundKey = null
                RefundOutcome.Refused(refusalMessage(result.error))
            }
        }
    }

    private suspend fun openTicket(order: FeastOrderDto, note: String?): FoodResult<SupportTicketDto> {
        val key = ticketKey ?: repository.newIdempotencyKey().also { ticketKey = it }
        val opened = repository.createRefundTicket(order.id, order.orderNumber, note, key)
        if (opened is FoodResult.Failure && !opened.error.outcomeUnknown) ticketKey = null
        return opened
    }

    companion object {
        const val DEFAULT_REASON = "The customer asked for a refund after the kitchen started."

        const val ALREADY_REQUESTED = "A refund for this order is already being reviewed, or nothing is left to refund."
        const val NOT_ELIGIBLE = "This order has no captured payment to refund."
        const val NOT_YOURS = "This order isn't on your account."
        const val OUTCOME_UNKNOWN =
            "We couldn't tell whether your refund request went through. Try again: it's the same request, " +
                "so nothing is sent twice."

        fun refusalMessage(error: FoodError): String = when {
            error.outcomeUnknown -> OUTCOME_UNKNOWN
            error is FoodError.InvalidField && error.code == AMOUNT_INVALID -> ALREADY_REQUESTED
            error is FoodError.Unexpected && error.code == NOT_ELIGIBLE_CODE -> NOT_ELIGIBLE
            error is FoodError.Unexpected && error.code == FORBIDDEN_CODE -> NOT_YOURS
            else -> error.serverMessage ?: "Your refund request couldn't be sent. Please try again."
        }

        private const val AMOUNT_INVALID = "FOOD_REFUND_AMOUNT_INVALID"
        private const val NOT_ELIGIBLE_CODE = "FOOD_REFUND_NOT_ELIGIBLE"
        private const val FORBIDDEN_CODE = "FOOD_SUPPORT_ORDER_FORBIDDEN"
    }
}
