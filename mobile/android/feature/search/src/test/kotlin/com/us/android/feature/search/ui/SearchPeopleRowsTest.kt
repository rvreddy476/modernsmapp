package com.us.android.feature.search.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.FollowStatus
import com.us.android.feature.search.data.SearchHit
import com.us.android.feature.search.navigation.SearchScope
import org.junit.Test

/**
 * People rows (2026-10-17): the viewer is never among the results, and each
 * person's row ends in Follow, Following or Requested — the button says
 * what is true rather than disappearing.
 */
class SearchPeopleRowsTest {

    private fun user(id: String) = SearchHit.User(id = id, username = id, displayName = id, avatarUrl = null)

    private val request = SearchRequest("clee", SearchScope.PEOPLE)

    @Test
    fun `the signed-in viewer is dropped from the results`() {
        val loaded = SearchResults.Loaded(request, listOf(user("me"), user("ana"), SearchHit.Hashtag("me")))

        val shown = SearchQueries.withoutSelf(loaded, ownUserId = "me") as SearchResults.Loaded

        assertThat(shown.hits.filterIsInstance<SearchHit.User>().map { it.id }).containsExactly("ana")
        assertThat(shown.hits).hasSize(2)
    }

    @Test
    fun `nothing is dropped before the viewer is known, or from a page that is not loaded`() {
        val loaded = SearchResults.Loaded(request, listOf(user("me")))

        assertThat(SearchQueries.withoutSelf(loaded, ownUserId = "")).isEqualTo(loaded)
        assertThat(SearchQueries.withoutSelf(SearchResults.Loading, ownUserId = "me")).isEqualTo(SearchResults.Loading)
    }

    @Test
    fun `the row's button follows the edge`() {
        assertThat(SearchQueries.followButton("me", "ana", FollowStatus.NONE)).isEqualTo(UserFollowButton.FOLLOW)
        assertThat(SearchQueries.followButton("me", "ana", null)).isEqualTo(UserFollowButton.FOLLOW)
        assertThat(SearchQueries.followButton("me", "ana", FollowStatus.FOLLOWING)).isEqualTo(UserFollowButton.FOLLOWING)
        assertThat(SearchQueries.followButton("me", "ana", FollowStatus.REQUESTED)).isEqualTo(UserFollowButton.REQUESTED)
        assertThat(SearchQueries.followButton("me", "me", FollowStatus.NONE)).isEqualTo(UserFollowButton.HIDDEN)
        assertThat(SearchQueries.followButton("me", "", null)).isEqualTo(UserFollowButton.HIDDEN)
    }

    @Test
    fun `the empty box's second list is called Trending searches`() {
        assertThat(TRENDING_SEARCHES).isEqualTo("Trending searches")
    }
}
