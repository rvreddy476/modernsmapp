package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The Me tab's More menu (founder, 2026-10-02): the header's corner is
 * Search, the bell, More on every page but Home, so Messages left the bar
 * for this menu, beside Settings.
 *
 * What this protects: the rows being real destinations only, in ascending
 * alphabetical order (the rule for every menu), and Messages still being
 * reachable from the Me tab after its glyph went.
 */
class MeMenuRowsTest {

    @Test
    fun `the menu is Messages and Settings, in alphabetical order`() {
        assertThat(meMenuRows(hasSettings = true))
            .containsExactly(MeMenuRow.MESSAGES, MeMenuRow.SETTINGS)
            .inOrder()
        assertThat(meMenuRows(hasSettings = true).map { it.label })
            .isInOrder(String.CASE_INSENSITIVE_ORDER)
    }

    @Test
    fun `Settings is not offered when the page has nowhere to send it`() {
        assertThat(meMenuRows(hasSettings = false)).containsExactly(MeMenuRow.MESSAGES)
    }

    @Test
    fun `every row says what it opens`() {
        assertThat(MeMenuRow.entries.map { it.label }).containsExactly("Messages", "Settings")
    }
}
