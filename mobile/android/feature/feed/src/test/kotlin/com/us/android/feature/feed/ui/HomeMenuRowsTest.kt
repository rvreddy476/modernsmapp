package com.us.android.feature.feed.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Protects Home's More menu (2026-10-02): the rows are in ascending
 * alphabetical order by label (the founder's rule for every menu), each is a
 * screen that exists, and Notifications is not folded into it — the bell
 * stays on the header with the unread count, and so does Messages (founder,
 * 2026-10-19: always visible).
 */
class HomeMenuRowsTest {

    @Test
    fun `the rows are Friends, Hashtags, Live, Settings, in alphabetical order`() {
        // Hashtags (2026-10-24): the followed-tags feed, a section of Home rather than a header tab.
        assertThat(homeMenuRows().map { it.label })
            .containsExactly("Friends", "Hashtags", "Live", "Settings")
            .inOrder()
    }

    @Test
    fun `the order is by label, case-insensitive, whatever order the rows are declared in`() {
        val labels = homeMenuRows().map { it.label }

        assertThat(labels).isEqualTo(labels.sortedWith(String.CASE_INSENSITIVE_ORDER))
        assertThat(homeMenuRows()).containsExactlyElementsIn(HomeMenuRow.entries)
    }

    @Test
    fun `notifications and messages stay on the header, and there is no create row`() {
        val labels = homeMenuRows().map { it.label.lowercase() }

        assertThat(labels).containsNoneOf("notifications", "create", "new post", "messages")
    }
}
