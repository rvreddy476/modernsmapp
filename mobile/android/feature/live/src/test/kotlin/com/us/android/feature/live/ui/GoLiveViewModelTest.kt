package com.us.android.feature.live.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.live.data.EndedReason
import com.us.android.feature.live.data.HostMessageAction
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveStatus
import com.us.android.feature.live.data.LiveStreamDto
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import org.junit.Rule
import org.junit.Test
import java.io.IOException

/**
 * Protects the host's truthful lifecycle: the pilot refusal, Starting until
 * the server says live, every later state from the server, End, and the host
 * moderation calls (remove, ban, unban, the moderator set and its cap).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class GoLiveViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val mainRule = MainDispatcherRule(dispatcher)

    private val json = NetworkModule.provideJson()
    private val api = FakeLiveApi()
    private val rooms = FakeRoomFactory()
    private val vms = HeldViewModels()

    private fun viewModel(factory: FakeRoomFactory = rooms) =
        vms.hold { GoLiveViewModel(api, ErrorMapper(json), factory, json) }

    private fun TestScope.goLive(vm: GoLiveViewModel) {
        // The eligibility answer lands first: the form is not offered before it.
        runCurrent()
        vm.onTitleChanged("Hello")
        vm.onGoLive()
        runCurrent()
    }

    private fun TestScope.poll() {
        advanceTimeBy(GoLiveViewModel.POLL_MILLIS)
        runCurrent()
    }

    @Test
    fun `a user outside the pilot sees the pilot copy, no retry, and no room is opened`() = liveTest(dispatcher, vms) {
        api.failures["POST create"] = httpError(403, "LIVE_NOT_ENABLED")
        val vm = viewModel()

        goLive(vm)

        val phase = vm.state.value.phase as GoLiveViewModel.Phase.Refused
        assertThat(phase.refusal.message).isEqualTo("Going live is in a closed pilot right now.")
        assertThat(phase.refusal.canRetry).isFalse()
        assertThat(vm.state.value.canGoLive).isFalse()
        assertThat(rooms.sessions).isEmpty()
        assertThat(api.calls).doesNotContain("POST start")
    }

    @Test
    fun `a pilot refusal on start is the same refusal`() = liveTest(dispatcher, vms) {
        api.failures["POST start"] = httpError(403, "LIVE_NOT_ENABLED")
        val vm = viewModel()

        goLive(vm)

        val phase = vm.state.value.phase as GoLiveViewModel.Phase.Refused
        assertThat(phase.refusal.message).isEqualTo(LIVE_PILOT_COPY)
        assertThat(rooms.sessions).isEmpty()
    }

    @Test
    fun `a network failure can be retried from the form`() = liveTest(dispatcher, vms) {
        api.failures["POST create"] = IOException("offline")
        val vm = viewModel()

        goLive(vm)
        assertThat(vm.state.value.canGoLive).isTrue()
        vm.onTryAgain()

        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.Setup)
    }

    @Test
    fun `a connected camera is Starting until the server says live`() = liveTest(dispatcher, vms) {
        api.stream = api.stream.copy(status = "starting")
        val vm = viewModel()

        goLive(vm)

        val room = rooms.sessions.single()
        assertThat(room.connectedWith).isEqualTo("wss://lk.test" to "pub-token")
        assertThat(room.published).isTrue()
        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.OnAir(LiveStatus.Starting))

        poll()
        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.OnAir(LiveStatus.Starting))

        api.stream = api.stream.copy(status = "live", viewerCount = 4)
        poll()
        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.OnAir(LiveStatus.Live))
        assertThat(vm.state.value.viewerCount).isEqualTo(4)
    }

    @Test
    fun `reconnecting then host_lost is shown as the server says and drops the room`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)

        api.stream = api.stream.copy(status = "reconnecting")
        poll()
        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.OnAir(LiveStatus.Reconnecting))

        api.stream = api.stream.copy(status = "ended", endedReason = "host_lost")
        poll()
        assertThat(vm.state.value.phase)
            .isEqualTo(GoLiveViewModel.Phase.Over(LiveStatus.Ended, EndedReason.HostLost))
        assertThat(rooms.sessions.single().disconnected).isTrue()

        val polls = api.count("GET stream")
        poll()
        assertThat(api.count("GET stream")).isEqualTo(polls)
    }

    @Test
    fun `a stream that never got media is Failed with no_media`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)

        api.stream = api.stream.copy(status = "failed", endedReason = "no_media")
        poll()

        assertThat(vm.state.value.phase)
            .isEqualTo(GoLiveViewModel.Phase.Over(LiveStatus.Failed, EndedReason.NoMedia))
    }

    @Test
    fun `a failed poll keeps the last answer`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)
        api.stream = api.stream.copy(status = "live")
        poll()

        api.failures["GET stream"] = IOException("blip")
        poll()

        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.OnAir(LiveStatus.Live))
    }

    @Test
    fun `a camera that never connects ends the stream and offers a retry`() = liveTest(dispatcher, vms) {
        val failing = FakeRoomFactory(failConnect = true)
        val vm = viewModel(failing)

        goLive(vm)

        val phase = vm.state.value.phase as GoLiveViewModel.Phase.Refused
        assertThat(phase.refusal.message).isEqualTo(HOST_MEDIA_FAILED_COPY)
        assertThat(phase.refusal.canRetry).isTrue()
        assertThat(api.calls).contains("POST end")
        assertThat(failing.sessions.single().disconnected).isTrue()
    }

    @Test
    fun `End calls the server, drops the room and says host_ended`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)

        vm.onEndStream()
        runCurrent()

        assertThat(api.calls).contains("POST end")
        assertThat(rooms.sessions.single().disconnected).isTrue()
        assertThat(vm.state.value.phase)
            .isEqualTo(GoLiveViewModel.Phase.Over(LiveStatus.Ended, EndedReason.HostEnded, unconfirmed = false))
    }

    @Test
    fun `an End the server did not acknowledge is marked unconfirmed`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)
        api.failures["POST end"] = IOException("offline")

        vm.onEndStream()
        runCurrent()

        val phase = vm.state.value.phase as GoLiveViewModel.Phase.Over
        assertThat(phase.unconfirmed).isTrue()
    }

    @Test
    fun `removing a message calls DELETE and a stale poll cannot bring it back`() = liveTest(dispatcher, vms) {
        api.chat = listOf(msg("b", "u2"), msg("a", "u1"))
        val vm = viewModel()
        goLive(vm)
        poll()
        assertThat(vm.state.value.chat.messages.map { it.id }).containsExactly("b", "a").inOrder()

        vm.onHostAction(msg("a", "u1"), HostMessageAction.RemoveMessage)
        runCurrent()
        assertThat(api.calls).contains("DELETE chat/a")
        assertThat(vm.state.value.chat.messages.map { it.id }).containsExactly("b")

        poll() // the server list still has "a" (removal not yet visible to this read)
        assertThat(vm.state.value.chat.messages.map { it.id }).containsExactly("b")
    }

    @Test
    fun `a remove that the server refuses keeps the message and says why`() = liveTest(dispatcher, vms) {
        api.chat = listOf(msg("a", "u1"))
        val vm = viewModel()
        goLive(vm)
        poll()
        api.failures["DELETE chat/a"] = httpError(403, "FORBIDDEN")

        vm.onRemoveMessage("a")
        runCurrent()

        assertThat(vm.state.value.chat.messages.map { it.id }).containsExactly("a")
        assertThat(vm.state.value.notice).isEqualTo("Only the host and moderators can do that.")
    }

    @Test
    fun `ban posts the author and unban lifts it`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)

        vm.onBan("u2", reason = "  spam ")
        runCurrent()
        assertThat(api.bans.single().userId).isEqualTo("u2")
        assertThat(api.bans.single().reason).isEqualTo("spam")
        assertThat(vm.state.value.banned).containsExactly("u2")

        vm.onUnban("u2")
        runCurrent()
        assertThat(api.calls).contains("DELETE bans/u2")
        assertThat(vm.state.value.banned).isEmpty()
    }

    @Test
    fun `the host cannot ban themselves`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)

        vm.onBan("host")
        runCurrent()

        assertThat(api.calls).doesNotContain("POST bans")
    }

    @Test
    fun `moderators are PUT as the full set and a sixth is refused without a request`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)

        listOf("u1", "u2", "u3", "u4", "u5").forEach {
            vm.onMakeModerator(it)
            runCurrent()
        }
        assertThat(api.moderatorPuts.last()).containsExactly("u1", "u2", "u3", "u4", "u5").inOrder()

        vm.onMakeModerator("u6")
        runCurrent()
        assertThat(api.count("PUT moderators")).isEqualTo(5)
        assertThat(vm.state.value.notice).isEqualTo("A stream can have at most 5 moderators.")

        vm.onRemoveModerator("u3")
        runCurrent()
        assertThat(api.moderatorPuts.last()).containsExactly("u1", "u2", "u4", "u5").inOrder()
        assertThat(vm.state.value.moderators).containsExactly("u1", "u2", "u4", "u5").inOrder()
    }

    @Test
    fun `the viewer count is the server's, not the room's`() = liveTest(dispatcher, vms) {
        api.started = api.started.copy(stream = LiveStreamDto(id = "s1", status = "starting", viewerCount = 0))
        val vm = viewModel()
        goLive(vm)
        assertThat(vm.state.value.viewerCount).isEqualTo(0)

        api.stream = api.stream.copy(status = "live", viewerCount = 7)
        poll()
        assertThat(vm.state.value.viewerCount).isEqualTo(7)
    }

    private fun msg(id: String, user: String) = LiveChatMessageDto(id = id, userId = user, text = "t$id")
}
