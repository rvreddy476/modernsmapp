package com.us.android.core.designsystem.component

import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.icon.UsIcons
import org.junit.Test

/**
 * Protects the top-right corner of every section header (founder,
 * 2026-10-02): Search, then More at the corner, the three dots rather than
 * the hamburger, and no create button up there.
 */
class UsHeaderCornerTest {

    @Test
    fun `the corner is Search, then More at the corner`() {
        assertThat(UsHeaderCorner)
            .containsExactly(UsHeaderCornerAction.SEARCH, UsHeaderCornerAction.MORE)
            .inOrder()
        assertThat(UsHeaderCorner.last()).isEqualTo(UsHeaderCornerAction.MORE)
    }

    @Test
    fun `More is the three dots and Search the magnifier`() {
        assertThat(UsHeaderCornerAction.MORE.icon).isSameInstanceAs(UsIcons.More)
        assertThat(UsHeaderCornerAction.MORE.icon).isNotSameInstanceAs(UsIcons.Menu)
        assertThat(UsHeaderCornerAction.SEARCH.icon).isSameInstanceAs(UsIcons.Search)
    }

    @Test
    fun `there is no create button in the header - Create lives in the bottom bar`() {
        assertThat(UsHeaderCornerAction.entries.map { it.icon }).doesNotContain(UsIcons.Create)
        assertThat(UsHeaderCornerAction.entries.map { it.description.lowercase() }).containsNoneOf("create", "new")
    }

    @Test
    fun `each glyph says what it is to a screen reader`() {
        assertThat(UsHeaderCornerAction.SEARCH.description).isEqualTo("Search")
        assertThat(UsHeaderCornerAction.MORE.description).isEqualTo("More")
    }
}
