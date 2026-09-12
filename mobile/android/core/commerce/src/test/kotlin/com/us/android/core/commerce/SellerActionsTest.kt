package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.SellerAction
import com.us.android.core.commerce.model.sellerActionsFor
import org.junit.Test

/**
 * The seller's action table matches the server's transition matrix.
 *
 * The Kotlin table cannot import the SQL one, so its rows are asserted
 * here: the `actor_type = 'seller'` entries of `order_status_transitions`
 * from commerce-service's migration 010. A row added on one side and not
 * the other is a button that the server refuses, or an action the seller is
 * never offered.
 */
class SellerActionsTest {

    @Test
    fun `a confirmed order can be packed or cancelled`() {
        assertThat(sellerActionsFor(OrderStatus.CONFIRMED))
            .containsExactly(SellerAction.PACK, SellerAction.CANCEL)
            .inOrder()
    }

    @Test
    fun `a packed order can be shipped or cancelled`() {
        assertThat(sellerActionsFor(OrderStatus.PACKED))
            .containsExactly(SellerAction.SHIP, SellerAction.CANCEL)
            .inOrder()
    }

    @Test
    fun `nothing can be done once the parcel is with the courier`() {
        // No seller row from shipped onwards: shipped -> delivered is the
        // courier webhook's (system) and shipped -> cancelled is an admin's.
        for (status in listOf(OrderStatus.SHIPPED, OrderStatus.OUT_FOR_DELIVERY, OrderStatus.DELIVERED)) {
            assertThat(sellerActionsFor(status)).isEmpty()
        }
    }

    @Test
    fun `nothing can be done before payment or after the order is dead`() {
        val untouchable = listOf(
            OrderStatus.PAYMENT_PENDING,
            OrderStatus.PAYMENT_FAILED,
            OrderStatus.EXPIRED,
            OrderStatus.CANCELLED,
            OrderStatus.REFUND_PENDING,
            OrderStatus.REFUNDED,
            OrderStatus.UNKNOWN,
        )
        for (status in untouchable) {
            assertThat(sellerActionsFor(status)).isEmpty()
        }
    }

    @Test
    fun `every status has an answer`() {
        // The `when` has an `else`, so a new status compiles; this makes sure
        // it lands on "nothing", the cautious side.
        for (status in OrderStatus.entries) {
            sellerActionsFor(status)
        }
    }
}
