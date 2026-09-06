package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * What the "+" offers, by where it was pressed (founder, 2026-09-06: "only
 * that plus button should change according to the app we are on").
 */
class CreateScopeTest {

    /** The founder's instruction, exactly: video, reel, live — and nothing else. */
    @Test
    fun `in Tube the plus offers a video, a reel and going live`() {
        assertThat(CreateScope.Tube.surfaces).containsExactly(CreateSurface.Video, CreateSurface.Reel).inOrder()
        assertThat(CreateScope.Tube.offersLive).isTrue()
    }

    @Test
    fun `Tube's plus offers nothing else`() {
        assertThat(CreateScope.Tube.surfaces).doesNotContain(CreateSurface.Text)
        assertThat(CreateScope.Tube.surfaces).doesNotContain(CreateSurface.Photo)
        assertThat(CreateScope.Tube.surfaces).doesNotContain(CreateSurface.Audio)
        assertThat(CreateScope.Tube.surfaces).doesNotContain(CreateSurface.Poll)
        assertThat(CreateScope.Tube.surfaces).doesNotContain(CreateSurface.Article)
    }

    /** Everywhere else keeps today's sheet — see the assumption on [CreateScope]. */
    @Test
    fun `elsewhere the plus is unchanged`() {
        assertThat(CreateScope.App.surfaces).containsExactlyElementsIn(CreateSurface.entries).inOrder()
        assertThat(CreateScope.App.offersLive).isTrue()
    }

    /** Every scope offers something; a plus that opens an empty sheet is a dead control. */
    @Test
    fun `no scope offers nothing`() {
        CreateScope.entries.forEach { scope ->
            assertThat(scope.surfaces.isNotEmpty() || scope.offersLive).isTrue()
        }
    }
}
