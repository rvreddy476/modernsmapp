package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.network.ErrorMapper
import kotlinx.serialization.json.Json
import org.junit.Test

/** Chat parity: what realtime reactions and unsends do to an open thread. */
class ThreadParityTest {

    private val api = java.lang.reflect.Proxy.newProxyInstance(
        ChatApi::class.java.classLoader,
        arrayOf(ChatApi::class.java),
    ) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi

    private fun controller(vararg messages: Message): ThreadController {
        val thread = ThreadController("c1", ChatRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })), "me")
        messages.reversed().forEach { thread.onRealtimeMessage(it) }
        return thread
    }

    private fun msg(id: String, text: String = "hi") = Message(id, "c1", "u2", null, text, "2026-10-18T10:00:00Z")

    // ── a send's echo and its confirmed copy are one bubble ──────────────

    private fun echo(id: String) = Message(id, "c1", "me", null, "sent", "2026-10-18T10:00:00Z")

    private fun confirmed(id: String) = echo(id).copy(bucket = "202610", ts = "2026-10-18T10:00:00.123Z")

    @Test
    fun `the confirmed copy gives an echoed row its address in place`() {
        val thread = controller(msg("m0"))
        thread.onRealtimeMessage(echo("s1"))
        assertThat(thread.snapshot().messages.first { it.id == "s1" }.addressable).isFalse()

        val next = thread.onRealtimeMessage(confirmed("s1"))
        val rows = next!!.messages.filter { it.id == "s1" }
        assertThat(rows).hasSize(1)
        assertThat(rows.single().bucket).isEqualTo("202610")
        assertThat(rows.single().ts).isEqualTo("2026-10-18T10:00:00.123Z")
        // Same place in the thread: still the newest, above m0.
        assertThat(next.messages.map { it.id }).containsExactly("s1", "m0").inOrder()
    }

    @Test
    fun `an echo after the confirmed copy changes nothing`() {
        val thread = controller()
        thread.onRealtimeMessage(confirmed("s1"))
        assertThat(thread.onRealtimeMessage(echo("s1"))).isNull()
        assertThat(thread.snapshot().messages.single().addressable).isTrue()
    }

    @Test
    fun `a reaction frame applies once and its echo is a no-op`() {
        val thread = controller(msg("m1"))
        val frame = ChatSocketEvent.ReactionChanged("c1", "m1", "u2", "❤️", added = true)
        val first = thread.onSocketEvent(frame)
        assertThat(first?.messages?.single()?.reactions?.single()?.userIds).containsExactly("u2")
        // The server echoes the reactor's own frame to their other devices.
        assertThat(thread.onSocketEvent(frame)).isNull()
        val removed = thread.onSocketEvent(frame.copy(added = false))
        assertThat(removed?.messages?.single()?.reactions).isEmpty()
    }

    @Test
    fun `a reaction for another conversation or an unknown message changes nothing`() {
        val thread = controller(msg("m1"))
        assertThat(thread.onSocketEvent(ChatSocketEvent.ReactionChanged("other", "m1", "u2", "❤️", true))).isNull()
        assertThat(thread.onSocketEvent(ChatSocketEvent.ReactionChanged("c1", "m9", "u2", "❤️", true))).isNull()
    }

    @Test
    fun `an unsend leaves a tombstone without the old content`() {
        val thread = controller(msg("m1", text = "secret"), msg("m2"))
        val next = thread.onSocketEvent(ChatSocketEvent.MessageDeleted("c1", "m1"))!!
        val tomb = next.messages.first { it.id == "m1" }
        assertThat(tomb.deleted).isTrue()
        assertThat(tomb.text).isEmpty()
        assertThat(next.messages).hasSize(2)
        // Twice is once; another conversation's unsend is not ours.
        assertThat(thread.onSocketEvent(ChatSocketEvent.MessageDeleted("c1", "m1"))).isNull()
        assertThat(thread.onSocketEvent(ChatSocketEvent.MessageDeleted("other", "m2"))).isNull()
    }

    @Test
    fun `an explicit typing stop clears the indicator`() {
        val thread = controller(msg("m1"))
        thread.onSocketEvent(ChatSocketEvent.Typing("c1", "u2", isTyping = true))
        val stopped = thread.onSocketEvent(ChatSocketEvent.Typing("c1", "u2", isTyping = false))
        assertThat(stopped?.typingUserIds).isEmpty()
    }

    @Test
    fun `a confirmed edit replaces the text and marks it edited`() {
        val thread = controller(msg("m1", text = "helo"))
        val next = thread.replaceText("m1", "hello")!!
        assertThat(next.messages.single().text).isEqualTo("hello")
        assertThat(next.messages.single().edited).isTrue()
        assertThat(thread.replaceText("nope", "x")).isNull()
    }
}
