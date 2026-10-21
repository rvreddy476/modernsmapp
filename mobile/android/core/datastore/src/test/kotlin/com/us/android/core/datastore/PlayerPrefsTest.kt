package com.us.android.core.datastore

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.AppearanceMode
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import org.robolectric.annotation.Config

/**
 * Reels' Auto scroll and the Appearance setting (T-32), on the real
 * preferences file: what a fresh install reads, and that a choice is what a
 * LATER reader of the file finds — the restart.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class PlayerPrefsTest {

    private fun settings() = SettingsDataStore(RuntimeEnvironment.getApplication())

    @Before
    fun setUp() = runTest { settings().clear() }

    @Test
    fun `auto scroll is off for a viewer who never chose`() = runTest {
        assertThat(DataStoreReelsAutoScrollStore(settings()).autoScroll.first()).isFalse()
    }

    @Test
    fun `turning auto scroll on is kept, and off again too`() = runTest {
        DataStoreReelsAutoScrollStore(settings()).setAutoScroll(true)
        assertThat(DataStoreReelsAutoScrollStore(settings()).autoScroll.first()).isTrue()

        DataStoreReelsAutoScrollStore(settings()).setAutoScroll(false)
        assertThat(DataStoreReelsAutoScrollStore(settings()).autoScroll.first()).isFalse()
    }

    /** Its own key: it neither reads nor disturbs the reels sound choice (muted start stays the default). */
    @Test
    fun `auto scroll leaves the reels sound choice alone`() = runTest {
        DataStoreReelsAutoScrollStore(settings()).setAutoScroll(true)

        assertThat(settings().reelsSoundOn.first()).isFalse()
    }

    @Test
    fun `appearance is System for a viewer who never chose`() = runTest {
        assertThat(DataStoreAppearanceStore(settings()).mode.first()).isEqualTo(AppearanceMode.SYSTEM)
    }

    @Test
    fun `each appearance choice is what a later reader finds`() = runTest {
        AppearanceMode.entries.forEach { mode ->
            DataStoreAppearanceStore(settings()).setMode(mode)
            assertThat(DataStoreAppearanceStore(settings()).mode.first()).isEqualTo(mode)
        }
    }

    @Test
    fun `the audio language is the original for a viewer who never chose`() = runTest {
        assertThat(DataStoreAudioLanguageStore(settings()).language.first()).isNull()
    }

    @Test
    fun `an audio language is kept, and the original again too`() = runTest {
        DataStoreAudioLanguageStore(settings()).setLanguage("hi")
        assertThat(DataStoreAudioLanguageStore(settings()).language.first()).isEqualTo("hi")

        DataStoreAudioLanguageStore(settings()).setLanguage(null)
        assertThat(DataStoreAudioLanguageStore(settings()).language.first()).isNull()
    }
}
