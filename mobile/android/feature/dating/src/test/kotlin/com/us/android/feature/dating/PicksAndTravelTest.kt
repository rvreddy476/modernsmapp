package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.home.CardUi
import com.us.android.feature.dating.home.HomeTab
import com.us.android.feature.dating.home.PersonState
import com.us.android.feature.dating.home.PersonViewModel
import com.us.android.feature.dating.home.PicksCopy
import com.us.android.feature.dating.home.PicksState
import com.us.android.feature.dating.home.PicksViewModel
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.incomingSparkUi
import com.us.android.feature.dating.home.toCardUi
import com.us.android.feature.dating.home.visibleTabs
import com.us.android.feature.dating.network.PassRequest
import com.us.android.feature.dating.network.PicksDto
import com.us.android.feature.dating.network.PicksMetaDto
import com.us.android.feature.dating.network.SparkCreatedDto
import com.us.android.feature.dating.network.SparkRequest
import com.us.android.feature.dating.network.TravelCityDto
import com.us.android.feature.dating.network.TravelDto
import com.us.android.feature.dating.network.TravelTripDto
import com.us.android.feature.dating.safety.SafetyActions
import com.us.android.feature.dating.travel.TravelCopy
import com.us.android.feature.dating.travel.TravelPhase
import com.us.android.feature.dating.travel.TravelRules
import com.us.android.feature.dating.travel.TravelViewModel
import com.us.android.feature.dating.travel.visitingLabel
import androidx.lifecycle.SavedStateHandle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.jsonObject
import org.junit.Rule
import org.junit.Test
import retrofit2.Response
import java.time.Instant
import java.time.ZoneId

/**
 * Mechanics M7 (daily picks) and M8 (travel mode): what the picks show and
 * when they hide, the zone retry, the source each action carries, the travel
 * screen's states and refusals, and the travelling marker on other people.
 */
class PicksAndTravelTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)
    private val urls = photoUrls()
    private val kolkata = ZoneId.of("Asia/Kolkata")
    private val zone = object : DeviceZone() {
        override fun zone(): ZoneId = kolkata
    }

    private fun picks() = PicksViewModel(repository, session, safety, urls, zone)
    private fun pulse() = PulseViewModel(repository, session, safety, urls)
    private fun travel() = TravelViewModel(repository, session, zone)

    private fun servePicks(vararg cards: com.us.android.feature.dating.network.PulseCardDto, resetsAt: String? = "2026-10-02T18:30:00Z") {
        api.picksResponse = { Response.success(PicksDto(data = cards.toList(), meta = PicksMetaDto(date = "2026-10-03", timezone = "Asia/Kolkata", resetsAt = resetsAt, size = cards.size))) }
    }

    private fun loaded(viewModel: PicksViewModel): PicksState.Loaded = viewModel.state.value as PicksState.Loaded

    private fun ids(viewModel: PicksViewModel): List<String> = loaded(viewModel).cards.map(CardUi::userId)

    private val travelFixture get() = fixture("travel_get_200.json", TravelDto.serializer())

    private fun travelWithPass(active: TravelTripDto? = null) = travelFixture.copy(available = true, active = active)

    // ── Daily picks (M7) ────────────────────────────────────────────────────

    @Test
    fun `picks load in the server's order, each person once`() = runTest {
        servePicks(card("c"), card("a"), card("b"), card("a"), card(""))
        val picks = picks()

        // The server's order holds all day: no client re-sorting.
        assertThat(ids(picks)).containsExactly("c", "a", "b").inOrder()
        assertThat(loaded(picks).resetsAt).isEqualTo(Instant.parse("2026-10-02T18:30:00Z"))
        assertThat(picks.visible.value).isTrue()
    }

    @Test
    fun `never more than the daily ceiling`() = runTest {
        servePicks(*(1..14).map { card("p$it") }.toTypedArray())
        assertThat(ids(picks())).hasSize(PicksViewModel.MAX_PICKS)
    }

    @Test
    fun `the device zone goes with the read`() = runTest {
        servePicks(card("a"))
        picks()
        assertThat(api.picksReads).containsExactly("Asia/Kolkata")
    }

    @Test
    fun `no picks today is an empty state with the time of the next set`() = runTest {
        servePicks(resetsAt = "2026-10-02T18:30:00Z")
        val picks = picks()

        val state = loaded(picks)
        assertThat(state.cards).isEmpty()
        val morning = Instant.parse("2026-10-02T10:00:00Z")
        assertThat(PicksCopy.emptyBody(state.resetsAt, morning, kolkata)).isEqualTo("A fresh set arrives at 12:00 AM tomorrow.")
        assertThat(PicksCopy.resetLine(state.resetsAt, morning, kolkata)).isEqualTo("New picks at 12:00 AM tomorrow")
        // A reset already behind us is not offered as a time.
        assertThat(PicksCopy.resetLine(state.resetsAt, Instant.parse("2026-10-03T00:00:00Z"), kolkata)).isEqualTo("New picks every day at midnight")
    }

    @Test
    fun `picks are hidden when the mechanic is off, and stay hidden`() = runTest {
        // The fake's default is the server's 404 MECHANIC_NOT_ENABLED golden.
        val picks = picks()

        assertThat(picks.state.value).isEqualTo(PicksState.Hidden)
        assertThat(picks.visible.value).isFalse()
        assertThat(session.isMechanicDisabled(PicksViewModel.MECHANIC)).isTrue()
        assertThat(visibleTabs(picks = false)).doesNotContain(HomeTab.PICKS)

        picks.refresh()
        assertThat(api.picksReads).hasSize(1)
    }

    @Test
    fun `an unknown zone is retried once without one`() = runTest {
        api.picksResponse = { tz ->
            if (tz != null) rawRefusedWithFixture(400, "picks_get_400_invalid_timezone.json") else Response.success(PicksDto(data = listOf(card("a"))))
        }
        val picks = picks()

        assertThat(api.picksReads).containsExactly("Asia/Kolkata", null).inOrder()
        assertThat(ids(picks)).containsExactly("a")
    }

    @Test
    fun `the zone retry happens once, not in a loop`() = runTest {
        api.picksResponse = { rawRefusedWithFixture(400, "picks_get_400_invalid_timezone.json") }
        val picks = picks()

        assertThat(api.picksReads).containsExactly("Asia/Kolkata", null).inOrder()
        assertThat(picks.state.value).isInstanceOf(PicksState.Failed::class.java)
        // A failure still shows the tab, so a retry is offered.
        assertThat(picks.visible.value).isTrue()
    }

    @Test
    fun `a spark from picks says so and leaves the list`() = runTest {
        servePicks(card("a"), card("b"))
        val picks = picks()

        assertThat(picks.spark("a")).isTrue()

        val sent = api.sparks.single()
        assertThat(sent.toUserId).isEqualTo("a")
        assertThat(sent.source).isEqualTo("picks")
        assertThat(ids(picks)).containsExactly("b")
        // Nothing from the deck's daily allowance was touched.
        assertThat(api.calls).doesNotContain("pulse")
    }

    @Test
    fun `a pass from picks says so and leaves the list`() = runTest {
        servePicks(card("a"), card("b"))
        val picks = picks()

        picks.pass("b")

        assertThat(api.passes).containsExactly("b")
        assertThat(api.passBodies.single().source).isEqualTo("picks")
        assertThat(ids(picks)).containsExactly("a")
    }

    @Test
    fun `deck actions carry no source on the wire`() = runTest {
        api.pulse = listOf(card("a"), card("b"))
        val pulse = pulse()

        pulse.spark("a")
        pulse.pass("b")

        assertThat(api.sparks.single().source).isNull()
        assertThat(api.passBodies.single().source).isNull()
        // explicitNulls = false: the key is absent, so the server reads the deck.
        val sparkBody = testJson.encodeToJsonElement(SparkRequest.serializer(), api.sparks.single()).jsonObject
        assertThat(sparkBody.keys).doesNotContain("source")
        assertThat(testJson.encodeToJsonElement(PassRequest.serializer(), PassRequest()).jsonObject.keys).isEmpty()
        val picksBody = testJson.encodeToJsonElement(PassRequest.serializer(), PassRequest(source = "picks")).jsonObject
        assertThat(picksBody.keys).containsExactly("source")
    }

    @Test
    fun `a refused pick spark stays on the list`() = runTest {
        servePicks(card("a"))
        api.sparkResponse = { refused(429, "SPARK_RATE_LIMITED") }
        val picks = picks()

        picks.spark("a")

        assertThat(ids(picks)).containsExactly("a")
        assertThat(picks.message.value?.text).isEqualTo(DatingCopy.forError(com.us.android.feature.dating.data.DatingError.Refused(429, "SPARK_RATE_LIMITED", "", null)))
    }

    @Test
    fun `a pick who is gone leaves the list`() = runTest {
        servePicks(card("a"), card("b"))
        api.sparkResponse = { refusedWithFixture(404, "spark_create_404_candidate_unavailable.json") }
        val picks = picks()

        picks.spark("a")

        assertThat(ids(picks)).containsExactly("b")
    }

    @Test
    fun `a matched pick opens the celebration`() = runTest {
        servePicks(card("a"))
        api.sparkResponse = { ok(SparkCreatedDto(matchId = "m-1", matched = true)) }
        val picks = picks()

        picks.spark("a")

        assertThat(picks.celebration.value?.matchId).isEqualTo("m-1")
        assertThat(picks.celebration.value?.name).isEqualTo("Person a")
    }

    @Test
    fun `a blocked person leaves the picks`() = runTest {
        servePicks(card("a"), card("b"))
        val picks = picks()

        picks.block("a")

        assertThat(ids(picks)).containsExactly("b")
    }

    @Test
    fun `past the reset time the picks are read again`() = runTest {
        servePicks(card("a"), resetsAt = "2026-10-02T18:30:00Z")
        val picks = picks()

        picks.refreshIfStale(Instant.parse("2026-10-02T18:00:00Z"))
        assertThat(api.picksReads).hasSize(1)

        picks.refreshIfStale(Instant.parse("2026-10-02T18:31:00Z"))
        assertThat(api.picksReads).hasSize(2)
    }

    // ── Travel mode (M8) ────────────────────────────────────────────────────

    @Test
    fun `travel loads the cities alphabetically and is locked without a pass`() = runTest {
        api.travelResponse = {
            ok(travelFixture.copy(cities = listOf(TravelCityDto("pune", "Pune"), TravelCityDto("goa", "Goa"), TravelCityDto("", "Nowhere"), TravelCityDto("delhi", "Delhi"))))
        }
        val travel = travel()

        val state = travel.state.value
        assertThat(state.phase).isEqualTo(TravelPhase.READY)
        assertThat(state.cities.map { it.label }).containsExactly("Delhi", "Goa", "Pune").inOrder()
        assertThat(state.maxDays).isEqualTo(7)
        assertThat(state.locked).isTrue()
        assertThat(state.trip).isNull()
    }

    @Test
    fun `starting without a pass opens the upsell and asks nothing`() = runTest {
        api.travelResponse = { ok(travelFixture) }
        val travel = travel()
        travel.selectCity("goa")

        travel.start()

        assertThat(travel.state.value.upsell).isTrue()
        assertThat(api.travelWrites).isEmpty()
    }

    @Test
    fun `starting a trip sends the city and days, and the deck and picks read again`() = runTest {
        api.travelResponse = { ok(travelWithPass()) }
        servePicks(card("a"))
        api.pulse = listOf(card("x"))
        val pulse = pulse()
        val picks = picks()
        val travel = travel()
        val pulseReads = api.calls.count { it == "pulse" }
        val picksReads = api.picksReads.size

        travel.selectCity("mumbai")
        travel.setDays(5)
        travel.start()

        assertThat(api.travelWrites.single().city).isEqualTo("mumbai")
        assertThat(api.travelWrites.single().days).isEqualTo(5)
        val state = travel.state.value
        assertThat(state.trip?.cityLabel).isEqualTo("Mumbai")
        assertThat(state.saving).isFalse()
        assertThat(state.message?.text).isEqualTo("You're set. Browsing Mumbai.")
        // While travelling the deck and the picks are the destination's: both read afresh.
        assertThat(session.travelVersion.value).isEqualTo(1)
        assertThat(api.calls.count { it == "pulse" }).isEqualTo(pulseReads + 1)
        assertThat(api.picksReads.size).isEqualTo(picksReads + 1)
        assertThat(pulse.deck.value.travelEnabled).isTrue()
        assertThat(picks.state.value).isInstanceOf(PicksState.Loaded::class.java)
    }

    @Test
    fun `a trip needs a city first`() = runTest {
        api.travelResponse = { ok(travelWithPass()) }
        val travel = travel()

        travel.start()

        assertThat(travel.state.value.cityError).isEqualTo(TravelCopy.PICK_CITY)
        assertThat(api.travelWrites).isEmpty()
    }

    @Test
    fun `the days stepper stays within 1 and max days`() = runTest {
        api.travelResponse = { ok(travelWithPass().copy(maxDays = 4)) }
        val travel = travel()
        assertThat(travel.state.value.days).isEqualTo(TravelRules.DEFAULT_DAYS)

        repeat(10) { travel.moreDays() }
        assertThat(travel.state.value.days).isEqualTo(4)
        repeat(10) { travel.fewerDays() }
        assertThat(travel.state.value.days).isEqualTo(1)
        // Max days absent (Go omits 0): the server's own seven.
        assertThat(TravelRules.maxDays(TravelDto())).isEqualTo(7)
    }

    @Test
    fun `ending a trip goes home and reloads the deck`() = runTest {
        val trip = TravelTripDto(TravelCityDto("goa", "Goa"), "2026-10-01T10:00:00Z", "2026-10-04T10:00:00Z")
        api.travelResponse = { ok(travelWithPass(trip)) }
        api.pulse = listOf(card("x"))
        val pulse = pulse()
        val travel = travel()
        assertThat(travel.state.value.trip?.endsAt).isEqualTo(Instant.parse("2026-10-04T10:00:00Z"))
        assertThat(pulse.deck.value.trip?.cityLabel).isEqualTo("Goa")
        // From now on the server reports no trip.
        api.travelResponse = { ok(travelWithPass()) }

        travel.end()

        assertThat(api.calls).contains("travel:end")
        assertThat(travel.state.value.trip).isNull()
        assertThat(travel.state.value.message?.text).isEqualTo(TravelCopy.ENDED)
        assertThat(session.travelVersion.value).isEqualTo(1)
        assertThat(pulse.deck.value.trip).isNull()
    }

    @Test
    fun `a 403 locks the screen and opens the upsell`() = runTest {
        api.travelResponse = { ok(travelWithPass()) }
        api.travelWriteResponse = { refusedWithFixture(403, "travel_put_403_requires_pass.json") }
        val travel = travel()
        travel.selectCity("goa")

        travel.start()

        val state = travel.state.value
        assertThat(state.locked).isTrue()
        assertThat(state.upsell).isTrue()
        assertThat(state.saving).isFalse()
        assertThat(session.travelVersion.value).isEqualTo(0)
    }

    @Test
    fun `an invalid city narrows the picker to the server's list`() = runTest {
        api.travelResponse = { ok(travelWithPass().copy(cities = travelFixture.cities + TravelCityDto("atlantis", "Atlantis"))) }
        api.travelWriteResponse = { refusedWithFixture(400, "travel_put_400_invalid_city.json") }
        val travel = travel()
        travel.selectCity("atlantis")

        travel.start()

        val state = travel.state.value
        assertThat(state.city).isNull()
        assertThat(state.cityError).isEqualTo(TravelCopy.CITY_GONE)
        assertThat(state.cities.map { it.code }).doesNotContain("atlantis")
        assertThat(state.cities).hasSize(22)
    }

    @Test
    fun `invalid travel days take the bounds from the details`() = runTest {
        api.travelResponse = { ok(travelWithPass().copy(maxDays = 10)) }
        api.travelWriteResponse = { refused(400, "INVALID_TRAVEL_DAYS", """{"min":1,"max":5}""") }
        val travel = travel()
        travel.selectCity("goa")
        travel.setDays(9)

        travel.start()

        val state = travel.state.value
        assertThat(state.maxDays).isEqualTo(5)
        assertThat(state.days).isEqualTo(5)
        assertThat(state.daysError).isEqualTo("Trips run from 1 to 5 days.")
    }

    @Test
    fun `travel switched off shows the off state and hides the deck's entry`() = runTest {
        // The fake's default is the server's 404 MECHANIC_NOT_ENABLED golden.
        val pulse = pulse()
        val travel = travel()

        assertThat(travel.state.value.phase).isEqualTo(TravelPhase.OFF)
        assertThat(pulse.deck.value.travelEnabled).isFalse()
        assertThat(session.isMechanicDisabled(TravelRules.MECHANIC)).isTrue()
    }

    @Test
    fun `the deck knows the trip in effect for its banner`() = runTest {
        val trip = TravelTripDto(TravelCityDto("mumbai", "Mumbai"), "2026-10-01T10:00:00Z", "2026-10-09T10:00:00Z")
        api.travelResponse = { ok(travelWithPass(trip)) }
        val pulse = pulse()

        val deck = pulse.deck.value
        assertThat(deck.travelEnabled).isTrue()
        val ui = checkNotNull(deck.trip)
        assertThat(TravelCopy.browsingUntil(ui, kolkata)).isEqualTo("Browsing Mumbai until 9 Oct")
    }

    // ── The travelling marker ───────────────────────────────────────────────

    @Test
    fun `the travelling marker names the destination, and only while travelling`() {
        assertThat(visitingLabel(travelling = true, city = "Hyderabad")).isEqualTo("Visiting Hyderabad")
        assertThat(visitingLabel(travelling = true, city = " ")).isEqualTo("Visiting")
        assertThat(visitingLabel(travelling = false, city = "Hyderabad")).isNull()

        assertThat(card("a", city = "Pune", travelling = true).toCardUi(urls).visiting).isEqualTo("Visiting Pune")
        assertThat(card("a").toCardUi(urls).visiting).isNull()
    }

    @Test
    fun `the marker reaches liked-you tiles and the person screen`() = runTest {
        val traveller = person("t", city = "Goa", travelling = true)
        val spark = incomingSparkUi(sparkId = "s", fromUserId = "t", person = traveller, note = null, superSpark = false, urls = urls)
        assertThat(spark.visiting).isEqualTo("Visiting Goa")
        assertThat(incomingSparkUi("s2", "h", person("h"), null, false, urls).visiting).isNull()

        api.people = mapOf("t" to traveller)
        val person = PersonViewModel(SavedStateHandle(mapOf("userId" to "t")), repository, urls)
        assertThat((person.state.value as PersonState.Loaded).person.visiting).isEqualTo("Visiting Goa")
    }
}
