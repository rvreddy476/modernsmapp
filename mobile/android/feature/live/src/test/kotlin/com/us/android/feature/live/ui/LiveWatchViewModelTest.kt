package com.us.android.feature.live.ui

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.core.ui.UsPostReportState
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveReportReason
import com.us.android.feature.live.data.LiveRoomSignal
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.ViewerMessageAction
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import org.junit.Rule
import org.junit.Test

/**
 * Protects the viewer's truthful states (the server's status, its ended
 * reason, the count without the host), the end of polling and of the room
 * when a stream is over, removed chat staying gone, chat refusals, and the
 * Report sheet's requests.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class LiveWatchViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val mainRule = MainDispatcherRule(dispatcher)

    private val api = FakeLiveApi()
    private val rooms = FakeRoomFactory()
    private val vms = HeldViewModels()

    private fun TestScope.viewModel(): LiveWatchViewModel {
        val vm = vms.hold {
            LiveWatchViewModel(
                api,
                ErrorMapper(NetworkModule.provideJson()),
                rooms,
                SavedStateHandle(mapOf("streamId" to "s1")),
            )
        }
        runCurrent()
        return vm
    }

    private fun TestScope.poll() {
        advanceTimeBy(LiveWatchViewModel.POLL_MILLIS)
        runCurrent()
    }

    @Test
    fun `an ended stream shows its reason and never asks for a token`() = liveTest(dispatcher, vms) {
        api.stream = api.stream.copy(status = "ended", endedReason = "admin_stopped")

        val vm = viewModel()

        assertThat(vm.state.value.status).isEqualTo(LiveStatus.Ended)
        assertThat(vm.state.value.endedReason).isEqualTo(EndedReason.AdminStopped)
        assertThat(vm.state.value.canChat).isFalse()
        assertThat(api.calls).doesNotContain("GET token")
        assertThat(rooms.sessions).isEmpty()
    }

    @Test
    fun `a live stream joins with the viewer token and shows the server's count`() = liveTest(dispatcher, vms) {
        api.stream = api.stream.copy(status = "live", viewerCount = 2)

        val vm = viewModel()

        assertThat(rooms.sessions.single().connectedWith).isEqualTo("wss://lk.test" to "view-token")
        assertThat(vm.state.value.status).isEqualTo(LiveStatus.Live)
        assertThat(vm.state.value.viewerCount).isEqualTo(2)
        assertThat(vm.state.value.loading).isFalse()
    }

    @Test
    fun `starting, reconnecting and ended follow the server, and an ended stream stops everything`() =
        liveTest(dispatcher, vms) {
            api.stream = api.stream.copy(status = "starting")
            val vm = viewModel()
            assertThat(vm.state.value.status).isEqualTo(LiveStatus.Starting)

            api.stream = api.stream.copy(status = "live")
            poll()
            assertThat(vm.state.value.status).isEqualTo(LiveStatus.Live)

            api.stream = api.stream.copy(status = "reconnecting")
            poll()
            assertThat(vm.state.value.status).isEqualTo(LiveStatus.Reconnecting)

            api.stream = api.stream.copy(status = "ended", endedReason = "host_lost")
            poll()
            assertThat(vm.state.value.status).isEqualTo(LiveStatus.Ended)
            assertThat(vm.state.value.endedReason).isEqualTo(EndedReason.HostLost)
            assertThat(rooms.sessions.single().disconnected).isTrue()

            val reads = api.count("GET stream")
            poll()
            assertThat(api.count("GET stream")).isEqualTo(reads)
        }

    @Test
    fun `the picture follows the room's video signals`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        val room = rooms.sessions.single()

        room.signalFlow.emit(LiveRoomSignal.VideoReady)
        runCurrent()
        assertThat(vm.state.value.hasVideo).isTrue()
        assertThat(vm.state.value.videoVersion).isEqualTo(1)

        room.signalFlow.emit(LiveRoomSignal.VideoGone)
        runCurrent()
        assertThat(vm.state.value.hasVideo).isFalse()
    }

    @Test
    fun `losing our own connection asks the server first, then offers a retry`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        val reads = api.count("GET stream")

        rooms.sessions.single().signalFlow.emit(LiveRoomSignal.Disconnected)
        runCurrent()

        assertThat(api.count("GET stream")).isEqualTo(reads + 1)
        assertThat(vm.state.value.joinError).isEqualTo(LiveWatchViewModel.CONNECTION_LOST_COPY)

        vm.onRetry()
        runCurrent()
        assertThat(rooms.sessions).hasSize(2)
        assertThat(vm.state.value.joinError).isNull()
    }

    @Test
    fun `a disconnect after the server ended the stream shows the end, not an error`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        api.stream = api.stream.copy(status = "ended", endedReason = "host_ended")

        rooms.sessions.single().signalFlow.emit(LiveRoomSignal.Disconnected)
        runCurrent()

        assertThat(vm.state.value.status).isEqualTo(LiveStatus.Ended)
        assertThat(vm.state.value.joinError).isNull()
    }

    @Test
    fun `a viewer the stream refuses sees why`() = liveTest(dispatcher, vms) {
        api.failures["GET token"] = httpError(403, "BANNED")

        val vm = viewModel()

        assertThat(vm.state.value.joinError).isEqualTo("You can't watch this stream.")
        assertThat(rooms.sessions).isEmpty()
    }

    @Test
    fun `a message removed on the server disappears on the next poll`() = liveTest(dispatcher, vms) {
        api.chat = listOf(msg("b"), msg("a"))
        val vm = viewModel()
        assertThat(vm.state.value.chat.messages.map { it.id }).containsExactly("b", "a").inOrder()

        api.chat = listOf(msg("b"))
        poll()

        assertThat(vm.state.value.chat.messages.map { it.id }).containsExactly("b")
    }

    @Test
    fun `a muted viewer gets their words back and the reason`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        api.failures["POST chat"] = httpError(403, "CHAT_MUTED")

        vm.onDraftChanged("hello")
        vm.onSendChat()
        runCurrent()

        assertThat(vm.state.value.draft).isEqualTo("hello")
        assertThat(vm.state.value.notice).isEqualTo("You've been muted in this chat.")
        assertThat(vm.state.value.sending).isFalse()
    }

    @Test
    fun `a sent message shows before the next poll`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        vm.onDraftChanged("hello")
        vm.onSendChat()
        runCurrent()

        assertThat(vm.state.value.draft).isEmpty()
        assertThat(vm.state.value.chat.messages.first().text).isEqualTo("hello")
    }

    @Test
    fun `an emoji message is sent whole, and emoji alone is a message`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        vm.onDraftChanged("so good 🎉❤️")
        vm.onSendChat()
        runCurrent()
        vm.onDraftChanged("🔥")
        vm.onSendChat()
        runCurrent()

        assertThat(api.sentChat).containsExactly("so good 🎉❤️", "🔥").inOrder()
        assertThat(vm.state.value.draft).isEmpty()
    }

    @Test
    fun `the draft is held to 500 code points, so 500 emoji fit and the 501st is not half kept`() =
        liveTest(dispatcher, vms) {
            val vm = viewModel()

            vm.onDraftChanged("😀".repeat(501))

            assertThat(vm.state.value.draft).isEqualTo("😀".repeat(500))
            vm.onSendChat()
            runCurrent()
            assertThat(api.sentChat).containsExactly("😀".repeat(500))
        }

    @Test
    fun `reporting a message sends its id, the reason token and the trimmed note`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        vm.onReportMessage(msg("m9"))
        vm.onSubmitReport(LiveReportReason.Harassment, "  rude  ")
        runCurrent()

        val sent = api.reports.single()
        assertThat(sent.reason).isEqualTo("harassment")
        assertThat(sent.messageId).isEqualTo("m9")
        assertThat(sent.note).isEqualTo("rude")
        assertThat(vm.state.value.report).isEqualTo(UsPostReportState.Sent)
    }

    @Test
    fun `reporting the stream sends no message id and no blank note`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        vm.onReportStream()
        vm.onSubmitReport(LiveReportReason.Spam, "   ")
        runCurrent()

        val sent = api.reports.single()
        assertThat(sent.messageId).isNull()
        assertThat(sent.note).isNull()
    }

    @Test
    fun `a second report of the same thing reads as already reported`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        api.failures["POST reports"] = httpError(409, "ALREADY_REPORTED")

        vm.onReportStream()
        vm.onSubmitReport(LiveReportReason.Spam, "")
        runCurrent()

        assertThat(vm.state.value.report).isEqualTo(UsPostReportState.AlreadyReported)
    }

    @Test
    fun `a plain viewer's long press goes straight to reporting that message`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        vm.onMessageLongPress(msg("m1"))

        assertThat(vm.state.value.selected).isNull()
        assertThat(vm.state.value.reportTarget).isEqualTo(LiveWatchViewModel.ReportTarget.Message(msg("m1")))
    }

    @Test
    fun `a moderator viewer gets the menu and can remove and ban`() = liveTest(dispatcher, vms) {
        api.stream = api.stream.copy(moderatorUserIds = listOf("me"))
        api.chat = listOf(msg("b"), msg("a"))
        val vm = viewModel()
        assertThat(vm.state.value.canModerate).isTrue()

        vm.onMessageLongPress(msg("a"))
        assertThat(vm.state.value.selected).isEqualTo(msg("a"))

        vm.onViewerAction(msg("a"), ViewerMessageAction.RemoveMessage)
        runCurrent()
        assertThat(api.calls).contains("DELETE chat/a")
        assertThat(vm.state.value.chat.messages.map { it.id }).containsExactly("b")
        assertThat(vm.state.value.selected).isNull()

        vm.onViewerAction(msg("b"), ViewerMessageAction.Ban)
        runCurrent()
        assertThat(api.bans.single().userId).isEqualTo("u-b")
    }

    @Test
    fun `a moderator viewer cannot ban the host, and a plain viewer cannot remove`() = liveTest(dispatcher, vms) {
        api.stream = api.stream.copy(moderatorUserIds = listOf("me"))
        val vm = viewModel()

        vm.onViewerAction(LiveChatMessageDto(id = "h1", userId = "host", text = "hi"), ViewerMessageAction.Ban)
        runCurrent()
        assertThat(api.calls).doesNotContain("POST bans")

        api.stream = api.stream.copy(moderatorUserIds = null)
        poll()
        vm.onViewerAction(msg("a"), ViewerMessageAction.RemoveMessage)
        runCurrent()
        assertThat(api.calls).doesNotContain("DELETE chat/a")
    }

    @Test
    fun `no report is sent without a target`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        vm.onSubmitReport(LiveReportReason.Spam, "")
        runCurrent()

        assertThat(api.reports).isEmpty()
    }

    private fun msg(id: String) = LiveChatMessageDto(id = id, userId = "u-$id", text = "t$id")
}
