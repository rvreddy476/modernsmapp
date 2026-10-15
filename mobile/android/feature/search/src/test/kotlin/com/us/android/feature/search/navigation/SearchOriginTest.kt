package com.us.android.feature.search.navigation

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The tabs each entry point shows (T-16, 2026-10-15), and which is selected on open. */
class SearchOriginTest {

    private val fullPage = listOf(
        SearchScope.TOP,
        SearchScope.PEOPLE,
        SearchScope.VIDEOS,
        SearchScope.REELS,
        SearchScope.SOUNDS,
        SearchScope.HASHTAGS,
    )

    @Test
    fun `Home and Explore show Top, People, Videos, Reels, Sounds, Hashtags - Top first`() {
        assertThat(SearchOrigin.HOME.scopes).containsExactlyElementsIn(fullPage).inOrder()
        assertThat(SearchOrigin.EXPLORE.scopes).containsExactlyElementsIn(fullPage).inOrder()
    }

    @Test
    fun `Reels opens on reels, with people, sounds and tags`() {
        assertThat(SearchOrigin.REELS.scopes)
            .containsExactly(SearchScope.REELS, SearchScope.PEOPLE, SearchScope.SOUNDS, SearchScope.HASHTAGS)
            .inOrder()
    }

    @Test
    fun `the video app looks for channels, people and videos - channels first`() {
        assertThat(SearchOrigin.VIDEO.scopes)
            .containsExactly(SearchScope.CHANNELS, SearchScope.PEOPLE, SearchScope.VIDEOS)
            .inOrder()
    }

    @Test
    fun `the posts search's type is named for reels and videos only`() {
        assertThat(SearchScope.REELS.wireType).isEqualTo("flicks")
        assertThat(SearchScope.VIDEOS.wireType).isEqualTo("videos")
        SearchScope.entries.filterNot { it == SearchScope.REELS || it == SearchScope.VIDEOS }.forEach {
            assertThat(it.wireType).isNull()
        }
    }

    @Test
    fun `only People and Hashtags ask the server to record the search`() {
        assertThat(SearchScope.entries.filter { it.recordsHistory })
            .containsExactly(SearchScope.PEOPLE, SearchScope.HASHTAGS)
    }

    @Test
    fun `an unknown origin on the wire is Explore, never a crash`() {
        assertThat(SearchOrigin.fromWire("VIDEO")).isEqualTo(SearchOrigin.VIDEO)
        assertThat(SearchOrigin.fromWire("nonsense")).isEqualTo(SearchOrigin.EXPLORE)
        assertThat(SearchRoute().origin).isEqualTo(SearchOrigin.EXPLORE.name)
    }
}
