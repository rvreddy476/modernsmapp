package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ReactionSummary
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.applyToggle
import org.junit.Test

/** Reaction-summary arithmetic and receipt state — completion-pass surface. */
class ReactionAndReceiptTest {

    // ── applyToggle ─────────────────────────────────────────────────────

    @Test
    fun `adding creates or joins a summary and removing empties it away`() {
        val none = emptyList<ReactionSummary>()

        val created = none.applyToggle("❤️", "u1", added = true)
        assertThat(created).containsExactly(ReactionSummary("❤️", listOf("u1")))

        val joined = created.applyToggle("❤️", "u2", added = true)
        assertThat(joined.single().userIds).containsExactly("u1", "u2")

        val left = joined.applyToggle("❤️", "u1", added = false)
        assertThat(left.single().userIds).containsExactly("u2")

        val gone = left.applyToggle("❤️", "u2", added = false)
        assertThat(gone).isEmpty()
    }

    @Test
    fun `toggles are idempotent against replayed answers`() {
        val one = listOf(ReactionSummary("👍", listOf("u1")))
        // A replayed "added" for a user already present must not double-count.
        assertThat(one.applyToggle("👍", "u1", added = true)).isEqualTo(one)
        // A "removed" for an emoji nobody used is a no-op.
        assertThat(one.applyToggle("😂", "u9", added = false)).isEqualTo(one)
    }

    // ── read receipts ───────────────────────────────────────────────────

    @Test
    fun `a receipt for THIS conversation sets the watermark and others do not`() {
        val controller = ThreadController("c1", repositoryUnused(), viewerId = "me")

        val updated = controller.onSocketEvent(
            ChatSocketEvent.ReadReceipt(conversationId = "c1", userId = "peer", messageId = "m5"),
        )
        assertThat(updated?.peerLastReadMessageId).isEqualTo("m5")

        val foreign = controller.onSocketEvent(
            ChatSocketEvent.ReadReceipt(conversationId = "OTHER", userId = "peer", messageId = "m6"),
        )
        assertThat(foreign).isNull()
        assertThat(controller.snapshot().peerLastReadMessageId).isEqualTo("m5")
    }

    private fun repositoryUnused() = com.us.android.core.chat.data.ChatRepository(
        java.lang.reflect.Proxy.newProxyInstance(
            com.us.android.core.chat.data.ChatApi::class.java.classLoader,
            arrayOf(com.us.android.core.chat.data.ChatApi::class.java),
        ) { _, method, _ -> error("not used: ${method.name}") }
            as com.us.android.core.chat.data.ChatApi,
        com.us.android.core.network.ErrorMapper(kotlinx.serialization.json.Json),
    )
}
