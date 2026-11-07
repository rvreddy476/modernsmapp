package com.us.android.core.media.ui

import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The observer every playing surface uses (2026-11-07): Home pauses, coming
 * back resumes — told by the lifecycle itself, against a real
 * [LifecycleRegistry], because a stopped screen runs no composition.
 */
class ForegroundPlaybackTest {

    private val events = mutableListOf<String>()
    private val observer = foregroundPlaybackObserver(pause = { events += "pause" }, resume = { events += "resume" })

    private class Owner : LifecycleOwner {
        val registry: LifecycleRegistry = LifecycleRegistry.createUnsafe(this)
        override val lifecycle: Lifecycle get() = registry
    }

    @Test
    fun `Home pauses and coming back resumes, once each`() {
        val owner = Owner()
        owner.registry.currentState = Lifecycle.State.RESUMED
        owner.registry.addObserver(observer)
        events.clear()

        owner.registry.currentState = Lifecycle.State.CREATED // ON_PAUSE, ON_STOP
        assertThat(events).containsExactly("pause")

        owner.registry.currentState = Lifecycle.State.RESUMED // ON_START, ON_RESUME
        assertThat(events).containsExactly("pause", "resume").inOrder()
    }

    @Test
    fun `a pause that only covers the screen (a dialog, split screen) stops nothing`() {
        val owner = Owner()
        owner.registry.currentState = Lifecycle.State.RESUMED
        owner.registry.addObserver(observer)
        events.clear()

        owner.registry.currentState = Lifecycle.State.STARTED // ON_PAUSE only

        assertThat(events).isEmpty()
    }

    @Test
    fun `added to a screen already in front it hears the start, so resume must be safe then`() {
        val owner = Owner()
        owner.registry.currentState = Lifecycle.State.RESUMED

        owner.registry.addObserver(observer)

        assertThat(events).containsExactly("resume")
    }

    @Test
    fun `leaving the screen for good pauses it`() {
        val owner = Owner()
        owner.registry.currentState = Lifecycle.State.RESUMED
        owner.registry.addObserver(observer)
        events.clear()

        owner.registry.currentState = Lifecycle.State.DESTROYED

        assertThat(events).containsExactly("pause")
    }
}
