package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Join notices: once per person per burst, never the reader's own, a burst
 * collapsed into one row, a message ending a burst, names never ids.
 */
class LiveJoinsTest {

    private val t0 = parseInstantMillis("2026-10-14T10:00:00Z")!!

    private fun join(id: String, atSeconds: Long, name: String? = id.uppercase()) =
        LiveJoin(id, name?.let { LiveChatAuthorDto(userId = id, name = it) }, t0 + atSeconds * 1_000)

    private fun msg(id: String, atSeconds: Long) =
        LiveChatMessageDto(id = id, userId = "w", text = id, createdAt = "2026-10-14T10:00:%02dZ".format(atSeconds))

    private fun labels(items: List<ChatTimelineItem>) = items.map {
        when (it) {
            is ChatTimelineItem.Message -> "msg:${it.message.id}"
            is ChatTimelineItem.Joins -> it.label
        }
    }

    @Test
    fun `labels name up to two people, then count the others, and never print an id`() {
        val a = LiveChatAuthorDto(userId = "u1", name = "Asha")
        val r = LiveChatAuthorDto(userId = "u2", handle = "ravi")
        val m = LiveChatAuthorDto(userId = "u3", name = "Mei")
        val k = LiveChatAuthorDto(userId = "u4", name = "Kiran")

        assertThat(joinLabel(listOf(a))).isEqualTo("Asha joined")
        assertThat(joinLabel(listOf(a, r))).isEqualTo("Asha and @ravi joined")
        assertThat(joinLabel(listOf(a, r, m))).isEqualTo("Asha, @ravi and Mei joined")
        assertThat(joinLabel(listOf(a, r, m, k))).isEqualTo("Asha, @ravi and 2 others joined")
        assertThat(joinLabel(listOf(a, null))).isEqualTo("Asha and 1 other joined")
        assertThat(joinLabel(listOf(null))).isEqualTo("Viewer joined")
        assertThat(joinLabel(listOf(null, LiveChatAuthorDto(userId = "u9")))).isEqualTo("2 viewers joined")
        assertThat(joinLabel(emptyList())).isEmpty()
    }

    @Test
    fun `a redelivered join inside the burst window is the same arrival`() {
        val once = addJoin(emptyList(), join("a", 0))
        val again = addJoin(once, join("a", 3))
        val later = addJoin(again, join("a", 70))

        assertThat(again).isSameInstanceAs(once)
        assertThat(later.map { it.atMillis }).containsExactly(t0, t0 + 70_000).inOrder()
    }

    @Test
    fun `the store keeps the newest joins and refuses a blank id`() {
        var joins = emptyList<LiveJoin>()
        (0 until 50).forEach { joins = addJoin(joins, join("u$it", it * 10L)) }

        assertThat(joins).hasSize(MAX_JOINS)
        assertThat(joins.first().userId).isEqualTo("u10")
        assertThat(addJoin(joins, LiveJoin(" ", null, t0))).isSameInstanceAs(joins)
    }

    @Test
    fun `joins that land together collapse into one row`() {
        val items = chatTimeline(emptyList(), listOf(join("a", 0), join("b", 2), join("c", 4)), meId = "me")

        assertThat(labels(items)).containsExactly("A, B and C joined")
    }

    @Test
    fun `a join past the burst window starts a new row, newest first`() {
        val items = chatTimeline(emptyList(), listOf(join("a", 0), join("b", 2), join("c", 20)), meId = "me")

        assertThat(labels(items)).containsExactly("C joined", "A and B joined").inOrder()
    }

    @Test
    fun `the reader's own join is never shown`() {
        val items = chatTimeline(emptyList(), listOf(join("me", 0), join("a", 1)), meId = "me")

        assertThat(labels(items)).containsExactly("A joined")
        assertThat(chatTimeline(emptyList(), listOf(join("me", 0)), meId = "me")).isEmpty()
    }

    @Test
    fun `a message between joins ends the burst and keeps its place in time`() {
        // Newest first, as GET …/chat returns it.
        val messages = listOf(msg("m2", 30), msg("m1", 2))
        val joins = listOf(join("a", 0), join("b", 3), join("c", 4), join("d", 40))

        val items = chatTimeline(messages, joins, meId = "me")

        assertThat(labels(items)).containsExactly("D joined", "msg:m2", "B and C joined", "msg:m1", "A joined").inOrder()
    }

    @Test
    fun `with no joins the timeline is the messages, untouched`() {
        val messages = listOf(msg("m2", 30), msg("m1", 2))

        assertThat(labels(chatTimeline(messages, emptyList(), meId = "me"))).containsExactly("msg:m2", "msg:m1").inOrder()
    }

    @Test
    fun `only the newest join rows are kept`() {
        val joins = (0 until 20).map { join("u$it", it * 60L) }

        val rows = chatTimeline(emptyList(), joins, meId = "me").filterIsInstance<ChatTimelineItem.Joins>()

        assertThat(rows).hasSize(MAX_JOIN_ROWS)
        assertThat(rows.first().userIds).containsExactly("u19")
    }

    @Test
    fun `row keys are unique so the list can key on them`() {
        val items = chatTimeline(listOf(msg("m1", 10)), listOf(join("a", 0), join("a", 30)), meId = "me")

        assertThat(items.map { it.key }.toSet()).hasSize(items.size)
    }

    @Test
    fun `join time is the frame's joined_at, else the time it arrived`() {
        assertThat(joinTime("2026-10-14T10:00:00Z", 5)).isEqualTo(t0)
        assertThat(joinTime("2026-10-14T15:30:00+05:30", 5)).isEqualTo(t0)
        assertThat(joinTime("", 5)).isEqualTo(5L)
        assertThat(joinTime("yesterday", 5)).isEqualTo(5L)
    }
}
