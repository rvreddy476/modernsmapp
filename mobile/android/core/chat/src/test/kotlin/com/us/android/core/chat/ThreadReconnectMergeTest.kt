package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.mergeLatestPage
import com.us.android.core.chat.data.messagesMissingFromPage
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.io.IOException

/**
 * Chat audit M2: a reconnect MERGES the newest page into the open thread.
 * Older pages, the paging cursor and the reader's place survive it; what the
 * server no longer returns inside the page's window goes.
 */
class ThreadReconnectMergeTest {

    /** Messages one minute apart: m1 at 10:01, m2 at 10:02, ... */
    private fun at(n: Int) = "2026-11-01T10:%02d:00Z".format(n)

    private fun msg(n: Int, pending: Boolean = false) =
        Message("m$n", "c1", "u2", null, "text $n", at(n), pending = pending)

    private fun dto(n: Int) =
        MessageDto(msgId = "m$n", conversationId = "c1", senderId = "u2", text = "text $n", createdAt = at(n))

    private fun ids(messages: List<Message>) = messages.map { it.id }

    // ── the merge rule ────────────────────────────────────────────────────

    @Test
    fun `a newest page that overlaps keeps every older page`() {
        val current = listOf(5, 4, 3, 2, 1).map(::msg)
        val page = listOf(6, 5, 4).map(::msg)

        val merged = mergeLatestPage(current, page, pageHasMore = true)

        assertThat(merged?.let(::ids)).containsExactly("m6", "m5", "m4", "m3", "m2", "m1").inOrder()
    }

    @Test
    fun `a message the server no longer returns inside the page window goes`() {
        val current = listOf(5, 4, 3, 2, 1).map(::msg)
        // m4 was unsent (or expired) while the socket was down.
        val page = listOf(6, 5, 3).map(::msg)

        val merged = mergeLatestPage(current, page, pageHasMore = true)

        assertThat(merged?.let(::ids)).containsExactly("m6", "m5", "m3", "m2", "m1").inOrder()
    }

    @Test
    fun `a page that does not reach the held messages is a gap - null, replace`() {
        val current = listOf(3, 2, 1).map(::msg)
        val page = listOf(9, 8, 7).map(::msg)

        assertThat(mergeLatestPage(current, page, pageHasMore = true)).isNull()
    }

    @Test
    fun `a page that is the whole history replaces what it does not hold`() {
        val current = listOf(3, 2, 1).map(::msg)
        val page = listOf(5, 4).map(::msg)

        val merged = mergeLatestPage(current, page, pageHasMore = false)

        assertThat(merged?.let(::ids)).containsExactly("m5", "m4").inOrder()
    }

    @Test
    fun `optimistic rows stay on top, and a row newer than the page is kept`() {
        val current = listOf(msg(99, pending = true), msg(8), msg(5), msg(4))
        val page = listOf(6, 5, 4).map(::msg)

        val merged = mergeLatestPage(current, page, pageHasMore = true)

        assertThat(merged?.let(::ids)).containsExactly("m99", "m8", "m6", "m5", "m4").inOrder()
    }

    @Test
    fun `only rows inside the page window count as gone`() {
        val cached = listOf(msg(9), msg(6), msg(5), msg(2), msg(99, pending = true))
        val page = listOf(msg(7), msg(4))

        assertThat(messagesMissingFromPage(cached, page, hasMore = true)).containsExactly("m6", "m5")
        assertThat(messagesMissingFromPage(cached, page, hasMore = false)).containsExactly("m6", "m5", "m2")
        // An unreadable time is never taken for gone.
        val untimed = Message("x", "c1", "u", null, "", "not a time")
        assertThat(messagesMissingFromPage(listOf(untimed), page, hasMore = false)).isEmpty()
        // An empty page proves nothing: a row may have arrived while it was read.
        assertThat(messagesMissingFromPage(cached, emptyList(), hasMore = true)).isEmpty()
        assertThat(messagesMissingFromPage(cached, emptyList(), hasMore = false)).isEmpty()
    }

    // ── the controller ────────────────────────────────────────────────────

    private class PagesApi : ChatApi by unscriptedMergeChat() {
        val pages = ArrayDeque<() -> ApiEnvelope<List<MessageDto>>>()
        override suspend fun messages(
            conversationId: String,
            limit: Int,
            cursor: String?,
        ): ApiEnvelope<List<MessageDto>> =
            pages.removeFirst()()
    }

    private fun page(vararg ns: Int, next: String? = null): () -> ApiEnvelope<List<MessageDto>> =
        { ApiEnvelope(data = ns.map(::dto), meta = ApiMeta(nextCursor = next)) }

    private fun controller(api: PagesApi) =
        ThreadController("c1", ChatRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })))

    @Test
    fun `a reconnect keeps the pages scrolled back to and the cursor they continue from`() = runTest {
        val api = PagesApi()
        val controller = controller(api)
        api.pages += page(5, 4, 3, next = "older-1")
        api.pages += page(2, 1, next = "older-2")
        controller.refresh()
        controller.loadMore()

        api.pages += page(6, 5, 4, next = "fresh-1")
        val state = controller.refreshLatest()

        assertThat(ids(state.messages)).containsExactly("m6", "m5", "m4", "m3", "m2", "m1").inOrder()
        assertThat(state.nextCursor).isEqualTo("older-2")
        assertThat(controller.newestPage?.messages?.let(::ids)).containsExactly("m6", "m5", "m4").inOrder()
    }

    @Test
    fun `a reconnect after a long gap starts again from the newest page`() = runTest {
        val api = PagesApi()
        val controller = controller(api)
        api.pages += page(3, 2, 1, next = "older-1")
        controller.refresh()

        api.pages += page(9, 8, 7, next = "fresh-1")
        val state = controller.refreshLatest()

        assertThat(ids(state.messages)).containsExactly("m9", "m8", "m7").inOrder()
        assertThat(state.nextCursor).isEqualTo("fresh-1")
    }

    @Test
    fun `a failed reconnect read keeps the thread as it was`() = runTest {
        val api = PagesApi()
        val controller = controller(api)
        api.pages += page(3, 2, 1, next = "older-1")
        controller.refresh()

        api.pages += { throw IOException("offline") }
        val state = controller.refreshLatest()

        assertThat(ids(state.messages)).containsExactly("m3", "m2", "m1").inOrder()
        assertThat(state.nextCursor).isEqualTo("older-1")
        assertThat(state.refreshError).isNotNull()
    }

    @Test
    fun `before any history was read a reconnect reads it in full`() = runTest {
        val api = PagesApi()
        val controller = controller(api)

        api.pages += page(2, 1, next = "older-1")
        val state = controller.refreshLatest()

        assertThat(ids(state.messages)).containsExactly("m2", "m1").inOrder()
        assertThat(state.nextCursor).isEqualTo("older-1")
    }
}

private fun unscriptedMergeChat(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
