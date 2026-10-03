package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.home.CallRequest
import com.us.android.feature.dating.home.MatchCalls
import com.us.android.feature.dating.home.MatchDetailState
import com.us.android.feature.dating.home.MatchDetailViewModel
import com.us.android.feature.dating.network.MatchDto
import com.us.android.feature.dating.network.PremiumEntitlementDto
import com.us.android.feature.dating.network.PremiumMeDto
import com.us.android.feature.dating.network.ReadReceiptsDto
import com.us.android.feature.dating.network.ReadReceiptsRequest
import com.us.android.feature.dating.premium.activeFeatureLabels
import com.us.android.feature.dating.premium.featureLabel
import com.us.android.feature.dating.premium.featureLabels
import com.us.android.feature.dating.privacy.ReadReceiptsPhase
import com.us.android.feature.dating.privacy.ReadReceiptsViewModel
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * Mechanic M9, in-match extras: the read-receipts setting and calls from the
 * match screen; and mechanic M10's list of what a pass unlocks.
 */
class InMatchExtrasTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()

    // ── Read receipts ───────────────────────────────────────────────────────

    private fun receipts() = ReadReceiptsViewModel(repository)

    private fun serverSays(dto: ReadReceiptsDto) {
        api.readReceiptsResponse = { ok(dto) }
    }

    @Test
    fun `read receipts are hidden while the server flag is off`() = runTest {
        // The fake's default is the golden 404 MECHANIC_NOT_ENABLED.
        val vm = receipts()

        assertThat(vm.state.value.phase).isEqualTo(ReadReceiptsPhase.HIDDEN)
        vm.setEnabled(true)
        vm.setEnabled(false)
        assertThat(api.readReceiptsWrites).isEmpty()
        assertThat(vm.state.value.upsell).isFalse()
    }

    @Test
    fun `a pass holder loads with the switch and no lock`() = runTest {
        serverSays(ReadReceiptsDto(enabled = false, active = false, available = true))
        val state = receipts().state.value

        assertThat(state.phase).isEqualTo(ReadReceiptsPhase.READY)
        assertThat(state.enabled).isFalse()
        assertThat(state.showSwitch).isTrue()
        assertThat(state.locked).isFalse()
        assertThat(state.paused).isFalse()
    }

    @Test
    fun `without a pass the section is locked and turning it on opens the upsell instead of asking`() = runTest {
        api.readReceiptsResponse = { ok(fixture("read_receipts_get_200.json", ReadReceiptsDto.serializer())) }
        val vm = receipts()

        assertThat(vm.state.value.phase).isEqualTo(ReadReceiptsPhase.READY)
        assertThat(vm.state.value.locked).isTrue()
        // No pass and never on: no switch to flip, the lock and Premium instead.
        assertThat(vm.state.value.showSwitch).isFalse()

        vm.setEnabled(true)

        assertThat(vm.state.value.upsell).isTrue()
        assertThat(api.readReceiptsWrites).isEmpty()
        vm.dismissUpsell()
        assertThat(vm.state.value.upsell).isFalse()
    }

    @Test
    fun `turning it on sends enabled and shows what the server holds`() = runTest {
        serverSays(ReadReceiptsDto(available = true))
        api.readReceiptsWriteResponse = { ok(fixture("read_receipts_put_200.json", ReadReceiptsDto.serializer())) }
        val vm = receipts()

        vm.setEnabled(true)

        assertThat(api.readReceiptsWrites).containsExactly(ReadReceiptsRequest(enabled = true))
        val state = vm.state.value
        assertThat(state.enabled).isTrue()
        assertThat(state.active).isTrue()
        assertThat(state.busy).isFalse()
        assertThat(state.upsell).isFalse()
    }

    @Test
    fun `a pass that ran out since the read turns the refusal into the upsell`() = runTest {
        serverSays(ReadReceiptsDto(available = true))
        api.readReceiptsWriteResponse = { refusedWithFixture(403, "read_receipts_put_403_requires_pass.json") }
        val vm = receipts()

        vm.setEnabled(true)

        val state = vm.state.value
        assertThat(state.upsell).isTrue()
        assertThat(state.enabled).isFalse()
        assertThat(state.available).isFalse()
        assertThat(state.locked).isTrue()
        assertThat(state.busy).isFalse()
        // The upsell says it; no error line on top.
        assertThat(state.message).isNull()
    }

    @Test
    fun `turning it off is always sent, even with no pass behind it`() = runTest {
        // Still on from a pass that has ended: paused, but the switch stays so it can be turned off.
        serverSays(ReadReceiptsDto(enabled = true, active = false, available = false))
        api.readReceiptsWriteResponse = { ok(fixture("read_receipts_get_200.json", ReadReceiptsDto.serializer())) }
        val vm = receipts()
        assertThat(vm.state.value.paused).isTrue()
        assertThat(vm.state.value.showSwitch).isTrue()
        assertThat(vm.state.value.locked).isTrue()

        vm.setEnabled(false)

        assertThat(api.readReceiptsWrites).containsExactly(ReadReceiptsRequest(enabled = false))
        assertThat(vm.state.value.enabled).isFalse()
        assertThat(vm.state.value.paused).isFalse()
        assertThat(vm.state.value.upsell).isFalse()
    }

    @Test
    fun `the mechanic switched off between read and write hides the section`() = runTest {
        serverSays(ReadReceiptsDto(available = true))
        api.readReceiptsWriteResponse = { refusedWithFixture(404, "read_receipts_get_404_not_enabled.json") }
        val vm = receipts()

        vm.setEnabled(true)

        assertThat(vm.state.value.phase).isEqualTo(ReadReceiptsPhase.HIDDEN)
    }

    @Test
    fun `any other refusal keeps the setting and says so`() = runTest {
        serverSays(ReadReceiptsDto(available = true))
        api.readReceiptsWriteResponse = { refused(500, "UPDATE_FAILED") }
        val vm = receipts()

        vm.setEnabled(true)

        assertThat(vm.state.value.enabled).isFalse()
        assertThat(vm.state.value.busy).isFalse()
        assertThat(vm.state.value.message?.type).isEqualTo(UsMessageType.Error)
    }

    @Test
    fun `coming back from Premium reads it again, and a pass that landed unlocks it`() = runTest {
        api.readReceiptsResponse = { ok(fixture("read_receipts_get_200.json", ReadReceiptsDto.serializer())) }
        val vm = receipts()
        vm.setEnabled(true)
        assertThat(vm.state.value.upsell).isTrue()

        // The first showing follows init's own read: nothing more is asked.
        vm.shown()
        assertThat(api.calls.count { it == "read-receipts" }).isEqualTo(1)

        serverSays(ReadReceiptsDto(available = true))
        vm.shown()

        assertThat(api.calls.count { it == "read-receipts" }).isEqualTo(2)
        assertThat(vm.state.value.locked).isFalse()
        assertThat(vm.state.value.upsell).isFalse()
    }

    // ── Calls from the match screen ─────────────────────────────────────────

    private val other = "user-other"

    private fun detail() = MatchDetailViewModel(
        SavedStateHandle(mapOf("matchId" to "m-1")),
        repository,
        session,
        SafetyActions(repository, session),
        photoUrls(),
    )

    private fun matchWith(canCall: Boolean?, conversationId: String? = "conv-m-1"): MatchDto =
        match("m-1", other).copy(canCall = canCall, conversationId = conversationId)

    private fun loaded(vm: MatchDetailViewModel) = (vm.state.value as MatchDetailState.Loaded).match

    @Test
    fun `can_call true offers voice and video, and a tap hands the right call to the app`() = runTest {
        // The server's golden, so the arguments are what the server actually sends.
        api.matches = listOf(fixture("match_get_200_can_call.json", MatchDto.serializer()).copy(id = "m-1"))
        val vm = detail()

        assertThat(loaded(vm).calls).isEqualTo(MatchCalls.OPEN)

        vm.startCall(video = true)
        assertThat(vm.call.value).isEqualTo(CallRequest(peerUserId = "<other>", peerName = "Asha", video = true, conversationId = "<uuid>"))
        vm.callStarted()
        assertThat(vm.call.value).isNull()

        vm.startCall(video = false)
        assertThat(vm.call.value).isEqualTo(CallRequest(peerUserId = "<other>", peerName = "Asha", video = false, conversationId = "<uuid>"))
    }

    @Test
    fun `can_call false shows the hint and places no call`() = runTest {
        api.matches = listOf(matchWith(canCall = false))
        val vm = detail()

        assertThat(loaded(vm).calls).isEqualTo(MatchCalls.LOCKED)
        vm.startCall(video = false)
        assertThat(vm.call.value).isNull()
    }

    @Test
    fun `no can_call (the mechanic off) offers neither buttons nor the hint`() = runTest {
        api.matches = listOf(matchWith(canCall = null))
        val vm = detail()

        assertThat(loaded(vm).canCall).isNull()
        assertThat(loaded(vm).calls).isEqualTo(MatchCalls.NONE)
        vm.startCall(video = true)
        assertThat(vm.call.value).isNull()
    }

    @Test
    fun `can_call true with no conversation yet is held back`() = runTest {
        api.matches = listOf(matchWith(canCall = true, conversationId = null))
        val vm = detail()

        assertThat(loaded(vm).calls).isEqualTo(MatchCalls.LOCKED)
        vm.startCall(video = true)
        assertThat(vm.call.value).isNull()
    }

    @Test
    fun `back from the chat the match is read again and calls open`() = runTest {
        api.matches = listOf(matchWith(canCall = false))
        val vm = detail()
        vm.shown()
        assertThat(loaded(vm).calls).isEqualTo(MatchCalls.LOCKED)

        // Both have now written.
        api.matches = listOf(matchWith(canCall = true))
        vm.shown()

        assertThat(loaded(vm).calls).isEqualTo(MatchCalls.OPEN)
        vm.startCall(video = false)
        assertThat(vm.call.value).isEqualTo(CallRequest(peerUserId = other, peerName = "Person $other", video = false, conversationId = "conv-m-1"))
    }

    // ── What a pass unlocks (mechanic M10) ──────────────────────────────────

    @Test
    fun `every pass feature has our own label`() {
        val labels = mapOf(
            "match_extend" to "Extend matches",
            "daily_boost" to "A daily Boost",
            "more_daily_cards" to "More people on Pulse each day",
            "unlimited_rewinds" to "Undo as many passes as you like",
            "more_super_sparks" to "More Super Sparks",
            "see_who_sparked" to "See who sparked you",
            "advanced_filters" to "More filters",
            "travel_mode" to "Browse another city",
            "read_receipts" to "Read receipts",
        )
        labels.forEach { (code, label) -> assertThat(featureLabel(code)).isEqualTo(label) }
    }

    @Test
    fun `an unknown feature code is left off, never shown raw`() {
        assertThat(featureLabel("teleport_mode")).isNull()
        assertThat(featureLabel("")).isNull()
        assertThat(featureLabels(listOf("match_extend", "teleport_mode", "read_receipts", "match_extend")))
            .containsExactly("Extend matches", "Read receipts").inOrder()
        assertThat(featureLabels(emptyList())).isEmpty()
    }

    @Test
    fun `the pass card lists only the active entitlements`() {
        val me = PremiumMeDto(
            isPremium = true,
            entitlements = listOf(
                PremiumEntitlementDto(feature = "travel_mode", active = true),
                PremiumEntitlementDto(feature = "read_receipts", active = false),
                PremiumEntitlementDto(feature = "future_thing", active = true),
            ),
        )
        assertThat(activeFeatureLabels(me)).containsExactly("Browse another city")
    }
}
