package com.us.android.feature.search.data

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** The recent list's rules: newest first, one per query, ten at most, and a round trip through the store's string. */
class RecentSearchListTest {

    @Test
    fun `a new search goes on top`() {
        assertThat(RecentSearchList.push(listOf("cats"), "dogs")).containsExactly("dogs", "cats").inOrder()
    }

    @Test
    fun `a repeat moves up instead of doubling, whatever its case`() {
        assertThat(RecentSearchList.push(listOf("dogs", "Cats", "birds"), "cats"))
            .containsExactly("cats", "dogs", "birds")
            .inOrder()
    }

    @Test
    fun `blank is not a search and whitespace is trimmed`() {
        assertThat(RecentSearchList.push(listOf("cats"), "   ")).containsExactly("cats")
        assertThat(RecentSearchList.push(emptyList(), "  dogs ")).containsExactly("dogs")
    }

    @Test
    fun `the list keeps the last ten`() {
        var list = emptyList<String>()
        repeat(12) { list = RecentSearchList.push(list, "q$it") }

        assertThat(list).hasSize(RecentSearchList.MAX)
        assertThat(list.first()).isEqualTo("q11")
        assertThat(list.last()).isEqualTo("q2")
    }

    @Test
    fun `the store's string round-trips and tolerates nothing stored`() {
        val list = listOf("dogs", "cats and hats", "q3")

        assertThat(RecentSearchList.decode(RecentSearchList.encode(list))).isEqualTo(list)
        assertThat(RecentSearchList.decode(null)).isEmpty()
        assertThat(RecentSearchList.decode("")).isEmpty()
    }
}
