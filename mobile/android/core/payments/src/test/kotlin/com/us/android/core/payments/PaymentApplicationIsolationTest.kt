package com.us.android.core.payments

import android.app.Activity
import com.us.android.core.payments.PaymentCoordinatorTest.ScriptedLauncher
import com.us.android.core.payments.PaymentCoordinatorTest.ScriptedSource
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertThrows
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Every payment belongs to ONE application (founder requirement, 2026-09-14).
 *
 * The hazard: MStore and Feast live in one process, and both have orders with
 * ids. A pending Feast payment resumed inside MStore checkout — or an MStore
 * sheet's result settling a Feast attempt — would show a buyer the wrong
 * payment, and possibly the wrong money. These cases prove each seam refuses it.
 */
class PaymentApplicationIsolationTest {

    /** One store shared by both applications: the worst case, not the usual one. */
    private class MapStore : PaymentStateStore {
        val values = linkedMapOf<String, String?>()
        override fun get(key: String): String? = values[key]
        override fun set(key: String, value: String?) {
            values[key] = value
        }
    }

    private val mstoreOrder = PaymentAttempt(applicationId = "mstore", referenceId = "order-1", id = "attempt-1")

    // The same order id and attempt id, in the other application.
    private val feastOrder = PaymentAttempt(applicationId = "feast", referenceId = "order-1", id = "attempt-1")

    @Test
    fun `a pending Feast payment is never resumed inside MStore, and vice versa`() {
        val store = MapStore()
        val feast = InFlightPayment(store, "feast")
        val mstore = InFlightPayment(store, "mstore")

        feast.attempt = feastOrder
        assertNull("MStore must not see Feast's in-flight payment", mstore.attempt)

        mstore.attempt = mstoreOrder
        assertEquals("writing MStore's must not overwrite Feast's", feastOrder, feast.attempt)
        assertEquals(mstoreOrder, mstore.attempt)

        mstore.clear()
        assertNull(mstore.attempt)
        assertEquals("clearing MStore's must leave Feast's pending payment alone", feastOrder, feast.attempt)

        // A process death: both are rebuilt from the same surviving store.
        assertEquals(feastOrder, InFlightPayment(store, "feast").attempt)
        assertNull(InFlightPayment(store, "mstore").attempt)
    }

    @Test
    fun `an in-flight record cannot be written under another application`() {
        val mstore = InFlightPayment(MapStore(), "mstore")
        assertThrows(IllegalArgumentException::class.java) { mstore.attempt = feastOrder }
        assertNull(mstore.attempt)
    }

    @Test
    fun `a record that names another application is not resumed`() {
        val store = MapStore()
        InFlightPayment(store, "mstore").attempt = mstoreOrder
        // However it got there — a bug, a stale bundle — a record naming
        // another application is not this application's payment.
        store["payments.mstore.applicationId"] = "feast"

        assertNull(InFlightPayment(store, "mstore").attempt)
    }

    @Test
    fun `the coordinator will not confirm one application's payment through another's source`() {
        val coordinator = PaymentCoordinator(ScriptedLauncher())
        val feastSource = ScriptedSource({ PaymentStatusReading.Paid }, applicationId = "feast")

        assertThrows(IllegalArgumentException::class.java) {
            coordinator.confirm("mstore", "order-1", feastSource)
        }
        assertTrue("the server must not even have been asked", feastSource.asked.isEmpty())
    }

    @Test
    fun `a sheet will not open a session for another application's attempt`() {
        val launcher = ScriptedLauncher()
        val coordinator = PaymentCoordinator(launcher)
        val feastSession = PaymentSession("feast", "razorpay", "order_rzp_1", "rzp_test_public", 100, "INR", "d")

        assertThrows(IllegalArgumentException::class.java) {
            coordinator.launch(Activity(), mstoreOrder, feastSession) { }
        }
        assertTrue("nothing may be presented", launcher.opened.isEmpty())
    }

    @Test
    fun `the same order id in two applications is two different attempts`() {
        assertNotEquals(mstoreOrder, feastOrder)
        assertNotEquals(
            "a Feast sheet's result can never match an MStore attempt",
            PaymentSheetResult.Closed(feastOrder).attempt,
            mstoreOrder,
        )

        // The REAL launcher: abandoning Feast's attempt must not free MStore's sheet.
        val launcher = RazorpayPaymentLauncher()
        launcher.claim(mstoreOrder) { }
        launcher.abandon(feastOrder)
        assertEquals(mstoreOrder, launcher.inFlightAttempt())
    }

    /**
     * Four products in one process now (Mopedu, 2026-09-18): a ride id, a Feast
     * order id, a Dating purchase id and an MStore order id can collide, and none
     * may ever settle, resume or be read through another's seam.
     */
    @Test
    fun `mopedu, feast, dating and mstore never cross - store, bus, source or sheet`() {
        val apps = listOf("mopedu", "feast", "dating", "mstore")
        val attempts = apps.associateWith { PaymentAttempt(applicationId = it, referenceId = "ref-1", id = "attempt-1") }

        // The same store, four in-flight records: each sees only its own.
        val store = MapStore()
        apps.forEach { InFlightPayment(store, it).attempt = attempts.getValue(it) }
        apps.forEach { app ->
            assertEquals(attempts.getValue(app), InFlightPayment(store, app).attempt)
            apps.filter { it != app }.forEach { other ->
                assertThrows(IllegalArgumentException::class.java) { InFlightPayment(store, app).attempt = attempts.getValue(other) }
            }
        }
        InFlightPayment(store, "mopedu").clear()
        assertNull(InFlightPayment(store, "mopedu").attempt)
        apps.filter { it != "mopedu" }.forEach { assertEquals(attempts.getValue(it), InFlightPayment(store, it).attempt) }

        // The bus: a Mopedu ending is replayed to Mopedu's stream and to no other.
        val handoff = PaymentHandoff()
        handoff.publish(PaymentHandoffEvent.SheetClosed(attempts.getValue("mopedu")))
        assertEquals(1, handoff.events("mopedu").replayCache.size)
        apps.filter { it != "mopedu" }.forEach { assertTrue("$it must not hear Mopedu's ending", handoff.events(it).replayCache.isEmpty()) }

        // The coordinator: a Mopedu ride is never confirmed through another product's source, nor the reverse.
        val coordinator = PaymentCoordinator(ScriptedLauncher())
        apps.forEach { app ->
            apps.filter { it != app }.forEach { other ->
                val source = ScriptedSource({ PaymentStatusReading.Paid }, applicationId = other)
                assertThrows(IllegalArgumentException::class.java) { coordinator.confirm(app, "ref-1", source) }
                assertTrue(source.asked.isEmpty())
            }
        }

        // The sheet: a Mopedu session never opens for another product's attempt.
        val launcher = ScriptedLauncher()
        val mopeduSession = PaymentSession("mopedu", "razorpay", "order_rzp_1", "rzp_test_public", 6500, "INR", "Mopedu ride")
        apps.filter { it != "mopedu" }.forEach { other ->
            assertThrows(IllegalArgumentException::class.java) {
                PaymentCoordinator(launcher).launch(Activity(), attempts.getValue(other), mopeduSession) { }
            }
        }
        assertTrue(launcher.opened.isEmpty())
        assertNotEquals(attempts.getValue("mopedu"), attempts.getValue("feast"))
    }

    @Test
    fun `application ids are non-blank lowercase keys`() {
        listOf("mstore", "feast", "feast.kitchen", "app_2").forEach {
            assertTrue(it, PaymentApplication.isValid(it))
        }
        listOf("", " ", "MStore", "m store", "1app", "-feast").forEach {
            assertFalse("'$it'", PaymentApplication.isValid(it))
            assertThrows(IllegalArgumentException::class.java) { PaymentAttempt(it, "order-1", "a") }
        }
        assertThrows(IllegalArgumentException::class.java) {
            PaymentSession("", "razorpay", "o", "k", 1, "INR", "d")
        }
        assertThrows(IllegalArgumentException::class.java) { InFlightPayment(MapStore(), "Feast") }
        assertThrows(IllegalArgumentException::class.java) { PaymentHistoryQuery(applicationId = " ") }
    }

    /**
     * Mopedu Captain pays its subscription (2026-09-18) under the SAME
     * application id as the rider's fare in Momentum — "mopedu", one payments
     * application — with a different reference (a subscription id, not a ride
     * id) from a different process. Same application, so the coordinator's
     * application check does not separate them; what does is the attempt
     * identity (reference + attempt id) at every seam, and a handoff slot that
     * is process-local. Both are proved here, including the worst case of the
     * two sharing one store and one bus.
     */
    @Test
    fun `the captain's plan payment never crosses the rider's ride payment - same application, different references`() = runTest {
        val ride = PaymentAttempt(applicationId = "mopedu", referenceId = "ride-1", id = "attempt-1")
        val plan = PaymentAttempt(applicationId = "mopedu", referenceId = "sub-1", id = "attempt-1")
        assertNotEquals("the same attempt id on two references is two attempts", ride, plan)

        // Two processes: Momentum's store holds the ride, the captain's holds the plan; neither sees the other's.
        val momentumStore = MapStore()
        val captainStore = MapStore()
        InFlightPayment(momentumStore, "mopedu").attempt = ride
        InFlightPayment(captainStore, "mopedu").attempt = plan
        assertEquals(ride, InFlightPayment(momentumStore, "mopedu").attempt)
        assertEquals(plan, InFlightPayment(captainStore, "mopedu").attempt)
        assertNotEquals(InFlightPayment(momentumStore, "mopedu").attempt, InFlightPayment(captainStore, "mopedu").attempt)

        // Two processes: a plan ending on the captain's bus is never replayed on Momentum's.
        val momentumBus = PaymentHandoff()
        val captainBus = PaymentHandoff()
        captainBus.publish(PaymentHandoffEvent.SheetClosed(plan))
        assertEquals(1, captainBus.events("mopedu").replayCache.size)
        assertTrue("Momentum must not hear the captain's ending", momentumBus.events("mopedu").replayCache.isEmpty())

        // Even on ONE bus (not the case in production), the collector's attempt check keeps them apart.
        val shared = PaymentHandoff()
        shared.publish(PaymentHandoffEvent.SheetClosed(plan))
        val replayed = shared.events("mopedu").replayCache.single()
        assertNotEquals("a ride collector must not match the plan's ending", ride, replayed.attempt)
        assertEquals(plan, replayed.attempt)
        assertEquals("sub-1", replayed.referenceId)
        shared.consume(plan)
        assertFalse("consuming the plan's attempt does not consume the ride's", shared.isConsumed(ride))

        // The launcher: abandoning the plan's attempt never frees a ride sheet, nor the reverse.
        val launcher = RazorpayPaymentLauncher()
        launcher.claim(ride) { }
        launcher.abandon(plan)
        assertEquals(ride, launcher.inFlightAttempt())
        launcher.abandon(ride)
        launcher.claim(plan) { }
        launcher.abandon(ride)
        assertEquals(plan, launcher.inFlightAttempt())

        // The coordinator: confirming the plan asks the source for the SUBSCRIPTION id, never the ride id.
        val source = ScriptedSource({ PaymentStatusReading.Paid }, applicationId = "mopedu")
        val confirmation = PaymentCoordinator(ScriptedLauncher()).confirm("mopedu", plan.referenceId, source).first()
        assertEquals(PaymentConfirmation.Paid, confirmation)
        assertEquals(listOf("sub-1"), source.asked)
        assertFalse("ride-1" in source.asked)

        // And a sheet result for the plan can never match the ride's attempt.
        assertNotEquals(PaymentSheetResult.Closed(plan).attempt, ride)
        assertNotEquals(PaymentSheetResult.Closed(ride).attempt, plan)
    }
}
