package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class FeedQueryTest {

    /** The default narrows nothing: an absent flag is the whole timeline. */
    @Test
    fun `for you is the plain home surface`() {
        assertThat(FeedQuery.ForYou).isEqualTo(FeedQuery(FeedSurface.Home))
        assertThat(FeedQuery.ForYou.followingOnly).isFalse()
        assertThat(FeedQuery.ForYou.circleOnly).isFalse()
    }

    /** Following and Friends are the SAME surface with one flag each. */
    @Test
    fun `following and friends narrow the home surface`() {
        assertThat(FeedQuery.Following.surface).isEqualTo(FeedSurface.Home)
        assertThat(FeedQuery.Following.followingOnly).isTrue()
        assertThat(FeedQuery.Following.circleOnly).isFalse()

        assertThat(FeedQuery.Friends.surface).isEqualTo(FeedSurface.Home)
        assertThat(FeedQuery.Friends.circleOnly).isTrue()
        assertThat(FeedQuery.Friends.followingOnly).isFalse()
    }

    @Test
    fun `the three home queries are distinct pager keys`() {
        assertThat(listOf(FeedQuery.ForYou, FeedQuery.Following, FeedQuery.Friends)).containsNoDuplicates()
    }

    @Test
    fun `a trending tag always displays with a hash`() {
        assertThat(TrendingHashtag("android", "#android", 3).label).isEqualTo("#android")
        assertThat(TrendingHashtag("android", "android", 3).label).isEqualTo("#android")
        assertThat(TrendingHashtag("android", "", 3).label).isEqualTo("#android")
    }
}
