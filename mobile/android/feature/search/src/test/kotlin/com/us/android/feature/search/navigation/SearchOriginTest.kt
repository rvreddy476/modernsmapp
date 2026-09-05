package com.us.android.feature.search.navigation

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The scope chips each entry point shows (founder, 2026-09-05), and which is selected on open. */
class SearchOriginTest {

    @Test
    fun `Home looks for people and posts`() {
        assertThat(SearchOrigin.HOME.scopes).containsExactly(SearchScope.USERS, SearchScope.POSTS).inOrder()
    }

    @Test
    fun `Reels looks for people and reels`() {
        assertThat(SearchOrigin.REELS.scopes).containsExactly(SearchScope.USERS, SearchScope.REELS).inOrder()
    }

    @Test
    fun `the video app looks for channels, people and videos - channels first`() {
        assertThat(SearchOrigin.VIDEO.scopes)
            .containsExactly(SearchScope.CHANNELS, SearchScope.USERS, SearchScope.VIDEOS)
            .inOrder()
    }

    @Test
    fun `Explore looks everywhere`() {
        assertThat(SearchOrigin.EXPLORE.scopes).containsExactlyElementsIn(SearchScope.entries)
        assertThat(SearchOrigin.EXPLORE.scopes).hasSize(SearchScope.entries.size)
    }

    @Test
    fun `the posts search's type is omitted for posts and named for reels and videos`() {
        assertThat(SearchScope.POSTS.wireType).isNull()
        assertThat(SearchScope.REELS.wireType).isEqualTo("flicks")
        assertThat(SearchScope.VIDEOS.wireType).isEqualTo("videos")
        assertThat(SearchScope.USERS.wireType).isNull()
        assertThat(SearchScope.CHANNELS.wireType).isNull()
    }

    @Test
    fun `an unknown origin on the wire is Explore, never a crash`() {
        assertThat(SearchOrigin.fromWire("VIDEO")).isEqualTo(SearchOrigin.VIDEO)
        assertThat(SearchOrigin.fromWire("nonsense")).isEqualTo(SearchOrigin.EXPLORE)
        assertThat(SearchRoute().origin).isEqualTo(SearchOrigin.EXPLORE.name)
    }
}
