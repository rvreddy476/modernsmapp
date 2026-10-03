package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.filters.Dealbreaker
import com.us.android.feature.dating.filters.FiltersCopy
import com.us.android.feature.dating.filters.FiltersViewModel
import com.us.android.feature.dating.home.CardUi
import com.us.android.feature.dating.home.CheckInPhase
import com.us.android.feature.dating.home.CheckInTarget
import com.us.android.feature.dating.home.DateAgain
import com.us.android.feature.dating.home.DateCheckInViewModel
import com.us.android.feature.dating.home.DateMet
import com.us.android.feature.dating.home.DeckCopy
import com.us.android.feature.dating.home.FairTurnUi
import com.us.android.feature.dating.home.IncomingSparkUi
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.SparksViewModel
import com.us.android.feature.dating.navigation.datingCheckInRequested
import com.us.android.feature.dating.network.AllowanceDto
import com.us.android.feature.dating.network.AllowancesDto
import com.us.android.feature.dating.network.DateCheckinDto
import com.us.android.feature.dating.network.DateFeedbackDto
import com.us.android.feature.dating.network.DateFeedbackRequest
import com.us.android.feature.dating.network.FairTurnDto
import com.us.android.feature.dating.network.PassFiltersDto
import com.us.android.feature.dating.network.PastMatchDto
import com.us.android.feature.dating.network.PastMatchPersonDto
import com.us.android.feature.dating.network.PastMatchesDto
import com.us.android.feature.dating.network.PastMatchesMetaDto
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.profile.ProfileOptionsStore
import com.us.android.feature.dating.safety.PastMatchesCopy
import com.us.android.feature.dating.safety.PastMatchesPhase
import com.us.android.feature.dating.safety.PastMatchesViewModel
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportReason
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.builtins.ListSerializer
import org.junit.Rule
import org.junit.Test
import retrofit2.Response
import java.time.Instant
import java.time.ZoneId

/**
 * Pulse mechanics M11 (fair turn), M12 (dealbreakers), M14 (after-date
 * check-ins) and M19 (past matches), in their view models: what switches each
 * on or hides it, what is locked, and how each refusal reads.
 */
class PulseSafetyMechanicsTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)

    // ── M11: fair turn ──────────────────────────────────────────────────────

    private fun pulse() = PulseViewModel(repository, session, safety, photoUrls())

    private fun cards(vm: PulseViewModel): List<String> = (vm.state.value as ListState.Items<CardUi>).items.map { it.userId }

    private fun fairTurnFixture() = fixture("allowances_get_200_fair_turn.json", AllowancesDto.serializer())

    @Test
    fun `without fair_turn the deck sparks as before`() = runTest {
        api.pulse = listOf(card("a"))
        val vm = pulse()

        assertThat(vm.deck.value.fairTurn).isNull()
        assertThat(vm.deck.value.sparksPaused).isFalse()
        assertThat(vm.spark("a")).isTrue()
        assertThat(api.sparks.map { it.toUserId }).containsExactly("a")
    }

    @Test
    fun `fair_turn present but not paused leaves sparks open`() = runTest {
        api.pulse = listOf(card("a"))
        api.allowancesResponse = { ok(AllowancesDto(sparks = AllowanceDto(dailyLimit = 50, remainingToday = 50), fairTurn = FairTurnDto(owed = 2, limit = 6))) }
        val vm = pulse()

        assertThat(vm.deck.value.fairTurn).isEqualTo(FairTurnUi(owed = 2, limit = 6, paused = false))
        assertThat(vm.deck.value.sparksPaused).isFalse()
        assertThat(vm.spark("a")).isTrue()
        assertThat(api.sparks).hasSize(1)
    }

    @Test
    fun `paused holds spark and Super Spark back without asking, while pass still works`() = runTest {
        api.pulse = listOf(card("a"), card("b"))
        api.allowancesResponse = { ok(fairTurnFixture().copy(superSpark = com.us.android.feature.dating.network.SuperSparkAllowanceDto(dailyLimit = 1, remainingToday = 1))) }
        val vm = pulse()

        assertThat(vm.deck.value.sparksPaused).isTrue()
        assertThat(vm.deck.value.fairTurn).isEqualTo(FairTurnUi(owed = 6, limit = 6, paused = true))
        assertThat(vm.spark("a")).isFalse()
        assertThat(vm.superSpark("a")).isFalse()
        assertThat(api.sparks).isEmpty()
        // The card stays where it was.
        assertThat(cards(vm)).containsExactly("a", "b").inOrder()

        assertThat(vm.pass("a")).isTrue()
        assertThat(api.passes).containsExactly("a")
        assertThat(cards(vm)).containsExactly("b")
    }

    @Test
    fun `a 409 FAIR_TURN_LIMIT shows the paused notice, not an error, and reads allowances again`() = runTest {
        api.pulse = listOf(card("a"))
        api.sparkResponse = { refusedWithFixture(409, "sparks_post_409_fair_turn_limit.json") }
        val vm = pulse()
        val reads = api.calls.count { it == "allowances" }
        // The server now counts six replies owed.
        api.allowancesResponse = { ok(fairTurnFixture()) }

        assertThat(vm.spark("a")).isTrue()

        assertThat(vm.deck.value.sparksPaused).isTrue()
        assertThat(vm.deck.value.fairTurn).isEqualTo(FairTurnUi(owed = 6, limit = 6, paused = true))
        assertThat(vm.message.value).isNull()
        // The card came back; nobody was sparked.
        assertThat(cards(vm)).containsExactly("a")
        assertThat(vm.deck.value.leaving).isNull()
        assertThat(api.calls.count { it == "allowances" }).isEqualTo(reads + 1)
        // And the next spark is held back here.
        assertThat(vm.spark("a")).isFalse()
        assertThat(api.sparks).hasSize(1)
    }

    @Test
    fun `replying to matches lifts the pause on the next allowances read`() = runTest {
        api.pulse = listOf(card("a"))
        api.allowancesResponse = { ok(fairTurnFixture()) }
        val vm = pulse()
        assertThat(vm.deck.value.sparksPaused).isTrue()

        api.allowancesResponse = { ok(fairTurnFixture().copy(fairTurn = FairTurnDto(owed = 3, limit = 6))) }
        vm.refreshAllowances()

        assertThat(vm.deck.value.sparksPaused).isFalse()
        assertThat(vm.spark("a")).isTrue()
    }

    @Test
    fun `accepting a spark is never paused`() = runTest {
        api.allowancesResponse = { ok(fairTurnFixture()) }
        api.incoming = listOf(spark("s-1", "x"))
        val vm = SparksViewModel(repository, session, safety, photoUrls())

        val row = (vm.state.value as ListState.Items<IncomingSparkUi>).items.single()
        vm.accept(row)

        assertThat(api.accepts).containsExactly("s-1")
    }

    @Test
    fun `the fair turn words count what the server sent`() {
        assertThat(DeckCopy.fairTurnBody(FairTurnUi(owed = 6, limit = 6, paused = true))).startsWith("6 matches are waiting for your reply.")
        assertThat(DeckCopy.fairTurnBody(FairTurnUi(owed = 1, limit = 1, paused = true))).startsWith("1 match is waiting for your reply.")
        assertThat(DeckCopy.fairTurnBody(FairTurnUi(owed = 0, limit = 0, paused = true))).startsWith("Some of your matches")
    }

    // ── M12: dealbreakers ───────────────────────────────────────────────────

    private val store = ProfileOptionsStore(repository)

    private fun filters() = FiltersViewModel(repository, store, session)

    private fun dealbreakersFixture() = fixture("preferences_get_200_dealbreakers.json", PreferencesDto.serializer())

    /** The M6 filters flag on, so the pass section and its dealbreakers exist. */
    private fun withPassSection(active: Boolean, dealbreakers: List<String> = emptyList()) = PreferencesDto(
        userId = ME,
        minAge = 21,
        maxAge = 35,
        distanceKm = 25,
        distanceBucket = "km_10_25",
        passFilters = PassFiltersDto(active = active, verifiedOnly = true, languages = listOf("en")),
        dealbreakers = dealbreakers,
    )

    @Test
    fun `with no dealbreakers on the wire every switch is hidden and nothing is sent`() = runTest {
        // The fake's default preferences carry no `dealbreakers`: the mechanic is off.
        val vm = filters()

        assertThat(vm.state.value.dealbreakersOn).isFalse()
        Dealbreaker.entries.forEach { assertThat(vm.state.value.showsDealbreaker(it)).isFalse() }
        vm.setDealbreaker(Dealbreaker.AGE, true)
        assertThat(vm.state.value.draft.dealbreakers).isEmpty()

        vm.setAges(25, 40)
        vm.save()
        assertThat(api.preferenceWrites.single().dealbreakers).isNull()
    }

    @Test
    fun `dealbreakers load from the server, a switch shows only under a set preference`() = runTest {
        api.preferences = dealbreakersFixture()
        val vm = filters()

        val state = vm.state.value
        assertThat(state.dealbreakersOn).isTrue()
        assertThat(state.draft.dealbreakers).containsExactly(Dealbreaker.AGE, Dealbreaker.INTENT)
        assertThat(state.showsDealbreaker(Dealbreaker.AGE)).isTrue()
        assertThat(state.showsDealbreaker(Dealbreaker.DISTANCE)).isTrue()
        // No intent chosen: nothing for a dealbreaker to hold.
        assertThat(state.showsDealbreaker(Dealbreaker.INTENT)).isFalse()
        // The pass filters flag is off in this golden: no pass dealbreakers at all.
        assertThat(state.showsDealbreaker(Dealbreaker.VERIFIED)).isFalse()

        vm.toggleIntent("serious")
        assertThat(vm.state.value.showsDealbreaker(Dealbreaker.INTENT)).isTrue()
    }

    @Test
    fun `a change sends the whole list, in the server's order`() = runTest {
        api.preferences = dealbreakersFixture()
        api.preferencesWriteResponse = { ok(fixture("preferences_put_200_dealbreakers.json", PreferencesDto.serializer())) }
        val vm = filters()

        vm.toggleIntent("serious")
        vm.setDealbreaker(Dealbreaker.DISTANCE, true)
        vm.save()

        val sent = api.preferenceWrites.single()
        assertThat(sent.dealbreakers).containsExactly("age", "distance", "intent").inOrder()
        assertThat(sent.intentFilter).containsExactly("serious")
        assertThat(vm.state.value.savedCount).isEqualTo(1)
    }

    @Test
    fun `unchanged dealbreakers stay out of the body`() = runTest {
        api.preferences = dealbreakersFixture()
        val vm = filters()

        vm.setAges(30, 40)
        vm.save()

        assertThat(api.preferenceWrites.single().dealbreakers).isNull()
        assertThat(api.preferenceWrites.single().minAge).isEqualTo(30)
    }

    @Test
    fun `switching one off sends the list without it, and an empty list clears them`() = runTest {
        api.preferences = dealbreakersFixture().copy(dealbreakers = listOf("age"))
        val vm = filters()

        vm.setDealbreaker(Dealbreaker.AGE, false)
        vm.save()

        assertThat(api.preferenceWrites.single().dealbreakers).isEmpty()
    }

    @Test
    fun `clearing a preference takes its dealbreaker with it`() = runTest {
        api.preferences = dealbreakersFixture().copy(intentFilter = listOf("serious"), dealbreakers = listOf("age", "intent"))
        val vm = filters()
        assertThat(vm.state.value.showsDealbreaker(Dealbreaker.INTENT)).isTrue()

        vm.toggleIntent("serious")
        vm.save()

        assertThat(api.preferenceWrites.single().dealbreakers).containsExactly("age")
    }

    @Test
    fun `without a pass the pass dealbreakers are locked and open the upsell`() = runTest {
        api.preferences = withPassSection(active = false)
        val vm = filters()

        assertThat(vm.state.value.showsDealbreaker(Dealbreaker.VERIFIED)).isTrue()
        assertThat(vm.state.value.dealbreakerLocked(Dealbreaker.VERIFIED)).isTrue()
        // The free ones are not.
        assertThat(vm.state.value.dealbreakerLocked(Dealbreaker.AGE)).isFalse()

        vm.setDealbreaker(Dealbreaker.VERIFIED, true)

        assertThat(vm.state.value.upsell).isTrue()
        assertThat(vm.state.value.draft.dealbreakers).isEmpty()
        assertThat(api.preferenceWrites).isEmpty()
    }

    @Test
    fun `a stored pass dealbreaker can be switched off without a pass`() = runTest {
        api.preferences = withPassSection(active = false, dealbreakers = listOf("verified"))
        val vm = filters()

        vm.setDealbreaker(Dealbreaker.VERIFIED, false)
        vm.save()

        assertThat(vm.state.value.upsell).isFalse()
        assertThat(api.preferenceWrites.single().dealbreakers).isEmpty()
    }

    @Test
    fun `a pass holder marks a pass dealbreaker`() = runTest {
        api.preferences = withPassSection(active = true)
        val vm = filters()

        vm.setDealbreaker(Dealbreaker.LANGUAGES, true)
        vm.setDealbreaker(Dealbreaker.VERIFIED, true)
        vm.save()

        assertThat(api.preferenceWrites.single().dealbreakers).containsExactly("verified", "languages").inOrder()
    }

    @Test
    fun `403 DEALBREAKERS_REQUIRE_PASS opens the upsell and locks them`() = runTest {
        api.preferences = withPassSection(active = true)
        api.preferencesWriteResponse = { refusedWithFixture(403, "preferences_put_403_dealbreakers_require_pass.json") }
        val vm = filters()

        vm.setDealbreaker(Dealbreaker.VERIFIED, true)
        vm.save()

        val state = vm.state.value
        assertThat(state.upsell).isTrue()
        assertThat(state.passActive).isFalse()
        assertThat(state.saving).isFalse()
        assertThat(state.dealbreakerLocked(Dealbreaker.VERIFIED)).isTrue()
        assertThat(state.message).isNull()
    }

    @Test
    fun `400 INVALID_DEALBREAKER is said, not upsold`() = runTest {
        api.preferences = dealbreakersFixture()
        api.preferencesWriteResponse = { refusedWithFixture(400, "preferences_put_400_invalid_dealbreaker.json") }
        val vm = filters()

        vm.setDealbreaker(Dealbreaker.DISTANCE, true)
        vm.save()

        val state = vm.state.value
        assertThat(state.upsell).isFalse()
        assertThat(state.saving).isFalse()
        assertThat(state.message?.type).isEqualTo(UsMessageType.Error)
        assertThat(state.message?.text).isEqualTo(DatingCopy.forError(com.us.android.feature.dating.data.DatingError.Refused(400, "INVALID_DEALBREAKER", "", null)))
    }

    @Test
    fun `the mechanic switched off since the read hides the switches and saves the rest`() = runTest {
        api.preferences = dealbreakersFixture()
        var writes = 0
        api.preferencesWriteResponse = {
            writes++
            if (it.dealbreakers != null) refusedWithFixture(404, "preferences_put_404_dealbreakers_not_enabled.json") else ok(dealbreakersFixture().copy(dealbreakers = null))
        }
        val vm = filters()

        vm.setAges(30, 40)
        vm.setDealbreaker(Dealbreaker.DISTANCE, true)
        vm.save()

        assertThat(writes).isEqualTo(2)
        val retry = api.preferenceWrites.last()
        assertThat(retry.dealbreakers).isNull()
        assertThat(retry.minAge).isEqualTo(30)
        assertThat(vm.state.value.dealbreakersOn).isFalse()
        assertThat(vm.state.value.savedCount).isEqualTo(1)
    }

    @Test
    fun `the mechanic switched off with only dealbreakers changed says so and sends nothing more`() = runTest {
        api.preferences = dealbreakersFixture()
        api.preferencesWriteResponse = { refusedWithFixture(404, "preferences_put_404_dealbreakers_not_enabled.json") }
        val vm = filters()

        vm.setDealbreaker(Dealbreaker.DISTANCE, true)
        vm.save()

        assertThat(api.preferenceWrites).hasSize(1)
        assertThat(vm.state.value.dealbreakersOn).isFalse()
        assertThat(vm.state.value.saving).isFalse()
        assertThat(vm.state.value.message?.text).isEqualTo(FiltersCopy.DEALBREAKERS_GONE)
    }

    // ── M14: after-date check-ins ───────────────────────────────────────────

    private fun checkIns(handle: SavedStateHandle = SavedStateHandle()) = DateCheckInViewModel(handle, repository, safety)

    private fun asks() = fixture("date_checkins_get_200.json", ListSerializer(DateCheckinDto.serializer()))

    private val asha = CheckInTarget(matchId = "<match>", userId = "<other>", name = "Asha")

    @Test
    fun `check-ins are hidden while the server flag is off`() = runTest {
        // The fake's default is the golden 404 MECHANIC_NOT_ENABLED.
        val vm = checkIns()

        assertThat(vm.state.value.phase).isEqualTo(CheckInPhase.HIDDEN)
        assertThat(vm.state.value.available).isFalse()
        vm.open(asha)
        assertThat(vm.state.value.sheet).isNull()
    }

    @Test
    fun `pending asks become cards with the person's first name`() = runTest {
        api.dateCheckinsResponse = { ok(asks()) }
        val vm = checkIns()

        assertThat(vm.state.value.phase).isEqualTo(CheckInPhase.READY)
        assertThat(vm.state.value.prompts).containsExactly(asha)
    }

    @Test
    fun `a nameless ask falls back to general words`() = runTest {
        api.dateCheckinsResponse = { ok(listOf(DateCheckinDto(matchId = "m", person = PastMatchPersonDto(userId = "u")))) }
        val vm = checkIns()

        assertThat(vm.state.value.prompts.single().name).isNull()
        assertThat(com.us.android.feature.dating.home.CheckInCopy.cardBody(null)).isEqualTo("Tell us how your date went. It only takes a moment.")
    }

    @Test
    fun `met no sends only met, and the ask goes`() = runTest {
        api.dateCheckinsResponse = { ok(asks()) }
        api.dateFeedbackResponse = { _, body -> ok(DateFeedbackDto(matchId = "<match>", met = body.met)) }
        val vm = checkIns()

        vm.open(asha)
        assertThat(vm.state.value.sheet?.canSend).isFalse()
        vm.chooseMet(DateMet.YES)
        vm.chooseAgain(DateAgain.YES)
        // Changing to "no" drops the follow-ups, as the server requires.
        vm.chooseMet(DateMet.NO)
        vm.send()

        assertThat(api.dateFeedbackWrites.single()).isEqualTo("<match>" to DateFeedbackRequest(met = "no"))
        assertThat(vm.state.value.sheet).isNull()
        assertThat(vm.state.value.prompts).isEmpty()
        assertThat(vm.state.value.message?.type).isEqualTo(UsMessageType.Success)
    }

    @Test
    fun `after yes the two follow-ups go along, and either can be left out`() = runTest {
        api.dateCheckinsResponse = { ok(asks()) }
        val vm = checkIns()

        vm.open(asha)
        vm.chooseMet(DateMet.YES)
        vm.chooseAgain(DateAgain.UNSURE)
        vm.chooseFeltSafe(true)
        // Tapping the chosen answer again clears it.
        vm.chooseAgain(DateAgain.UNSURE)
        vm.send()

        assertThat(api.dateFeedbackWrites.single().second).isEqualTo(DateFeedbackRequest(met = "yes", again = null, feltSafe = true))
        // The golden 201: offer_report false.
        assertThat(vm.state.value.sheet).isNull()
    }

    @Test
    fun `not feeling safe offers support and the report flow for that person`() = runTest {
        api.dateCheckinsResponse = { ok(asks()) }
        api.dateFeedbackResponse = { _, _ -> ok(fixture("date_feedback_post_201_unsafe.json", DateFeedbackDto.serializer())) }
        val vm = checkIns()

        vm.open(asha)
        vm.chooseMet(DateMet.YES)
        vm.chooseAgain(DateAgain.NO)
        vm.chooseFeltSafe(false)
        vm.send()

        assertThat(api.dateFeedbackWrites.single().second).isEqualTo(DateFeedbackRequest(met = "yes", again = "no", feltSafe = false))
        val sheet = checkNotNull(vm.state.value.sheet)
        assertThat(sheet.offerReport).isTrue()
        assertThat(sheet.canSend).isFalse()
        assertThat(com.us.android.feature.dating.home.CheckInCopy.reportAction(sheet.target.name)).isEqualTo("Report Asha")

        vm.startReport()
        assertThat(vm.state.value.sheet).isNull()
        assertThat(vm.state.value.reporting).isEqualTo(asha)

        vm.report(ReportDraft(targetId = "<other>", reason = ReportReason.HARASSMENT))
        assertThat(api.reports.single().targetId).isEqualTo("<other>")
        assertThat(vm.state.value.reporting).isNull()
        assertThat(vm.state.value.reported).isEqualTo(1)
        assertThat(session.isRemoved("<other>")).isTrue()
    }

    @Test
    fun `400 INVALID_DATE_FEEDBACK is said in the sheet and the choices stay`() = runTest {
        api.dateCheckinsResponse = { ok(asks()) }
        api.dateFeedbackResponse = { _, _ -> refusedWithFixture(400, "date_feedback_post_400_invalid.json") }
        val vm = checkIns()

        vm.open(asha)
        vm.chooseMet(DateMet.NOT_YET)
        vm.send()

        val sheet = checkNotNull(vm.state.value.sheet)
        assertThat(sheet.error).isNotNull()
        assertThat(sheet.met).isEqualTo(DateMet.NOT_YET)
        assertThat(sheet.sending).isFalse()
        assertThat(vm.state.value.prompts).containsExactly(asha)
    }

    @Test
    fun `429 DATE_FEEDBACK_LIMIT reads as already answered`() = runTest {
        api.dateCheckinsResponse = { ok(asks()) }
        api.dateFeedbackResponse = { _, _ -> refused(429, "DATE_FEEDBACK_LIMIT") }
        val vm = checkIns()

        vm.open(asha)
        vm.chooseMet(DateMet.YES)
        vm.send()

        assertThat(vm.state.value.sheet).isNull()
        assertThat(vm.state.value.prompts).isEmpty()
        assertThat(vm.state.value.message?.text).isEqualTo(DatingCopy.DATE_FEEDBACK_LIMIT)
    }

    @Test
    fun `404 MECHANIC_NOT_ENABLED on the answer hides everything`() = runTest {
        api.dateCheckinsResponse = { ok(asks()) }
        api.dateFeedbackResponse = { _, _ -> refusedWithFixture(404, "date_feedback_post_404_not_enabled.json") }
        val vm = checkIns()

        vm.open(asha)
        vm.chooseMet(DateMet.YES)
        vm.send()

        assertThat(vm.state.value.phase).isEqualTo(CheckInPhase.HIDDEN)
        assertThat(vm.state.value.sheet).isNull()
        assertThat(vm.state.value.prompts).isEmpty()
    }

    @Test
    fun `the match screen offers it unasked, for any match`() = runTest {
        api.dateCheckinsResponse = { ok(emptyList()) }
        val vm = checkIns(SavedStateHandle(mapOf("matchId" to "m-9")))
        val target = CheckInTarget("m-9", "u-9", "Ravi")

        assertThat(vm.state.value.available).isTrue()
        // No link flag: knowing the match opens nothing by itself.
        vm.matchKnown(target)
        assertThat(vm.state.value.sheet).isNull()

        vm.open(target)
        assertThat(vm.state.value.sheet?.target).isEqualTo(target)
    }

    @Test
    fun `the checkin link opens the sheet once the match is known, and only once`() = runTest {
        api.dateCheckinsResponse = { ok(emptyList()) }
        val vm = checkIns(SavedStateHandle(mapOf("matchId" to "m-9", "checkIn" to true)))
        val target = CheckInTarget("m-9", "u-9", "Ravi")
        assertThat(vm.state.value.sheet).isNull()

        vm.matchKnown(target)
        assertThat(vm.state.value.sheet?.target).isEqualTo(target)

        vm.dismissSheet()
        vm.matchKnown(target)
        assertThat(vm.state.value.sheet).isNull()
    }

    @Test
    fun `the checkin link opens from the pending ask even before the match loads`() = runTest {
        api.dateCheckinsResponse = { ok(asks()) }
        val vm = checkIns(SavedStateHandle(mapOf("matchId" to "<match>", "checkIn" to true)))

        assertThat(vm.state.value.sheet?.target).isEqualTo(asha)
    }

    @Test
    fun `the checkin link does nothing while the mechanic is off`() = runTest {
        val vm = checkIns(SavedStateHandle(mapOf("matchId" to "m-9", "checkIn" to true)))

        vm.matchKnown(CheckInTarget("m-9", "u-9", "Ravi"))
        assertThat(vm.state.value.sheet).isNull()
    }

    @Test
    fun `the deep link's checkin flag is read from its query`() {
        assertThat(datingCheckInRequested("/dating/matches/m-1?checkin=1")).isTrue()
        assertThat(datingCheckInRequested("/dating/matches/m-1?x=2&checkin=1#top")).isTrue()
        assertThat(datingCheckInRequested("/dating/matches/m-1")).isFalse()
        assertThat(datingCheckInRequested("/dating/matches/m-1?checkin=0")).isFalse()
        assertThat(datingCheckInRequested(null)).isFalse()
    }

    // ── M19: past matches ───────────────────────────────────────────────────

    private fun past() = PastMatchesViewModel(repository, safety)

    private fun pastRow(id: String, user: String, name: String = "", ended: String = "unmatched", reported: Boolean = false) =
        PastMatchDto(matchId = id, person = PastMatchPersonDto(userId = user, firstName = name), ended = ended, reported = reported, endedAt = "2026-09-12T10:00:00Z")

    @Test
    fun `past matches are hidden while the server flag is off`() = runTest {
        val vm = past()

        assertThat(vm.state.value.phase).isEqualTo(PastMatchesPhase.HIDDEN)
    }

    @Test
    fun `past matches list from the golden with the window`() = runTest {
        api.pastMatchesResponse = { Response.success(testJson.decodeFromString(PastMatchesDto.serializer(), fixtureText("past_matches_get_200.json"))) }
        val vm = past()

        val state = vm.state.value
        assertThat(state.phase).isEqualTo(PastMatchesPhase.READY)
        assertThat(state.windowDays).isEqualTo(30)
        val row = state.items.single()
        assertThat(row.name).isEqualTo("Asha")
        assertThat(row.userId).isEqualTo("<other>")
        assertThat(row.reported).isFalse()
        assertThat(PastMatchesCopy.intro(state.windowDays)).contains("last 30 days")
    }

    @Test
    fun `a nameless row reads Someone, and each ending has its own words`() = runTest {
        api.pastMatchesResponse = {
            Response.success(
                PastMatchesDto(
                    data = listOf(pastRow("m1", "u1"), pastRow("m2", "u2", "Ravi", ended = "blocked", reported = true), pastRow("m3", "", "Ghost")),
                    meta = PastMatchesMetaDto(windowDays = 30),
                ),
            )
        }
        val vm = past()

        val items = vm.state.value.items
        // A row with no person id cannot be reported, so it is left out.
        assertThat(items.map { it.matchId }).containsExactly("m1", "m2").inOrder()
        assertThat(items[0].name).isEqualTo(PastMatchesCopy.SOMEONE)
        assertThat(items[1].reported).isTrue()
        val utc = ZoneId.of("UTC")
        assertThat(PastMatchesCopy.endedLine("unmatched", Instant.parse("2026-09-12T10:00:00Z"), utc)).isEqualTo("Unmatched on 12 Sep")
        assertThat(PastMatchesCopy.endedLine("blocked", null, utc)).isEqualTo("Blocked")
        assertThat(PastMatchesCopy.endedLine("expired", null, utc)).isEqualTo("Expired")
        assertThat(PastMatchesCopy.endedLine("closed", null, utc)).isEqualTo("Ended")
        assertThat(PastMatchesCopy.endedLine("something_new", null, utc)).isEqualTo("Ended")
    }

    @Test
    fun `report opens the report flow for that person and the row then reads Reported`() = runTest {
        api.pastMatchesResponse = { Response.success(PastMatchesDto(data = listOf(pastRow("m1", "u1", "Asha")))) }
        val vm = past()

        vm.startReport("m1")
        assertThat(vm.state.value.reporting?.userId).isEqualTo("u1")

        vm.report(ReportDraft(targetId = "u1", reason = ReportReason.SCAM))

        assertThat(api.reports.single().targetId).isEqualTo("u1")
        assertThat(api.reports.single().reason).isEqualTo("scam")
        assertThat(vm.state.value.reporting).isNull()
        assertThat(vm.state.value.items.single().reported).isTrue()
        assertThat(vm.state.value.message?.type).isEqualTo(UsMessageType.Success)
    }

    @Test
    fun `a row already reported offers no report`() = runTest {
        api.pastMatchesResponse = { Response.success(PastMatchesDto(data = listOf(pastRow("m1", "u1", reported = true)))) }
        val vm = past()

        vm.startReport("m1")

        assertThat(vm.state.value.reporting).isNull()
    }

    @Test
    fun `a failed report keeps the row reportable and says why`() = runTest {
        api.pastMatchesResponse = { Response.success(PastMatchesDto(data = listOf(pastRow("m1", "u1")))) }
        api.reportResponse = { refused(429, "REPORT_RATE_LIMITED") }
        val vm = past()

        vm.startReport("m1")
        vm.report(ReportDraft(targetId = "u1", reason = ReportReason.SPAM))

        assertThat(vm.state.value.items.single().reported).isFalse()
        assertThat(vm.state.value.message?.type).isEqualTo(UsMessageType.Error)
    }

    @Test
    fun `a failed first read offers a retry`() = runTest {
        api.pastMatchesResponse = { rawRefusedWithFixture(503, "premium_purchase_post_503_unavailable.json") }
        val vm = past()
        assertThat(vm.state.value.phase).isEqualTo(PastMatchesPhase.FAILED)

        api.pastMatchesResponse = { Response.success(PastMatchesDto()) }
        vm.refresh()
        assertThat(vm.state.value.phase).isEqualTo(PastMatchesPhase.READY)
        assertThat(vm.state.value.items).isEmpty()
    }
}
