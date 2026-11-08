package com.us.android.feature.kitchen.queue

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.repository.KitchenOrderAction
import org.junit.Test
import java.time.Instant

/** Kitchen transition keys and the local "too late" refusal (food audit L8). */
class TransitionKeysTest {

    private var minted = 0
    private val keys = TransitionKeys { "key-${++minted}" }
    private val t0 = Instant.parse("2026-11-08T06:30:00Z")

    @Test
    fun `a retry after an unknown outcome resends the same key`() {
        val first = keys.keyFor("o-1", KitchenOrderAction.ACCEPT)
        keys.settle("o-1", KitchenOrderAction.ACCEPT, outcomeUnknown = true)

        assertThat(keys.isOutstanding("o-1", KitchenOrderAction.ACCEPT)).isTrue()
        assertThat(keys.keyFor("o-1", KitchenOrderAction.ACCEPT)).isEqualTo(first)
    }

    @Test
    fun `a definite answer frees the key, so the next tap is a new request`() {
        val first = keys.keyFor("o-1", KitchenOrderAction.ACCEPT)
        keys.settle("o-1", KitchenOrderAction.ACCEPT, outcomeUnknown = false)

        assertThat(keys.isOutstanding("o-1", KitchenOrderAction.ACCEPT)).isFalse()
        assertThat(keys.keyFor("o-1", KitchenOrderAction.ACCEPT)).isNotEqualTo(first)
    }

    @Test
    fun `keys are per order and per action`() {
        keys.keyFor("o-1", KitchenOrderAction.ACCEPT)

        assertThat(keys.isOutstanding("o-1", KitchenOrderAction.REJECT)).isFalse()
        assertThat(keys.isOutstanding("o-2", KitchenOrderAction.ACCEPT)).isFalse()
    }

    @Test
    fun `a fresh tap after the countdown ends is refused on the device`() {
        val countdown = AcceptCountdown(deadline = t0.plusSeconds(60), now = t0)

        assertThat(refuseAcceptLocally(countdown, t0.plusSeconds(61), retryOfUnknownOutcome = false)).isTrue()
        assertThat(refuseAcceptLocally(countdown, t0.plusSeconds(59), retryOfUnknownOutcome = false)).isFalse()
    }

    @Test
    fun `a retry of an accept whose outcome is unknown always goes to the server`() {
        val countdown = AcceptCountdown(deadline = t0.plusSeconds(60), now = t0)

        assertThat(refuseAcceptLocally(countdown, t0.plusSeconds(120), retryOfUnknownOutcome = true)).isFalse()
    }

    @Test
    fun `an order with no countdown is never refused on the device`() {
        assertThat(refuseAcceptLocally(null, t0, retryOfUnknownOutcome = false)).isFalse()
    }
}
