package com.us.android.feature.live.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowStatus
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.live.data.ChatTimelineItem
import com.us.android.feature.live.data.HeartBlock
import com.us.android.feature.live.data.HEART_FLUSH_MILLIS
import com.us.android.feature.live.data.HeartsAnswerDto
import com.us.android.feature.live.data.LiveChatAuthorDto
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveCreatorCardDto
import com.us.android.feature.live.data.LiveFrame
import com.us.android.feature.live.data.LiveSocketEvent
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.chatTimeline
import com.us.android.feature.live.data.liveSocketRetryMillis
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import org.junit.Rule
import org.junit.Test

/**
 * The room's T-30 parts on the viewer's screen: realtime frames (join rows,
 * mute, pin), the blocked-word refusal that keeps the draft, Follow and
 * Share for viewers only, and free hearts — and none of it for the host
 * watching their own stream.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class LiveWatchRoomTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val mainRule = MainDispatcherRule(dispatcher)

    private val api = FakeLiveApi()
    private val rooms = FakeRoomFactory()
    private val socket = FakeRoomSocket()
    private val vms = HeldViewModels()

    private fun TestScope.viewModel(follows: FakeLiveFollows = FakeLiveFollows(ownId = "me")): LiveWatchViewModel {
        val vm = vms.hold {
            LiveWatchViewModel(
                api,
                ErrorMapper(NetworkModule.provideJson()),
                rooms,
                socket,
                follows,
                SavedStateHandle(mapOf("streamId" to "s1")),
            )
        }
        runCurrent()
        return vm
    }

    private fun TestScope.frame(frame: LiveFrame) {
        socket.events.tryEmit(LiveSocketEvent.Frame(frame))
        runCurrent()
    }

    private fun TestScope.open() {
        socket.events.tryEmit(LiveSocketEvent.Open)
        runCurrent()
    }

    private fun joined(id: String, at: String, name: String? = null) =
        LiveFrame.Joined("s1", id, name?.let { LiveChatAuthorDto(userId = id, name = it) }, at)

    private fun timeline(vm: LiveWatchViewModel) =
        chatTimeline(vm.state.value.chat.messages, vm.state.value.joins, vm.state.value.meId)

    // ── The socket ─────────────────────────────────────────────────────

    @Test
    fun `joining a live stream subscribes to its room`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        assertThat(socket.opened).containsExactly("s1")
        open()
        assertThat(vm.state.value.roomLive).isTrue()
    }

    @Test
    fun `an ended stream opens no room`() = liveTest(dispatcher, vms) {
        api.stream = api.stream.copy(status = "ended")

        viewModel()

        assertThat(socket.opened).isEmpty()
    }

    @Test
    fun `a dropped socket is reopened, a refused room is not`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        open()

        socket.events.tryEmit(LiveSocketEvent.Closed(permanent = false))
        runCurrent()
        assertThat(vm.state.value.roomLive).isFalse()
        advanceTimeBy(liveSocketRetryMillis(1) + 1)
        runCurrent()
        assertThat(socket.opened).hasSize(2)

        frame(LiveFrame.Refused("s1"))
        advanceTimeBy(liveSocketRetryMillis(5) + 1)
        runCurrent()
        assertThat(socket.opened).hasSize(2)
    }

    // ── Join notices ───────────────────────────────────────────────────

    @Test
    fun `a join becomes a system row, a burst collapses, and it is never stored with the chat`() =
        liveTest(dispatcher, vms) {
            val vm = viewModel()

            frame(joined("u2", "2026-10-14T10:00:00Z", "Asha"))
            frame(joined("u3", "2026-10-14T10:00:02Z", "Ravi"))

            val rows = timeline(vm).filterIsInstance<ChatTimelineItem.Joins>()
            assertThat(rows.single().label).isEqualTo("Asha and Ravi joined")
            assertThat(vm.state.value.chat.messages).isEmpty()
        }

    @Test
    fun `the viewer's own join and the host's are skipped`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        frame(joined("me", "2026-10-14T10:00:00Z", "Me"))
        frame(joined("host", "2026-10-14T10:00:01Z", "Host"))

        assertThat(vm.state.value.joins).isEmpty()
    }

    @Test
    fun `a frame for another stream changes nothing`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        vm.onFrame(LiveFrame.Joined("other", "u2", null, "2026-10-14T10:00:00Z"))
        vm.onFrame(LiveFrame.Muted("other", "me"))

        assertThat(vm.state.value.joins).isEmpty()
        assertThat(vm.state.value.muted).isFalse()
    }

    @Test
    fun `a chat frame shows at once, and a removal frame takes it away`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        frame(LiveFrame.Chat("s1", LiveChatMessageDto(id = "m1", userId = "u2", text = "hi")))
        assertThat(vm.state.value.chat.messages.map { it.id }).containsExactly("m1")

        frame(LiveFrame.Removed("s1", "m1"))
        assertThat(vm.state.value.chat.messages).isEmpty()
    }

    // ── Muted ──────────────────────────────────────────────────────────

    @Test
    fun `a mute for this viewer swaps the composer for the notice, and an unmute brings it back`() =
        liveTest(dispatcher, vms) {
            val vm = viewModel()
            assertThat(vm.state.value.showsComposer).isTrue()

            frame(LiveFrame.Muted("s1", "someone-else"))
            assertThat(vm.state.value.muted).isFalse()

            frame(LiveFrame.Muted("s1", "me"))
            assertThat(vm.state.value.muted).isTrue()
            assertThat(vm.state.value.showsComposer).isFalse()
            vm.onDraftChanged("hello")
            vm.onSendChat()
            runCurrent()
            assertThat(api.sentChat).isEmpty()

            frame(LiveFrame.Unmuted("s1", "me"))
            assertThat(vm.state.value.showsComposer).isTrue()
            assertThat(vm.state.value.draft).isEqualTo("hello")
        }

    @Test
    fun `a CHAT_MUTED refusal mutes the composer and keeps the words`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        api.failures["POST chat"] = httpError(403, "CHAT_MUTED")

        vm.onDraftChanged("hello")
        vm.onSendChat()
        runCurrent()

        assertThat(vm.state.value.muted).isTrue()
        assertThat(vm.state.value.draft).isEqualTo("hello")
    }

    // ── Blocked word ───────────────────────────────────────────────────

    @Test
    fun `a blocked word keeps the draft, says why, and leaves the composer open`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        api.failures["POST chat"] = httpError(400, "CHAT_BLOCKED_WORD")

        vm.onDraftChanged("a rude word")
        vm.onSendChat()
        runCurrent()

        assertThat(vm.state.value.draft).isEqualTo("a rude word")
        assertThat(vm.state.value.notice).isEqualTo(BLOCKED_WORD_COPY)
        assertThat(vm.state.value.showsComposer).isTrue()
        assertThat(vm.state.value.muted).isFalse()
    }

    // ── Pinned ─────────────────────────────────────────────────────────

    @Test
    fun `the pinned message is read on join and follows pin and unpin frames`() = liveTest(dispatcher, vms) {
        api.pinned = LiveChatMessageDto(
            id = "p1",
            userId = "host",
            text = "Rules",
            isPinned = true,
            author = LiveChatAuthorDto(userId = "host", name = "Asha"),
        )
        api.chat = listOf(
            LiveChatMessageDto(id = "m2", userId = "u2", text = "Q?", author = LiveChatAuthorDto(userId = "u2", name = "Ravi")),
        )
        val vm = viewModel()
        assertThat(vm.state.value.pinned).isEqualTo(LivePinned("p1", "host", "Rules", LiveChatAuthorDto("host", "Asha")))

        frame(LiveFrame.Pinned("s1", "m2", "u2", "Q?"))
        assertThat(vm.state.value.pinned?.messageId).isEqualTo("m2")
        // The frame has no author card: the row's is used.
        assertThat(vm.state.value.pinned?.author?.name).isEqualTo("Ravi")

        frame(LiveFrame.Unpinned("s1", "other"))
        assertThat(vm.state.value.pinned?.messageId).isEqualTo("m2")
        frame(LiveFrame.Unpinned("s1", "m2"))
        assertThat(vm.state.value.pinned).isNull()
    }

    @Test
    fun `removing the pinned message clears the bar`() = liveTest(dispatcher, vms) {
        api.pinned = LiveChatMessageDto(id = "p1", userId = "host", text = "Rules")
        val vm = viewModel()

        frame(LiveFrame.Removed("s1", "p1"))

        assertThat(vm.state.value.pinned).isNull()
    }

    @Test
    fun `without frames the pin is polled, with frames it is not`() = liveTest(dispatcher, vms) {
        viewModel()
        val reads = api.count("GET pinned")

        advanceTimeBy(LiveWatchViewModel.POLL_MILLIS)
        runCurrent()
        assertThat(api.count("GET pinned")).isEqualTo(reads + 1)

        open()
        advanceTimeBy(LiveWatchViewModel.POLL_MILLIS)
        runCurrent()
        assertThat(api.count("GET pinned")).isEqualTo(reads + 1)
    }

    // ── Follow and Share ───────────────────────────────────────────────

    @Test
    fun `a viewer who does not follow the host gets Follow and Share`() = liveTest(dispatcher, vms) {
        api.stream = api.stream.copy(creator = LiveCreatorCardDto(userId = "host", name = "Asha"))
        val follows = FakeLiveFollows(ownId = "me")
        val vm = viewModel(follows)

        assertThat(follows.asked).containsExactly("host")
        assertThat(vm.state.value.showsFollow).isTrue()
        assertThat(vm.state.value.showsShare).isTrue()
        assertThat(vm.state.value.host?.name).isEqualTo("Asha")

        vm.onFollowHost()
        runCurrent()

        assertThat(follows.followed).containsExactly("host")
        assertThat(vm.state.value.hostEdge).isEqualTo(FollowStatus.FOLLOWING)
        assertThat(vm.state.value.showsFollow).isFalse()
    }

    @Test
    fun `Follow is not offered to a viewer who already follows, or while the answer is unknown`() =
        liveTest(dispatcher, vms) {
            val vm = viewModel(FakeLiveFollows(ownId = "me", known = mapOf("host" to FollowStatus.FOLLOWING)))
            assertThat(vm.state.value.showsFollow).isFalse()
            assertThat(vm.state.value.showsShare).isTrue()
        }

    @Test
    fun `a refused follow says so`() = liveTest(dispatcher, vms) {
        val follows = FakeLiveFollows(ownId = "me", followResult = AppResult.Failure(AppError.NoNetwork()))
        val vm = viewModel(follows)

        vm.onFollowHost()
        runCurrent()

        assertThat(vm.state.value.notice).isEqualTo(LiveWatchViewModel.FOLLOW_FAILED_COPY)
        assertThat(vm.state.value.showsFollow).isTrue()
    }

    @Test
    fun `the host watching their own stream gets no Follow, no Share and no hearts`() = liveTest(dispatcher, vms) {
        val follows = FakeLiveFollows(ownId = "host")
        val vm = viewModel(follows)

        assertThat(vm.state.value.isHost).isTrue()
        assertThat(vm.state.value.showsFollow).isFalse()
        assertThat(vm.state.value.showsShare).isFalse()
        assertThat(vm.state.value.showsHearts).isFalse()
        assertThat(follows.asked).isEmpty()

        vm.onFollowHost()
        vm.onHeart()
        advanceTimeBy(HEART_FLUSH_MILLIS + 1)
        runCurrent()
        assertThat(follows.followed).isEmpty()
        assertThat(api.heartBatches).isEmpty()
    }

    // ── Hearts ─────────────────────────────────────────────────────────

    @Test
    fun `taps are batched once a second, at most twenty a request`() = liveTest(dispatcher, vms) {
        api.stream = api.stream.copy(heartCount = 100)
        api.heartsAnswer = HeartsAnswerDto(heartCount = 120)
        val vm = viewModel()
        assertThat(vm.state.value.heartCount).isEqualTo(100L)

        repeat(25) { vm.onHeart() }
        assertThat(vm.state.value.heartCount).isEqualTo(125L)
        assertThat(api.heartBatches).isEmpty()

        advanceTimeBy(HEART_FLUSH_MILLIS + 1)
        runCurrent()
        assertThat(api.heartBatches).containsExactly(20)

        api.heartsAnswer = HeartsAnswerDto(heartCount = 125)
        advanceTimeBy(HEART_FLUSH_MILLIS + 1)
        runCurrent()
        assertThat(api.heartBatches).containsExactly(20, 5).inOrder()
        assertThat(vm.state.value.heartCount).isEqualTo(125L)
    }

    @Test
    fun `a stream that is not live stops hearts, a rate limit only drops the batch`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        api.failures["POST hearts"] = httpError(429, "RATE_LIMITED")
        vm.onHeart()
        advanceTimeBy(HEART_FLUSH_MILLIS + 1)
        runCurrent()
        assertThat(vm.state.value.heartBlock).isNull()

        api.failures["POST hearts"] = httpError(409, "STREAM_NOT_LIVE")
        vm.onHeart()
        advanceTimeBy(HEART_FLUSH_MILLIS + 1)
        runCurrent()
        assertThat(vm.state.value.heartBlock).isEqualTo(HeartBlock.NotLive)

        val sent = api.count("POST hearts")
        vm.onHeart()
        advanceTimeBy(HEART_FLUSH_MILLIS + 1)
        runCurrent()
        assertThat(api.count("POST hearts")).isEqualTo(sent)
    }

    @Test
    fun `a hearts frame raises the total`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        frame(LiveFrame.Hearts("s1", 4, 512))

        assertThat(vm.state.value.heartCount).isEqualTo(512L)
    }

    @Test
    fun `hearts are not offered once the stream is over`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        assertThat(vm.state.value.showsHearts).isTrue()

        frame(LiveFrame.Status("s1", "ended", "host_ended", null, null))

        assertThat(vm.state.value.status).isEqualTo(LiveStatus.Ended)
        assertThat(vm.state.value.showsHearts).isFalse()
        assertThat(rooms.sessions.single().disconnected).isTrue()
    }
}
