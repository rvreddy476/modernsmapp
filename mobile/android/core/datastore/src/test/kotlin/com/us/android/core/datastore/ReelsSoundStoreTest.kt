package com.us.android.core.datastore

import com.google.common.truth.Truth.assertThat
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

/**
 * The viewer's choice of sound in Reels, on the real preferences file.
 *
 * founder, 2026-09-30: reels open MUTED, and once the viewer turns the sound
 * on it stays on across restarts until they mute again. What this protects:
 * the default being muted for someone who never chose — a fresh install must
 * not start loud — and the choice being what a LATER reader of the file
 * finds, in both directions.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ReelsSoundStoreTest {

    private fun settings() = SettingsDataStore(RuntimeEnvironment.getApplication())

    private fun store() = DataStoreReelsSoundStore(settings())

    @Before
    fun setUp() = runTest { settings().clear() }

    @Test
    fun `a viewer who never chose has the sound off`() = runTest {
        assertThat(store().soundOn.first()).isFalse()
        assertThat(settings().reelsSoundOn.first()).isFalse()
    }

    @Test
    fun `turning the sound on is kept, and a later reader finds it on`() = runTest {
        store().setSoundOn(true)

        assertThat(store().soundOn.first()).isTrue()
        assertThat(settings().reelsSoundOn.first()).isTrue()
    }

    @Test
    fun `muting again is kept too`() = runTest {
        store().setSoundOn(true)
        store().setSoundOn(false)

        assertThat(store().soundOn.first()).isFalse()
    }

    /** Its own key: the choice neither reads nor disturbs another setting. */
    @Test
    fun `the choice is its own setting`() = runTest {
        settings().setAutoplayNextEpisode(false)
        settings().setDataSaverEnabled(true)

        store().setSoundOn(true)

        assertThat(settings().autoplayNextEpisode.first()).isFalse()
        assertThat(settings().dataSaverEnabled.first()).isTrue()
        assertThat(store().soundOn.first()).isTrue()
    }
}
