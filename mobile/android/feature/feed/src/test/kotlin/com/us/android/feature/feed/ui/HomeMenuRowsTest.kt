package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Protects Home's More menu (2026-10-02): the rows are in ascending
 * alphabetical order by label (the founder's rule for every menu), each is a
 * screen that exists, and Messages and Notifications are not folded into it —
 * they stay on the bar, with the unread count.
 */
class HomeMenuRowsTest {

    @Test
    fun `the rows are Friends, Live, Settings, in alphabetical order`() {
        assertThat(homeMenuRows().map { it.label }).containsExactly("Friends", "Live", "Settings").inOrder()
    }

    @Test
    fun `the order is by label, case-insensitive, whatever order the rows are declared in`() {
        val labels = homeMenuRows().map { it.label }

        assertThat(labels).isEqualTo(labels.sortedWith(String.CASE_INSENSITIVE_ORDER))
        assertThat(homeMenuRows()).containsExactlyElementsIn(HomeMenuRow.entries)
    }

    @Test
    fun `messages and notifications stay on the bar, and there is no create row`() {
        val labels = homeMenuRows().map { it.label.lowercase() }

        assertThat(labels).containsNoneOf("messages", "notifications", "create", "new post")
    }
}
