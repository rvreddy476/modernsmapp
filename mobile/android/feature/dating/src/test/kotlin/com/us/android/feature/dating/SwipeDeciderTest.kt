package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.dating.home.SwipeDecider
import com.us.android.feature.dating.home.SwipeDirection
import com.us.android.feature.dating.home.SwipeHint
import org.junit.Test

/**
 * The deck gesture's arithmetic: which way a released card goes, if any.
 *
 * A 1000 x 1500 card, so the thresholds are round: 300 across, 330 up, and a
 * flick needs 80 across (120 up) at 1000 px/s.
 */
class SwipeDeciderTest {

    private val decider = SwipeDecider(width = 1000f, height = 1500f, flingVelocity = 1000f)
    private val withUp = SwipeDecider(width = 1000f, height = 1500f, flingVelocity = 1000f, upEnabled = true)

    @Test
    fun `released past the threshold to the right is a spark, to the left a pass`() {
        assertThat(decider.decide(300f, 0f, 0f, 0f)).isEqualTo(SwipeDirection.RIGHT)
        assertThat(decider.decide(-300f, 40f, 0f, 0f)).isEqualTo(SwipeDirection.LEFT)
    }

    @Test
    fun `released short of the threshold goes nowhere`() {
        assertThat(decider.decide(299f, 0f, 0f, 0f)).isNull()
        assertThat(decider.decide(-299f, 0f, 0f, 0f)).isNull()
        assertThat(decider.decide(0f, 0f, 0f, 0f)).isNull()
    }

    @Test
    fun `a flick in the direction of the lean commits from a short drag`() {
        assertThat(decider.decide(80f, 0f, 1000f, 0f)).isEqualTo(SwipeDirection.RIGHT)
        assertThat(decider.decide(-80f, 0f, -1000f, 0f)).isEqualTo(SwipeDirection.LEFT)
        // Fast, but the card has barely moved: a tap that slipped.
        assertThat(decider.decide(79f, 0f, 5000f, 0f)).isNull()
        // Moved, but slowly.
        assertThat(decider.decide(80f, 0f, 999f, 0f)).isNull()
    }

    @Test
    fun `a flick back against the lean is a change of mind`() {
        assertThat(decider.decide(400f, 0f, -1000f, 0f)).isNull()
        assertThat(decider.decide(-400f, 0f, 1000f, 0f)).isNull()
        // Drifting back slowly past the threshold still counts.
        assertThat(decider.decide(400f, 0f, -200f, 0f)).isEqualTo(SwipeDirection.RIGHT)
    }

    @Test
    fun `an upward drag is nothing while Super Spark is off`() {
        assertThat(decider.decide(0f, -900f, 0f, -5000f)).isNull()
        assertThat(decider.hint(0f, -900f)).isNull()
        // It still leans a little to one side, and that side is all it can mean.
        assertThat(decider.decide(20f, -900f, 0f, -5000f)).isNull()
        assertThat(decider.hint(20f, -900f)?.direction).isEqualTo(SwipeDirection.RIGHT)
    }

    @Test
    fun `an upward drag is a Super Spark once it is on`() {
        assertThat(withUp.decide(0f, -330f, 0f, 0f)).isEqualTo(SwipeDirection.UP)
        assertThat(withUp.decide(0f, -329f, 0f, 0f)).isNull()
        assertThat(withUp.decide(0f, -120f, 0f, -1000f)).isEqualTo(SwipeDirection.UP)
        // A downward drag is never anything.
        assertThat(withUp.decide(0f, 900f, 0f, 5000f)).isNull()
    }

    @Test
    fun `the axis that moved further decides`() {
        assertThat(withUp.decide(350f, -340f, 0f, 0f)).isEqualTo(SwipeDirection.RIGHT)
        assertThat(withUp.decide(340f, -350f, 0f, 0f)).isEqualTo(SwipeDirection.UP)
        assertThat(withUp.decide(-350f, -340f, 0f, 0f)).isEqualTo(SwipeDirection.LEFT)
    }

    @Test
    fun `the hint grows to full at the threshold and names the side`() {
        assertThat(decider.hint(150f, 0f)).isEqualTo(SwipeHint(SwipeDirection.RIGHT, 0.5f))
        assertThat(decider.hint(-600f, 0f)).isEqualTo(SwipeHint(SwipeDirection.LEFT, 1f))
        assertThat(withUp.hint(0f, -165f)).isEqualTo(SwipeHint(SwipeDirection.UP, 0.5f))
        assertThat(decider.hint(0f, 0f)).isNull()
    }

    @Test
    fun `the card tilts with the drag and no further than the cap`() {
        assertThat(decider.rotation(0f)).isEqualTo(0f)
        assertThat(decider.rotation(500f)).isEqualTo(SwipeDecider.MAX_ROTATION / 2)
        assertThat(decider.rotation(-5000f)).isEqualTo(-SwipeDecider.MAX_ROTATION)
    }

    @Test
    fun `a card that has not been measured yet decides nothing`() {
        val unmeasured = SwipeDecider(width = 0f, height = 0f, flingVelocity = 1000f)
        assertThat(unmeasured.decide(500f, 0f, 5000f, 0f)).isNull()
        assertThat(unmeasured.hint(500f, 0f)).isNull()
        assertThat(unmeasured.rotation(500f)).isEqualTo(0f)
    }
}
