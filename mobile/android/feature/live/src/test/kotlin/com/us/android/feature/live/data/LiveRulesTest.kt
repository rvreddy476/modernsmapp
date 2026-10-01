package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

/**
 * Protects the pure live rules: the server's status words, the "never Live
 * without the server's `live`" rule, the chat tombstones that keep a removed
 * message gone, the moderator cap, and the alphabetical menus.
 */
class LiveRulesTest {

    @Test
    fun `every contract status word maps to its state and anything else is Unknown`() {
        val table = mapOf(
            "scheduled" to LiveStatus.Scheduled,
            "starting" to LiveStatus.Starting,
            "live" to LiveStatus.Live,
            "reconnecting" to LiveStatus.Reconnecting,
            "ended" to LiveStatus.Ended,
            "failed" to LiveStatus.Failed,
            " LIVE " to LiveStatus.Live,
            "" to LiveStatus.Unknown,
            "on_air" to LiveStatus.Unknown,
        )
        table.forEach { (wire, expected) -> assertThat(liveStatusOf(wire)).isEqualTo(expected) }
    }

    @Test
    fun `only ended and failed are final`() {
        val over = LiveStatus.entries.filter { it.isOver }
        assertThat(over).containsExactly(LiveStatus.Ended, LiveStatus.Failed)
    }

    @Test
    fun `every contract ended reason maps and anything else is Unknown`() {
        val table = mapOf(
            "host_ended" to EndedReason.HostEnded,
            "host_lost" to EndedReason.HostLost,
            "room_finished" to EndedReason.RoomFinished,
            "admin_stopped" to EndedReason.AdminStopped,
            "no_media" to EndedReason.NoMedia,
            "" to EndedReason.Unknown,
            "timeout" to EndedReason.Unknown,
        )
        table.forEach { (wire, expected) -> assertThat(endedReasonOf(wire)).isEqualTo(expected) }
    }

    @Test
    fun `after start a blank scheduled or unknown status reads as Starting, never Live`() {
        listOf("", "scheduled", "starting", "garbage").forEach { wire ->
            assertThat(hostStatusAfterStart(wire)).isEqualTo(LiveStatus.Starting)
        }
        assertThat(hostStatusAfterStart("live")).isEqualTo(LiveStatus.Live)
        assertThat(hostStatusAfterStart("reconnecting")).isEqualTo(LiveStatus.Reconnecting)
        assertThat(hostStatusAfterStart("failed")).isEqualTo(LiveStatus.Failed)
        assertThat(hostStatusAfterStart("ended")).isEqualTo(LiveStatus.Ended)
    }

    @Test
    fun `the red badge belongs to live alone`() {
        assertThat(LiveStatus.entries.filter(::showsLiveBadge)).containsExactly(LiveStatus.Live)
    }

    @Test
    fun `the viewer count shows while live or reconnecting only`() {
        assertThat(LiveStatus.entries.filter(::showsViewerCount))
            .containsExactly(LiveStatus.Live, LiveStatus.Reconnecting)
    }

    @Test
    fun `a removed message stays gone when a stale poll still carries it`() {
        val a = msg("a")
        val b = msg("b")
        val log = ChatLog().withSnapshot(listOf(b, a)).withRemoved("a")

        val afterStalePoll = log.withSnapshot(listOf(b, a))

        assertThat(afterStalePoll.messages.map { it.id }).containsExactly("b")
    }

    @Test
    fun `a sent message is shown first, once, and never after its removal`() {
        val log = ChatLog().withSnapshot(listOf(msg("a")))

        val sent = log.withSent(msg("b")).withSent(msg("b"))
        assertThat(sent.messages.map { it.id }).containsExactly("b", "a").inOrder()

        val removedThenEchoed = sent.withRemoved("b").withSent(msg("b"))
        assertThat(removedThenEchoed.messages.map { it.id }).containsExactly("a")
    }

    @Test
    fun `a snapshot keeps the server's newest-first order and drops blank and duplicate ids`() {
        val log = ChatLog().withSnapshot(listOf(msg("c"), msg(""), msg("b"), msg("c"), msg("a")))
        assertThat(log.messages.map { it.id }).containsExactly("c", "b", "a").inOrder()
    }

    @Test
    fun `a blank removal is ignored`() {
        val log = ChatLog().withSnapshot(listOf(msg("a"))).withRemoved("")
        assertThat(log.removed).isEmpty()
        assertThat(log.messages).hasSize(1)
    }

    @Test
    fun `the host can add up to five moderators, never themselves or twice`() {
        val four = listOf("u1", "u2", "u3", "u4")
        assertThat(canAddModerator(four, "u5", hostId = "host")).isTrue()
        assertThat(canAddModerator(four + "u5", "u6", hostId = "host")).isFalse()
        assertThat(canAddModerator(four, "host", hostId = "host")).isFalse()
        assertThat(canAddModerator(four, "u1", hostId = "host")).isFalse()
        assertThat(canAddModerator(four, "", hostId = "host")).isFalse()
    }

    @Test
    fun `the host menu for a viewer's message is alphabetical`() {
        val actions = hostMessageActions(msg("m", user = "u1"), "host", moderators = emptyList(), banned = emptySet())
        assertThat(actions.map { it.label }).containsExactly("Ban user", "Make moderator", "Remove message").inOrder()
    }

    @Test
    fun `the host menu flips to unban and remove moderator when they apply`() {
        val actions = hostMessageActions(msg("m", user = "u1"), "host", moderators = listOf("u1"), banned = setOf("u1"))
        assertThat(actions.map { it.label })
            .containsExactly("Remove message", "Remove moderator", "Unban user")
            .inOrder()
    }

    @Test
    fun `the host's own message can only be removed`() {
        val actions = hostMessageActions(msg("m", user = "host"), "host", emptyList(), emptySet())
        assertThat(actions).containsExactly(HostMessageAction.RemoveMessage)
    }

    @Test
    fun `make moderator is not offered once five are set`() {
        val five = listOf("u1", "u2", "u3", "u4", "u5")
        val actions = hostMessageActions(msg("m", user = "u9"), "host", five, emptySet())
        assertThat(actions).doesNotContain(HostMessageAction.MakeModerator)
    }

    @Test
    fun `a viewer who is not a moderator can only report`() {
        assertThat(viewerMessageActions(msg("m", user = "u1"), hostId = "host", canModerate = false))
            .containsExactly(ViewerMessageAction.Report)
    }

    @Test
    fun `a moderator's menu is alphabetical and never bans the host`() {
        val onViewer = viewerMessageActions(msg("m", user = "u1"), hostId = "host", canModerate = true)
        assertThat(onViewer.map { it.label }).containsExactly("Ban user", "Remove message", "Report message").inOrder()

        val onHost = viewerMessageActions(msg("m", user = "host"), hostId = "host", canModerate = true)
        assertThat(onHost).containsExactly(ViewerMessageAction.RemoveMessage, ViewerMessageAction.Report).inOrder()
    }

    @Test
    fun `report reasons are alphabetical by label and carry the contract tokens`() {
        val reasons = liveReportReasons()
        assertThat(reasons.map { it.label })
            .isEqualTo(reasons.map { it.label }.sortedBy { it.lowercase() })
        assertThat(reasons.map { it.wire })
            .containsExactly("spam", "harassment", "hate", "nudity", "violence", "scam", "other")
    }

    @Test
    fun `the contract code is read from a 403, an envelope error and a 5xx, and blank is none`() {
        assertThat(AppError.Forbidden(code = "LIVE_NOT_ENABLED").liveCode()).isEqualTo("LIVE_NOT_ENABLED")
        assertThat(AppError.Unknown(code = "LIVE_NOT_ENABLED", statusCode = null).liveCode())
            .isEqualTo("LIVE_NOT_ENABLED")
        assertThat(AppError.Server(statusCode = 503, code = "X").liveCode()).isEqualTo("X")
        assertThat(AppError.Unknown(code = "", statusCode = 400).liveCode()).isNull()
        assertThat(AppError.NoNetwork().liveCode()).isNull()
    }

    private fun msg(id: String, user: String = "u") = LiveChatMessageDto(id = id, userId = user, text = "t$id")
}
