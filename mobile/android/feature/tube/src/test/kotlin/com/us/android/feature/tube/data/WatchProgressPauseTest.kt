package com.us.android.feature.tube.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.datastore.WatchHistoryPauseStore
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * History's "Pause recording" (2026-10-22) is honoured at the one place a
 * progress row is written: while paused, the watch screen's reports send
 * nothing, as the web's watch page skips its POST. Resume reads still go.
 */
class WatchProgressPauseTest {

    private class FakeApi : WatchProgressApi {
        val saved = mutableListOf<String>()
        var reads = 0

        override suspend fun save(postId: String, body: WatchProgressRequest): ApiEnvelope<WatchProgressDto> {
            saved += "$postId@${body.positionMs}/${body.durationMs}:${body.completed}"
            return ApiEnvelope(WatchProgressDto(body.positionMs, body.durationMs, body.completed))
        }

        override suspend fun get(postId: String): ApiEnvelope<WatchProgressDto> {
            reads++
            return ApiEnvelope(WatchProgressDto(positionMs = 30_000, durationMs = 60_000))
        }
    }

    private class Pause(initial: Boolean) : WatchHistoryPauseStore {
        override val paused = MutableStateFlow(initial)
        override suspend fun setPaused(paused: Boolean) {
            this.paused.value = paused
        }
    }

    private val errors = ErrorMapper(NetworkModule.provideJson())

    @Test
    fun `while recording is on, every report is saved`() = runTest {
        val api = FakeApi()

        WatchProgressRepository(api, errors, Pause(false)).save("p1", 10_000, 60_000, completed = false)

        assertThat(api.saved).containsExactly("p1@10000/60000:false")
    }

    @Test
    fun `while paused, nothing is saved, and resuming saves again`() = runTest {
        val api = FakeApi()
        val pause = Pause(true)
        val repository = WatchProgressRepository(api, errors, pause)

        repository.save("p1", 10_000, 60_000, completed = false)
        assertThat(api.saved).isEmpty()

        pause.setPaused(false)
        repository.save("p1", 20_000, 60_000, completed = false)
        assertThat(api.saved).containsExactly("p1@20000/60000:false")
    }

    @Test
    fun `pausing does not stop the resume read`() = runTest {
        val api = FakeApi()

        val progress = WatchProgressRepository(api, errors, Pause(true)).progress("p1")

        assertThat(api.reads).isEqualTo(1)
        assertThat(progress?.positionMs).isEqualTo(30_000)
    }
}
