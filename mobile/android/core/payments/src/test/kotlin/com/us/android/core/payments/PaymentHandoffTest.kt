package com.us.android.core.payments

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withTimeoutOrNull
import org.junit.Test

/**
 * The one handoff bus both products use (moved from `:core:commerce`).
 *
 * What it must keep: a late subscriber still hears its own ending (replay),
 * an ending is consumed once, and one application's ending never reaches
 * another application's stream.
 */
class PaymentHandoffTest {

    private val mstore = PaymentAttempt(applicationId = "mstore", referenceId = "o-1", id = "a-1")
    private val feast = PaymentAttempt(applicationId = "feast", referenceId = "o-1", id = "a-1")

    @Test
    fun `a screen that subscribes after the sheet closed still hears its ending`() = runTest {
        val bus = PaymentHandoff()
        bus.publish(PaymentHandoffEvent.SheetClosed(feast))

        assertThat(bus.events("feast").first()).isEqualTo(PaymentHandoffEvent.SheetClosed(feast))
    }

    @Test
    fun `a feast ending never reaches mstore and an mstore ending never reaches feast`() = runTest {
        val bus = PaymentHandoff()
        bus.publish(PaymentHandoffEvent.SheetClosed(feast))

        val heardByMStore = withTimeoutOrNull(100) { bus.events("mstore").first() }
        assertThat(heardByMStore).isNull()

        bus.publish(PaymentHandoffEvent.Unavailable(mstore, "no session"))
        // Feast's replay slot still holds Feast's ending: MStore's publish did not displace it.
        assertThat(bus.events("feast").first()).isEqualTo(PaymentHandoffEvent.SheetClosed(feast))
        assertThat(bus.events("mstore").first()).isEqualTo(PaymentHandoffEvent.Unavailable(mstore, "no session"))
    }

    @Test
    fun `consuming is per attempt, and the same order id in another application is a different attempt`() {
        val bus = PaymentHandoff()
        bus.consume(feast)

        assertThat(bus.isConsumed(feast)).isTrue()
        assertThat(bus.isConsumed(mstore)).isFalse()
        assertThat(bus.isConsumed(feast.copy(id = "a-2"))).isFalse()
    }

    @Test
    fun `live subscribers receive events in order`() = runTest(UnconfinedTestDispatcher()) {
        val bus = PaymentHandoff()
        val received = mutableListOf<PaymentHandoffEvent>()
        val job = launch { bus.events("feast").take(2).toList(received) }

        bus.publish(PaymentHandoffEvent.Unavailable(feast, "first"))
        bus.publish(PaymentHandoffEvent.SheetClosed(feast.copy(id = "a-2")))
        job.join()

        assertThat(received).containsExactly(
            PaymentHandoffEvent.Unavailable(feast, "first"),
            PaymentHandoffEvent.SheetClosed(feast.copy(id = "a-2")),
        ).inOrder()
    }

    @Test
    fun `a sheet result maps to the event for the same attempt, with no paid in either`() {
        assertThat(PaymentOutcome.Succeeded("pay_1").toSheetResult(feast).toHandoffEvent())
            .isEqualTo(PaymentHandoffEvent.SheetClosed(feast))
        assertThat(PaymentOutcome.Unavailable("x").toSheetResult(feast).toHandoffEvent())
            .isEqualTo(PaymentHandoffEvent.Unavailable(feast, "x"))
    }
}
