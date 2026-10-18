package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Chat parity (CONTRACT §8): a chat push rides its conversation's alert-tone channel. */
class AlertToneChannelTest {

    @Test
    fun `a chat push picks its tone's channel`() {
        assertThat(NotificationChannelSpec.forPush("dm", "chime")).isEqualTo(NotificationChannelSpec.MESSAGES_CHIME)
        assertThat(NotificationChannelSpec.forPush("message", "pop")).isEqualTo(NotificationChannelSpec.MESSAGES_POP)
        assertThat(NotificationChannelSpec.forPush("message_request", "BELL")).isEqualTo(NotificationChannelSpec.MESSAGES_BELL)
        assertThat(NotificationChannelSpec.forPush("chat", "silent")).isEqualTo(NotificationChannelSpec.MESSAGES_SILENT)
    }

    @Test
    fun `default, unknown or absent tones keep the Messages channel`() {
        assertThat(NotificationChannelSpec.forPush("dm", "default")).isEqualTo(NotificationChannelSpec.MESSAGES)
        assertThat(NotificationChannelSpec.forPush("dm", "airhorn")).isEqualTo(NotificationChannelSpec.MESSAGES)
        assertThat(NotificationChannelSpec.forPush("dm", null)).isEqualTo(NotificationChannelSpec.MESSAGES)
    }

    @Test
    fun `a tone never moves a push that is not a chat`() {
        assertThat(NotificationChannelSpec.forPush("incoming_call", "silent")).isEqualTo(NotificationChannelSpec.CALLS)
        assertThat(NotificationChannelSpec.forPush("dating.match.new_message", "chime")).isEqualTo(NotificationChannelSpec.DATING)
        assertThat(NotificationChannelSpec.forPush("like", "pop")).isEqualTo(NotificationChannelSpec.SOCIAL)
    }

    @Test
    fun `the server-named channel wins, but only among the Messages channels`() {
        assertThat(NotificationChannelSpec.forPush("dm", "default", "messages_bell")).isEqualTo(NotificationChannelSpec.MESSAGES_BELL)
        assertThat(NotificationChannelSpec.forPush("dm", "chime", "messages_silent")).isEqualTo(NotificationChannelSpec.MESSAGES_SILENT)
        assertThat(NotificationChannelSpec.forPush("message", null, "messages")).isEqualTo(NotificationChannelSpec.MESSAGES)
        // Unknown or foreign ids fall back to the tone; a non-chat push never moves.
        assertThat(NotificationChannelSpec.forPush("dm", "pop", "messages_v2")).isEqualTo(NotificationChannelSpec.MESSAGES_POP)
        assertThat(NotificationChannelSpec.forPush("dm", null, "calls")).isEqualTo(NotificationChannelSpec.MESSAGES)
        assertThat(NotificationChannelSpec.forPush("like", null, "messages_chime")).isEqualTo(NotificationChannelSpec.SOCIAL)
    }

    @Test
    fun `the tone channels keep the exact wire ids and carry their own bundled sounds`() {
        assertThat(NotificationChannelSpec.MESSAGE_TONE_CHANNELS.map { it.id })
            .containsExactly("messages", "messages_chime", "messages_pop", "messages_bell", "messages_silent").inOrder()
        assertThat(NotificationChannelSpec.MESSAGES_CHIME.soundRes).isEqualTo(R.raw.msg_tone_chime)
        assertThat(NotificationChannelSpec.MESSAGES_POP.soundRes).isEqualTo(R.raw.msg_tone_pop)
        assertThat(NotificationChannelSpec.MESSAGES_BELL.soundRes).isEqualTo(R.raw.msg_tone_bell)
        // Default keeps the device sound; silent has none and stays LOW.
        assertThat(NotificationChannelSpec.MESSAGES.soundRes).isNull()
        assertThat(NotificationChannelSpec.MESSAGES_SILENT.soundRes).isNull()
        assertThat(NotificationChannelSpec.MESSAGES_SILENT.importance).isEqualTo(android.app.NotificationManager.IMPORTANCE_LOW)
    }

    @Test
    fun `the silent channel is quiet and the tones vibrate their own way`() {
        assertThat(NotificationChannelSpec.MESSAGES_SILENT.silent).isTrue()
        val patterns = listOf(
            NotificationChannelSpec.MESSAGES_CHIME,
            NotificationChannelSpec.MESSAGES_POP,
            NotificationChannelSpec.MESSAGES_BELL,
        ).map { it.vibration!!.toList() }
        assertThat(patterns.toSet()).hasSize(3)
    }
}
