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
