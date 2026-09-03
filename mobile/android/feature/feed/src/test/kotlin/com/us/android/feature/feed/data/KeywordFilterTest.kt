package com.us.android.feature.feed.data

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class KeywordFilterTest {

    @Test
    fun `matches whole words case-insensitively and ignores hashtags`() {
        assertThat(KeywordFilter.matches("Look at these CATS today", listOf("cats"))).isTrue()
        assertThat(KeywordFilter.matches("#cats forever", listOf("cats"))).isTrue()
        assertThat(KeywordFilter.matches("cats forever", listOf("#Cats"))).isTrue()
    }

    @Test
    fun `does not match a substring of a longer word`() {
        assertThat(KeywordFilter.matches("concatenate the lists", listOf("cat"))).isFalse()
    }

    @Test
    fun `a multi-word keyword matches as a phrase`() {
        assertThat(KeywordFilter.matches("the season finale spoiler is out", listOf("season finale"))).isTrue()
        assertThat(KeywordFilter.matches("finale of the season", listOf("season finale"))).isFalse()
    }

    @Test
    fun `an empty list or blank text never matches`() {
        assertThat(KeywordFilter.matches("anything", emptyList())).isFalse()
        assertThat(KeywordFilter.matches("", listOf("anything"))).isFalse()
        assertThat(KeywordFilter.matches("anything", listOf(""))).isFalse()
    }
}
