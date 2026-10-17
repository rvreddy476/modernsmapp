package com.us.android.core.designsystem.component

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The end drawer's release rule (the Me tab's ☰, 2026-10-17): a swipe toward
 * the edge closes it once it has travelled past a third of the panel or was
 * flung; anything less springs it back open.
 */
class UsEndDrawerTest {

    private val fling = 1_000f

    @Test
    fun `a short slow drag springs back`() {
        assertThat(endDrawerCloses(hiddenFraction = 0.1f, velocity = 100f, flingPxPerSecond = fling)).isFalse()
        assertThat(endDrawerCloses(hiddenFraction = 0f, velocity = 0f, flingPxPerSecond = fling)).isFalse()
    }

    @Test
    fun `a drag past a third of the panel closes it`() {
        assertThat(endDrawerCloses(hiddenFraction = 0.5f, velocity = 0f, flingPxPerSecond = fling)).isTrue()
    }

    @Test
    fun `a fling toward the edge closes it however short`() {
        assertThat(endDrawerCloses(hiddenFraction = 0.05f, velocity = fling, flingPxPerSecond = fling)).isTrue()
    }

    @Test
    fun `a fling back into the screen does not close it`() {
        assertThat(endDrawerCloses(hiddenFraction = 0.2f, velocity = -fling * 2, flingPxPerSecond = fling)).isFalse()
    }
}
