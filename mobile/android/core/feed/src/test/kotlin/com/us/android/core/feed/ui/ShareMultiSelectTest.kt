package com.us.android.core.feed.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.ui.share.MAX_SHARE_TARGETS
import com.us.android.core.feed.ui.share.toggledTarget
import org.junit.Test

/** Chat parity (CONTRACT §9): the share sheet picks at most five chats, groups included. */
class ShareMultiSelectTest {

    @Test
    fun `taps add in order and a second tap removes`() {
        val picked = emptyList<String>().toggledTarget("a").toggledTarget("g1").toggledTarget("b")
        assertThat(picked).containsExactly("a", "g1", "b").inOrder()
        assertThat(picked.toggledTarget("g1")).containsExactly("a", "b").inOrder()
    }

    @Test
    fun `a sixth pick is refused, not swapped in`() {
        val five = (1..MAX_SHARE_TARGETS).fold(emptyList<String>()) { acc, i -> acc.toggledTarget("c$i") }
        assertThat(five).hasSize(5)
        assertThat(five.toggledTarget("c6")).isEqualTo(five)
        // Unpicking still works at the cap.
        assertThat(five.toggledTarget("c3")).hasSize(4)
    }
}
