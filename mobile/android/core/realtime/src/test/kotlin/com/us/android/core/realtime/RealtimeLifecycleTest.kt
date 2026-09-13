package com.us.android.core.realtime

import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.flow
import org.junit.Rule
import org.junit.Test

class RealtimeLifecycleTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class TestOwner : LifecycleOwner {
        val registry = LifecycleRegistry.createUnsafe(this)
        override val lifecycle: Lifecycle get() = registry
    }

    @Test
    fun `the stream is held only while the owner is at least STARTED`() {
        var open = 0
        var opened = 0
        val stream = flow<Unit> {
            open++
            opened++
            try {
                awaitCancellation()
            } finally {
                open--
            }
        }
        val owner = TestOwner()
        owner.registry.currentState = Lifecycle.State.CREATED

        val job = stream.collectWhileStarted(owner) { }
        assertThat(open).isEqualTo(0)

        owner.registry.currentState = Lifecycle.State.STARTED
        assertThat(open).isEqualTo(1)

        owner.registry.currentState = Lifecycle.State.RESUMED
        assertThat(opened).isEqualTo(1)

        owner.registry.currentState = Lifecycle.State.CREATED
        assertThat(open).isEqualTo(0)

        owner.registry.currentState = Lifecycle.State.STARTED
        assertThat(open).isEqualTo(1)
        assertThat(opened).isEqualTo(2)

        owner.registry.currentState = Lifecycle.State.DESTROYED
        assertThat(open).isEqualTo(0)
        assertThat(job.isActive).isFalse()
    }
}
