package com.us.android.core.media

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * When a pooled player's re-prepare keeps the playhead (2026-10-21): the
 * same reel with a new source (an audio track picked from More) does; a
 * different reel landing on the index does not, nor anything unnamed.
 */
class PlayerPoolPlaceTest {

    @Test
    fun `the same video with a new source keeps its place`() {
        assertThat(keepsPlace(before = "p1", after = "p1")).isTrue()
    }

    @Test
    fun `another video at the same index starts from the top`() {
        assertThat(keepsPlace(before = "p1", after = "p2")).isFalse()
    }

    @Test
    fun `an unnamed prepare never carries a place`() {
        assertThat(keepsPlace(before = null, after = "p1")).isFalse()
        assertThat(keepsPlace(before = "p1", after = null)).isFalse()
        assertThat(keepsPlace(before = null, after = null)).isFalse()
    }
}
