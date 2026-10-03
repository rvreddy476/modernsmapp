package com.us.android.feature.live.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.live.data.LiveChatAuthorDto
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.LiveEligibilityDto
import com.us.android.feature.live.data.LiveGateAction
import com.us.android.feature.live.data.LiveProgressDto
import com.us.android.feature.live.data.LiveRequirementDto
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import org.junit.Rule
import org.junit.Test
import java.io.IOException

/**
 * Protects what the go-live screen opens on (live-eligibility contract,
 * 2026-10-02): the eligibility call comes first; not eligible shows the
 * requirements and never creates a stream; `pilot_only` keeps the closed
 * pilot wording; a failed call falls back to the form; a `403
 * LIVE_NOT_ELIGIBLE` from create or start shows the same list. Also the
 * host's own chat: sending, the draft limit, and the names the moderation
 * sheets print.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class GoLiveGateTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val mainRule = MainDispatcherRule(dispatcher)

    private val json = NetworkModule.provideJson()
    private val api = FakeLiveApi()
    private val rooms = FakeRoomFactory()
    private val vms = HeldViewModels()

    private fun viewModel() = vms.hold { GoLiveViewModel(api, ErrorMapper(json), rooms, json) }

    private fun TestScope.goLive(vm: GoLiveViewModel) {
        runCurrent()
        vm.onTitleChanged("Hello")
        vm.onGoLive()
        runCurrent()
    }

    private val unmetActivity = LiveRequirementDto(
        key = "activity",
        met = false,
        posts = LiveProgressDto(current = 1, needed = 3),
        followers = LiveProgressDto(current = 4, needed = 10),
    )

    @Test
    fun `the eligibility call is made before the form is offered`() = liveTest(dispatcher, vms) {
        val vm = viewModel()

        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.Checking)
        vm.onTitleChanged("Hello")
        assertThat(vm.state.value.canGoLive).isFalse()

        runCurrent()

        assertThat(api.calls).containsExactly("GET eligibility")
        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.Setup)
        assertThat(vm.state.value.canGoLive).isTrue()
    }

    @Test
    fun `an eligible answer carries the new-streamer viewer cap to the form`() = liveTest(dispatcher, vms) {
        api.eligibility = LiveEligibilityDto(mode = "open", eligible = true, viewerCap = 200)
        val vm = viewModel()

        runCurrent()

        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.Setup)
        assertThat(vm.state.value.viewerCap).isEqualTo(200)
    }

    @Test
    fun `not eligible shows the requirements and the button that helps, and no stream is created`() =
        liveTest(dispatcher, vms) {
            api.eligibility = LiveEligibilityDto(
                mode = "open",
                eligible = false,
                requirements = listOf(LiveRequirementDto(key = "phone_verified", met = true), unmetActivity),
            )
            val vm = viewModel()

            goLive(vm)

            val phase = vm.state.value.phase as GoLiveViewModel.Phase.NotYet
            assertThat(phase.gate.action).isEqualTo(LiveGateAction.CreatePost)
            assertThat(phase.gate.requirements.map { it.key }).containsExactly("phone_verified", "activity").inOrder()
            assertThat(api.calls).doesNotContain("POST create")
        }

    @Test
    fun `pilot_only shows the closed-pilot wording, with no retry`() = liveTest(dispatcher, vms) {
        api.eligibility = LiveEligibilityDto(mode = "pilot", eligible = false, pilotOnly = true)
        val vm = viewModel()

        runCurrent()

        val phase = vm.state.value.phase as GoLiveViewModel.Phase.Refused
        assertThat(phase.refusal.message).isEqualTo(LIVE_PILOT_COPY)
        assertThat(phase.refusal.canRetry).isFalse()
    }

    @Test
    fun `a failed eligibility call falls back to the form, and the server still decides`() =
        liveTest(dispatcher, vms) {
            api.failures["GET eligibility"] = IOException("offline")
            val vm = viewModel()

            goLive(vm)

            assertThat(api.calls).containsAtLeast("GET eligibility", "POST create", "POST start").inOrder()
            assertThat(vm.state.value.isOnAir).isTrue()
        }

    @Test
    fun `an eligibility route the server does not have yet falls back to the form`() = liveTest(dispatcher, vms) {
        api.failures["GET eligibility"] = httpError(404, "NOT_FOUND")
        val vm = viewModel()

        runCurrent()

        assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.Setup)
    }

    @Test
    fun `a 403 LIVE_NOT_ELIGIBLE from create shows the same list from details requirements`() =
        liveTest(dispatcher, vms) {
            api.failures["POST create"] = httpError(
                status = 403,
                code = "LIVE_NOT_ELIGIBLE",
                details = """{"requirements":[{"key":"phone_verified","met":false},""" +
                    """{"key":"account_age","met":false,"current":2,"needed":7,"unit":"days"}]}""",
            )
            val vm = viewModel()

            goLive(vm)

            val phase = vm.state.value.phase as GoLiveViewModel.Phase.NotYet
            assertThat(phase.gate.action).isEqualTo(LiveGateAction.VerifyPhone)
            assertThat(requirementLines(phase.gate.requirements).map { it.text }).containsExactly(
                "Verify your phone number",
                "Your account must be 7 days old (5 days to go)",
            ).inOrder()
            assertThat(rooms.sessions).isEmpty()
        }

    @Test
    fun `a 403 LIVE_NOT_ELIGIBLE from start shows the list too`() = liveTest(dispatcher, vms) {
        api.failures["POST start"] = httpError(
            status = 403,
            code = "LIVE_NOT_ELIGIBLE",
            details = """{"requirements":[{"key":"activity","met":false}]}""",
        )
        val vm = viewModel()

        goLive(vm)

        val phase = vm.state.value.phase as GoLiveViewModel.Phase.NotYet
        assertThat(phase.gate.action).isEqualTo(LiveGateAction.CreatePost)
    }

    @Test
    fun `a 403 LIVE_NOT_ELIGIBLE with nothing readable is the one-line refusal, never an empty list`() =
        liveTest(dispatcher, vms) {
            api.failures["POST create"] = httpError(403, "LIVE_NOT_ELIGIBLE")
            val vm = viewModel()

            goLive(vm)

            val phase = vm.state.value.phase as GoLiveViewModel.Phase.Refused
            assertThat(phase.refusal.canRetry).isFalse()
        }

    @Test
    fun `checking again asks the server and opens the form once the requirement is met`() =
        liveTest(dispatcher, vms) {
            api.eligibility = LiveEligibilityDto(mode = "open", eligible = false, requirements = listOf(unmetActivity))
            val vm = viewModel()
            runCurrent()
            assertThat(vm.state.value.phase).isInstanceOf(GoLiveViewModel.Phase.NotYet::class.java)

            api.eligibility = LiveEligibilityDto(mode = "open", eligible = true)
            vm.onCheckAgain()
            assertThat(vm.state.value.rechecking).isTrue()
            runCurrent()

            assertThat(api.count("GET eligibility")).isEqualTo(2)
            assertThat(vm.state.value.phase).isEqualTo(GoLiveViewModel.Phase.Setup)
            assertThat(vm.state.value.rechecking).isFalse()
        }

    @Test
    fun `checking again does nothing from the form or on air`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        runCurrent()

        vm.onCheckAgain()
        goLive(vm)
        vm.onCheckAgain()
        runCurrent()

        assertThat(api.count("GET eligibility")).isEqualTo(1)
        assertThat(vm.state.value.isOnAir).isTrue()
    }

    // ── The host's own chat ─────────────────────────────────────────────

    @Test
    fun `the host sends a chat message, emoji and all, and sees it at once`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)

        vm.onDraftChanged("welcome 🎉")
        vm.onSendChat()
        runCurrent()

        assertThat(api.sentChat).containsExactly("welcome 🎉")
        assertThat(vm.state.value.draft).isEmpty()
        assertThat(vm.state.value.chat.messages.map { it.text }).containsExactly("welcome 🎉")
    }

    @Test
    fun `the host's draft is held to 500 code points, never half an emoji`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)

        vm.onDraftChanged("😀".repeat(501))

        assertThat(vm.state.value.draft).isEqualTo("😀".repeat(500))
    }

    @Test
    fun `the names the moderation sheets print come from the chat rows`() = liveTest(dispatcher, vms) {
        val vm = viewModel()
        goLive(vm)
        api.chat = listOf(
            LiveChatMessageDto(
                id = "m1",
                userId = "5f0c2a9e-1111",
                text = "hi",
                author = LiveChatAuthorDto(userId = "5f0c2a9e-1111", name = "Asha Rao"),
            ),
        )

        advanceTimeBy(GoLiveViewModel.POLL_MILLIS)
        runCurrent()

        assertThat(personName("5f0c2a9e-1111", vm.state.value.people)).isEqualTo("Asha Rao")
        assertThat(personName("someone-else", vm.state.value.people)).isEqualTo("Viewer")
    }
}
