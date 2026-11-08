package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.Conversation
import org.junit.Test

/** Dating audit M3: a Pulse match's chat holds the secure window; no other chat does. */
class DatingChatProtectionTest {

    private fun conversation(sourceApp: String?) = Conversation(
        id = "c",
        type = "direct",
        title = null,
        isRequest = false,
        members = emptyList(),
        updatedAt = "",
        sourceApp = sourceApp,
    )

    @Test
    fun `a Pulse match's chat is protected`() {
        assertThat(protectsThread(conversation("dating"))).isTrue()
        assertThat(protectsThread(conversation(" Dating "))).isTrue()
    }

    @Test
    fun `an ordinary chat, another product's chat and a thread still loading are not`() {
        assertThat(protectsThread(conversation(null))).isFalse()
        assertThat(protectsThread(conversation("doorstep"))).isFalse()
        assertThat(protectsThread(null)).isFalse()
    }
}
