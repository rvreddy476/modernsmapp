package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.hidden.HiddenTokenSnapshot
import com.us.android.feature.chat.ui.hidden.GuardVerdict
import com.us.android.feature.chat.ui.hidden.HiddenChatContext
import com.us.android.feature.chat.ui.hidden.guardVerdict
import org.junit.Test

/**
 * The hidden-chat guard's rule: the hidden list composes only while
 * revealed, a hidden chat only while opened; every other chat passes through.
 */
class HiddenChatGuardTest {

    private val none = HiddenTokenSnapshot()
    private val revealed = HiddenTokenSnapshot(revealed = true)
    private val opened = HiddenTokenSnapshot(revealed = true, opened = true)

    @Test
    fun `a normal chat is not the guard's business`() {
        assertThat(guardVerdict("c1", requireReveal = false, lockedIds = setOf("h1"), snapshot = none))
            .isEqualTo(GuardVerdict.NotHidden)
        assertThat(guardVerdict(null, requireReveal = false, lockedIds = setOf("h1"), snapshot = none))
            .isEqualTo(GuardVerdict.NotHidden)
    }

    @Test
    fun `a hidden chat shows only while the chats are open`() {
        assertThat(guardVerdict("h1", false, setOf("h1"), opened)).isEqualTo(GuardVerdict.Show)
        assertThat(guardVerdict("h1", false, setOf("h1"), revealed)).isEqualTo(GuardVerdict.Relock)
        assertThat(guardVerdict("h1", false, setOf("h1"), none)).isEqualTo(GuardVerdict.Relock)
    }

    @Test
    fun `the hidden list shows only while revealed`() {
        assertThat(guardVerdict(null, requireReveal = true, lockedIds = emptySet(), snapshot = revealed)).isEqualTo(GuardVerdict.Show)
        assertThat(guardVerdict(null, requireReveal = true, lockedIds = emptySet(), snapshot = none)).isEqualTo(GuardVerdict.Relock)
    }

    @Test
    fun `a normal chat's media URLs are untouched, a hidden chat's carry chat_open`() {
        val url = "https://api.example.com/v1/media/m1/serve"
        assertThat(HiddenChatContext.Normal.mediaUrl(url)).isEqualTo(url)
        val hidden = HiddenChatContext(hidden = true, openToken = { "op" }, touch = {})
        assertThat(hidden.mediaUrl(url)).isEqualTo("$url?chat_open=op")
        // Re-locked mid-flight: no token, no parameter.
        val relocked = HiddenChatContext(hidden = true, openToken = { null }, touch = {})
        assertThat(relocked.mediaUrl(url)).isEqualTo(url)
    }
}
