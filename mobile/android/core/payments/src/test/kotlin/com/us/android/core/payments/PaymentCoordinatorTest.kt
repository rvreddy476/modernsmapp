package com.us.android.core.payments

import android.app.Activity
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.currentTime
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException

/**
 * [PaymentCoordinator] — open a sheet, then believe only the server.
 *
 * The launcher here is scripted so a test can play the SDK; the launcher's own
 * one-flight bookkeeping is proven against the real class by
 * PaymentLauncherOneFlightTest. The poll runs on virtual time, so the full
 * 180-second schedule costs nothing and its timings can be asserted exactly.
 * Application isolation has its own file, PaymentApplicationIsolationTest.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PaymentCoordinatorTest {

    /** Records what it was asked to open and lets the test report as the SDK. */
    internal class ScriptedLauncher : PaymentLauncher {
        val opened = mutableListOf<Pair<PaymentAttempt, PaymentSession>>()
        private var pending: ((PaymentOutcome) -> Unit)? = null

        override fun open(
            activity: Activity,
            attempt: PaymentAttempt,
            session: PaymentSession,
            onOutcome: (PaymentOutcome) -> Unit,
        ) {
            opened += attempt to session
            pending = onOutcome
        }

        override fun abandon(attempt: PaymentAttempt) {
            pending = null
        }

        fun sdkReports(outcome: PaymentOutcome) {
            val callback = checkNotNull(pending) { "no sheet is open" }
            pending = null
            callback(outcome)
        }
    }

    /** Answers from a script, then repeats its last line; counts the reads. */
    internal class ScriptedSource(
        vararg script: () -> PaymentStatusReading,
        override val applicationId: String = APP,
    ) : PaymentStatusSource {
        private val lines = script.toList()
        val asked = mutableListOf<String>()

        override suspend fun status(referenceId: String): PaymentStatusReading {
            asked += referenceId
            return lines[minOf(asked.size, lines.size) - 1]()
        }
    }

    private val activity = Activity()
    private val attempt = PaymentAttempt(applicationId = APP, referenceId = "order-1", id = "attempt-1")
    private val session = PaymentSession(
        applicationId = APP,
        provider = "razorpay",
        providerOrderId = "order_rzp_1",
        keyId = "rzp_test_public",
        amountMinor = 204000,
        currency = "INR",
        description = "Order MS-1",
    )

    @Test
    fun `the coordinator never reports paid from the SDK callback alone`() = runTest {
        val launcher = ScriptedLauncher()
        val coordinator = PaymentCoordinator(launcher)
        val results = mutableListOf<PaymentSheetResult>()

        coordinator.launch(activity, attempt, session) { results += it }
        launcher.sdkReports(PaymentOutcome.Succeeded("pay_1"))

        assertEquals(
            "an SDK success is a closed sheet — ask the server — and nothing more",
            listOf(PaymentSheetResult.Closed(attempt)),
            results,
        )

        // The server never confirms. The SDK said success; that must not matter.
        val source = ScriptedSource({ PaymentStatusReading.Confirming })
        val confirmations = coordinator.confirm(APP, attempt.referenceId, source).toList()

        assertFalse(
            "paid may only come from the status source, never from the SDK; got $confirmations",
            confirmations.any { it is PaymentConfirmation.Paid },
        )
        assertEquals(PaymentConfirmation.TimedOut(180), confirmations.last())
        assertTrue("the server must actually have been asked", source.asked.isNotEmpty())
    }

    @Test
    fun `polling stops at paid`() = runTest {
        val coordinator = PaymentCoordinator(ScriptedLauncher())
        val source = ScriptedSource(
            { PaymentStatusReading.Confirming },
            { PaymentStatusReading.Confirming },
            { PaymentStatusReading.Paid },
            { error("polled again after the server said paid") },
        )

        val confirmations = coordinator.confirm(APP, "order-1", source).toList()

        assertEquals(
            listOf(
                PaymentConfirmation.Confirming(1),
                PaymentConfirmation.Confirming(3),
                PaymentConfirmation.Paid,
            ),
            confirmations,
        )
        assertEquals(listOf("order-1", "order-1", "order-1"), source.asked)
    }

    @Test
    fun `polling stops at failed`() = runTest {
        val coordinator = PaymentCoordinator(ScriptedLauncher())
        val source = ScriptedSource(
            { PaymentStatusReading.Confirming },
            { PaymentStatusReading.Failed(reason = "declined", retryable = true) },
            { error("polled again after the server said failed") },
        )

        val confirmations = coordinator.confirm(APP, "order-1", source).toList()

        assertEquals(
            listOf(
                PaymentConfirmation.Confirming(1),
                PaymentConfirmation.Failed(reason = "declined", retryable = true),
            ),
            confirmations,
        )
        assertEquals(2, source.asked.size)
    }

    @Test
    fun `a refund is final and is never paid`() = runTest {
        val coordinator = PaymentCoordinator(ScriptedLauncher())

        val pending = coordinator.confirm(APP, "o", ScriptedSource({ PaymentStatusReading.RefundPending })).toList()
        val refunded = coordinator.confirm(APP, "o", ScriptedSource({ PaymentStatusReading.Refunded })).toList()

        assertEquals(listOf(PaymentConfirmation.RefundPending), pending)
        assertEquals(listOf(PaymentConfirmation.Refunded), refunded)
    }

    @Test
    fun `polling stops at timeout on commerce's schedule`() = runTest {
        val coordinator = PaymentCoordinator(ScriptedLauncher())
        val source = ScriptedSource({ PaymentStatusReading.Confirming })
        val startedAt = currentTime

        val confirmations = coordinator.confirm(APP, "order-1", source).toList()

        // 1s, 2s, 3s, 4s, then every 5s — the checkout poll's backoff as it
        // shipped — until 180s.
        val elapsed = confirmations.filterIsInstance<PaymentConfirmation.Confirming>().map { it.elapsedSeconds }
        assertEquals(listOf(1, 3, 6, 10, 15, 20, 25), elapsed.take(7))
        assertEquals(180, elapsed.last())
        assertEquals("one read per wait", 38, source.asked.size)
        assertEquals(PaymentConfirmation.TimedOut(180), confirmations.last())
        assertEquals("the whole poll is 180 seconds of waiting", 180_000L, currentTime - startedAt)
    }

    @Test
    fun `a retry after a failed attempt reuses the same session and order`() = runTest {
        val launcher = ScriptedLauncher()
        val coordinator = PaymentCoordinator(launcher)
        val results = mutableListOf<PaymentSheetResult>()

        // First attempt: the SDK reports a failure and the SERVER confirms it,
        // saying the product lets the buyer try again.
        coordinator.launch(activity, attempt, session) { results += it }
        launcher.sdkReports(PaymentOutcome.Failed(2, "declined"))
        val first = coordinator.confirm(
            APP,
            attempt.referenceId,
            ScriptedSource({ PaymentStatusReading.Failed(retryable = true) }),
        ).toList().last()
        assertEquals(PaymentConfirmation.Failed(reason = null, retryable = true), first)

        // Retry: a NEW attempt, the SAME order and the SAME server session.
        val retry = attempt.copy(id = "attempt-2")
        coordinator.launch(activity, retry, session) { results += it }
        launcher.sdkReports(PaymentOutcome.Succeeded("pay_2"))

        assertEquals(2, launcher.opened.size)
        val (retriedAttempt, retriedSession) = launcher.opened[1]
        assertEquals("the retry pays the same order", attempt.referenceId, retriedAttempt.referenceId)
        assertNotEquals("but as a distinct attempt", attempt.id, retriedAttempt.id)
        assertEquals("with the same provider order and key", session, retriedSession)
        assertEquals(
            "each ending is stamped with its own attempt, so the first cannot settle the second",
            listOf(PaymentSheetResult.Closed(attempt), PaymentSheetResult.Closed(retry)),
            results,
        )

        val second = coordinator.confirm(
            APP,
            retry.referenceId,
            ScriptedSource({ PaymentStatusReading.Paid }),
        ).toList().last()
        assertEquals(PaymentConfirmation.Paid, second)

        // A product that does NOT allow a retry says so, and the flag survives.
        val final = coordinator.confirm(
            APP,
            "order-2",
            ScriptedSource({ PaymentStatusReading.Failed(retryable = false) }),
        ).toList().last()
        assertEquals(PaymentConfirmation.Failed(reason = null, retryable = false), final)
    }

    @Test
    fun `a status-source error keeps confirming and does not flip to failed`() = runTest {
        val coordinator = PaymentCoordinator(ScriptedLauncher())
        val source = ScriptedSource(
            { PaymentStatusReading.Unreachable("503") },
            { throw IOException("connection reset") },
            { PaymentStatusReading.Unreachable(null) },
            { PaymentStatusReading.Paid },
        )

        val confirmations = coordinator.confirm(APP, "order-1", source).toList()

        assertEquals(
            "a read that could not be made is 'we do not know yet', never 'failed'",
            listOf(
                PaymentConfirmation.Confirming(1),
                PaymentConfirmation.Confirming(3),
                PaymentConfirmation.Confirming(6),
                PaymentConfirmation.Paid,
            ),
            confirmations,
        )

        // And a server that stays unreachable for the whole poll times out —
        // it is still not a failure.
        val neverAnswers = coordinator.confirm(
            APP,
            "order-1",
            ScriptedSource({ throw IOException("offline") }),
        ).toList()
        assertTrue(neverAnswers.none { it is PaymentConfirmation.Failed })
        assertEquals(PaymentConfirmation.TimedOut(180), neverAnswers.last())
    }

    @Test
    fun `a sheet that could not open is reported to its own attempt`() {
        val launcher = ScriptedLauncher()
        val coordinator = PaymentCoordinator(launcher)
        var result: PaymentSheetResult? = null

        coordinator.launch(activity, attempt, session) { result = it }
        launcher.sdkReports(PaymentOutcome.Unavailable("no session"))

        assertEquals(PaymentSheetResult.Unavailable(attempt, "no session"), result)
    }

    @Test
    fun `a session is built from the server's client_session and intent`() {
        val built = PaymentSession.fromClientSession(
            applicationId = APP,
            clientSession = mapOf("provider" to "razorpay", "order_id" to "order_rzp_1", "key_id" to "rzp_test_public"),
            amountMinor = 204000,
            currency = "INR",
            description = "Order MS-1",
        )
        assertEquals(session, built)

        val empty = PaymentSession.fromClientSession(APP, emptyMap(), 1, "INR", "d")
        assertEquals("missing keys are empty, which the launcher refuses", "", empty.keyId + empty.providerOrderId)
    }

    internal companion object {
        const val APP = "mstore"
    }
}
