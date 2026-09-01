package com.us.android.feature.commerce

import com.us.android.feature.commerce.checkout.PaymentMethod
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The payment-method vocabulary must not exceed what the backend chain
 * accepts end to end.
 *
 * This is a real defect this test was written to close, not a hypothetical.
 * The enum offered four methods while the three layers disagreed:
 *
 *	Android enum                    upi, card, net_banking, wallet
 *	commerce CHECK (gated 998)      upi, card, net_banking
 *	payments-service (B6)           upi, card
 *
 * commerce forwards the order's `payment_method` verbatim to payments when it
 * opens the intent, so `net_banking` would pass checkout, commit the order and
 * hold stock — and then intent creation is refused. The buyer is left with a
 * `payment_pending` order they cannot complete, with stock reserved until the
 * expiry sweeper releases it.
 *
 * `wallet` was worse: before B6, payments accepted it and SKIPPED provider
 * order creation, producing an intent with a blank provider reference that can
 * never be captured, refunded or reconciled.
 *
 * If payments-service widens its allowlist, widen this set FIRST and this test
 * second. Widening the enum alone reintroduces the defect.
 */
class PaymentMethodContractTest {

    /** Exactly what `payments-service` accepts today (internal/service/service.go). */
    private val paymentsServiceAccepts = setOf("upi", "card")

    @Test
    fun `every offered method is accepted by payments-service`() {
        PaymentMethod.entries.forEach { method ->
            assertTrue(
                "PaymentMethod.${method.name} sends \"${method.wire}\", which payments-service " +
                    "does not accept. The buyer would get an order they cannot pay for.",
                method.wire in paymentsServiceAccepts,
            )
        }
    }

    @Test
    fun `cod is not expressible`() {
        // A5: prepaid only. COD is absent from the enum rather than hidden in
        // the UI — the review was explicit that hiding a control in Android is
        // not a fence. The server refuses `cod` at the handler, in the
        // checkout transaction, and by a CHECK constraint; this asserts the
        // client has nothing to send in the first place.
        assertTrue(PaymentMethod.entries.none { it.wire.equals("cod", ignoreCase = true) })
    }

    @Test
    fun `wallet and net banking are not offered`() {
        val offered = PaymentMethod.entries.map { it.wire }.toSet()
        assertTrue("wallet must not be offered: it yields a blank provider reference", "wallet" !in offered)
        assertTrue("net_banking must not be offered: payments-service refuses it", "net_banking" !in offered)
    }

    @Test
    fun `wire values are lowercase and stable`() {
        // The server matches these exactly. A capitalised value would be
        // rejected as an unsupported method.
        PaymentMethod.entries.forEach { method ->
            assertEquals(method.wire, method.wire.lowercase())
        }
    }
}
