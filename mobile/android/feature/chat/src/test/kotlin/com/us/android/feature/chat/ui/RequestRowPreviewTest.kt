package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.Conversation
import org.junit.Test

/** DM consent (2026-10-20): a request row shows the requester's one message, not a fixed label. */
class RequestRowPreviewTest {
    private fun request(preview: String) = Conversation(
        id = "c1", type = "direct", title = null, isRequest = true, members = emptyList(),
        updatedAt = "2026-10-20T00:00:00Z", lastMessagePreview = preview,
    )

    @Test
    fun `a request row shows its one message`() {
        assertThat(requestRowPreview(request("Hi! I'm a test account."))).isEqualTo("Hi! I'm a test account.")
    }

    @Test
    fun `a request with no text yet says Message request`() {
        assertThat(requestRowPreview(request(""))).isEqualTo("Message request")
    }
}
