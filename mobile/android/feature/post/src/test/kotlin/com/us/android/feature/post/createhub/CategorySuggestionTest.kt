package com.us.android.feature.post.createhub

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The category a post's own hashtags already name.
 *
 * The feed's topical term reads `cat:<id>` and `tag:<hashtag>` and nothing
 * else, and almost no post carries a category — so the cheapest way to wake
 * the signal up is to stop asking creators a question they have already
 * answered in the field above. The rule has to be exact for the same reason
 * the taxonomy is closed: a wrong category is worse than an absent one,
 * because it relates the post to the wrong neighbours rather than to none.
 */
class CategorySuggestionTest {

    private val taxonomy = FallbackReelCategories

    @Test
    fun `a hashtag that is a category names it`() {
        assertThat(suggestCategory(listOf("food"), taxonomy)).isEqualTo("food")
    }

    /** `#Food` and `#food` are the same word; the stored id is the lowercase one. */
    @Test
    fun `case and stray space do not change the answer`() {
        assertThat(suggestCategory(listOf(" Food "), taxonomy)).isEqualTo("food")
    }

    /** The author's order, not the taxonomy's: the first tag they typed is the one they led with. */
    @Test
    fun `the first matching hashtag wins, in the author's order`() {
        assertThat(suggestCategory(listOf("sunset", "travel", "food"), taxonomy)).isEqualTo("travel")
    }

    /**
     * Nothing is inferred. A synonym table would map `#cover` to music and
     * mis-file a video about phone cases; an absent category costs the ranker
     * nothing, a wrong one costs it accuracy.
     */
    @Test
    fun `a hashtag that only sounds like a category suggests nothing`() {
        assertThat(suggestCategory(listOf("cooking", "funny", "gym"), taxonomy)).isNull()
    }

    @Test
    fun `no hashtags suggests nothing`() {
        assertThat(suggestCategory(emptyList(), taxonomy)).isNull()
    }

    /**
     * `cat:other` would relate a cooking video to a car review. It stays
     * pickable for an author who means it and is never proposed.
     */
    @Test
    fun `other is never suggested`() {
        assertThat(suggestCategory(listOf(CATEGORY_OTHER), taxonomy)).isNull()
    }

    /**
     * The taxonomy is the server's when it has loaded, so a category added
     * server-side starts being suggested with no app release.
     */
    @Test
    fun `the suggestion follows the server's list, not a baked-in one`() {
        val server = listOf(ReelCategory("skits", "Skits"))
        assertThat(suggestCategory(listOf("skits"), server)).isEqualTo("skits")
        // And a category the server has dropped stops being suggested.
        assertThat(suggestCategory(listOf("comedy"), server)).isNull()
    }
}
