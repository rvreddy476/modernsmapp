package com.us.android.core.media

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * A hidden chat's media (24 Oct 2026) never enters the media cache: its URL
 * carries the chat's open token as `chat_open`, and a cached entry would keep
 * the bytes and that URL on disk after the chat re-locks.
 */
class HiddenChatMediaCacheTest {

    @Test
    fun `a chat_open URL is recognised wherever the parameter sits`() {
        assertThat(carriesChatOpen("chat_open=tok")).isTrue()
        assertThat(carriesChatOpen("w=320&chat_open=tok")).isTrue()
        assertThat(carriesChatOpen("chat_open=")).isTrue()
    }

    @Test
    fun `everything else stays cacheable`() {
        assertThat(carriesChatOpen(null)).isFalse()
        assertThat(carriesChatOpen("")).isFalse()
        assertThat(carriesChatOpen("w=320")).isFalse()
        assertThat(carriesChatOpen("x_chat_open=1")).isFalse()
        assertThat(carriesChatOpen("chat_opener=1")).isFalse()
    }
}
