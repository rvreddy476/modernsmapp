package com.us.android.feature.chat.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatParityRepository
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.ReelsSoundStore
import com.us.android.core.media.MediaSources
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.PlayerFactory
import com.us.android.core.testing.MainDispatcherRule
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.filterNotNull
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The chat's shared-reels viewer (audit 2026-10-30, M4): it plays with the
 * viewer's Reels choice of sound — held until that choice is read, so a
 * reel never starts silent and flips loud — the speaker keeps a change for
 * Reels too, and a tap holds the reel paused until the pager moves on.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SharedReelsViewModelTest {

    @get:Rule
    val main = MainDispatcherRule(UnconfinedTestDispatcher())

    private class SoundStore(initial: Boolean?) : ReelsSoundStore {
        val stored = MutableStateFlow(initial)
        val writes = mutableListOf<Boolean>()
        override val soundOn: Flow<Boolean> get() = stored.filterNotNull()
        override suspend fun setSoundOn(on: Boolean) {
            writes += on
            stored.value = on
        }
    }

    private fun viewModel(sound: SoundStore): SharedReelsViewModel {
        val parity = mockk<ChatParityRepository>()
        coEvery { parity.shared(any(), any(), any()) } returns AppResult.Failure(AppError.NoNetwork())
        return SharedReelsViewModel(
            parity = parity,
            posts = PostCardSource { AppResult.Failure(AppError.NoNetwork()) },
            soundStore = sound,
            media = SharedReelMedia(
                urls = mockk<MediaUrlResolver>(relaxed = true),
                playerFactory = mockk<PlayerFactory>(relaxed = true),
                mediaSources = mockk<MediaSources>(relaxed = true),
            ),
            savedStateHandle = SavedStateHandle(mapOf("conversationId" to "c1", "postId" to "p1")),
        )
    }

    @Test
    fun `nothing plays until the choice of sound is read`() = runTest {
        val sound = SoundStore(initial = null)
        val vm = viewModel(sound)
        assertThat(vm.state.value.mayPlay).isFalse()
        assertThat(vm.state.value.muted).isTrue()

        sound.stored.value = true

        assertThat(vm.state.value.mayPlay).isTrue()
        assertThat(vm.state.value.muted).isFalse()
    }

    @Test
    fun `a viewer who never turned sound on watches muted`() = runTest {
        val vm = viewModel(SoundStore(initial = false))

        assertThat(vm.state.value.muted).isTrue()
        assertThat(vm.state.value.mayPlay).isTrue()
        assertThat(sharedReelVolume(vm.state.value.muted)).isEqualTo(0f)
    }

    @Test
    fun `the speaker turns sound on, here and for Reels`() = runTest {
        val sound = SoundStore(initial = false)
        val vm = viewModel(sound)

        vm.toggleMuted()

        assertThat(vm.state.value.muted).isFalse()
        assertThat(sharedReelVolume(vm.state.value.muted)).isEqualTo(1f)
        assertThat(sound.writes).containsExactly(true)
    }

    @Test
    fun `a tap that beat the read is the choice that stands`() = runTest {
        val sound = SoundStore(initial = null)
        val vm = viewModel(sound)

        vm.toggleMuted() // sound on, before the store answered
        sound.stored.value = false

        assertThat(vm.state.value.muted).isFalse()
    }

    @Test
    fun `a tap holds the reel, and moving to another reel lets it go`() = runTest {
        val vm = viewModel(SoundStore(initial = true))

        vm.togglePaused()
        assertThat(vm.state.value.mayPlay).isFalse()

        vm.onSettled(1)
        assertThat(vm.state.value.paused).isFalse()
        assertThat(vm.state.value.mayPlay).isTrue()
    }
}
