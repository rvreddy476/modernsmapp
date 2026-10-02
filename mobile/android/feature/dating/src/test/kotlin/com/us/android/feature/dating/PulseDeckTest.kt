package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.home.CardUi
import com.us.android.feature.dating.home.DeckCopy
import com.us.android.feature.dating.home.DeckExit
import com.us.android.feature.dating.home.DeckLeaving
import com.us.android.feature.dating.home.DeckUi
import com.us.android.feature.dating.home.HelloTarget
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.SparkLimitUi
import com.us.android.feature.dating.home.SparksViewModel
import com.us.android.feature.dating.network.PulseMetaDto
import com.us.android.feature.dating.network.PulseTodayDto
import com.us.android.feature.dating.network.SparkCreatedDto
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import retrofit2.Response
import java.time.Instant
import java.time.ZoneId

/**
 * Mechanic M1 in the view model: the refilling deck and its allowance, the
 * card that leaves only when the server says so, the out-of-sparks state and
 * the match screen's state.
 */
class PulseDeckTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)
    private val urls = photoUrls()

    private fun pulse() = PulseViewModel(repository, session, safety, urls)

    private fun cards(viewModel: PulseViewModel): List<String> =
        (viewModel.state.value as ListState.Items<CardUi>).items.map { it.userId }

    private fun batch(vararg ids: String, limit: Int = 0, remaining: Int = 0, resetsAt: String? = null) = PulseTodayDto(
        data = ids.map { card(it) },
        meta = PulseMetaDto(size = ids.size, dailyLimit = limit, remainingToday = remaining, resetsAt = resetsAt),
    )

    /** Serves [batches] in order; the last one repeats. */
    private fun serve(vararg batches: PulseTodayDto) {
        val queue = ArrayDeque(batches.toList())
        api.pulseResponse = { Response.success(if (queue.size > 1) queue.removeFirst() else queue.first()) }
    }

    private fun fixtureBatch(name: String): PulseTodayDto = testJson.decodeFromString(PulseTodayDto.serializer(), fixtureText(name))

    private val pulseCalls get() = api.calls.count { it == "pulse" }

    // ── The refetch rule ─────────────────────────────────────────────────────

    @Test
    fun `the next batch is fetched when the stack runs empty and cards remain`() = runTest {
        serve(batch("a", "b", limit = 25, remaining = 10), batch("c", limit = 25, remaining = 8))
        val pulse = pulse()
        assertThat(pulse.deck.value.dailyLimit).isEqualTo(25)
        assertThat(pulse.deck.value.remaining).isEqualTo(10)

        // One card still on screen: nothing is fetched.
        assertThat(pulse.pass("a")).isTrue()
        assertThat(pulseCalls).isEqualTo(1)
        assertThat(cards(pulse)).containsExactly("b")

        pulse.spark("b")

        assertThat(pulseCalls).isEqualTo(2)
        assertThat(cards(pulse)).containsExactly("c")
        // The server's number replaces the local countdown.
        assertThat(pulse.deck.value.remaining).isEqualTo(8)
        assertThat(pulse.deck.value.refilling).isFalse()
    }

    @Test
    fun `nothing is refetched when the meta carries no daily limit`() = runTest {
        // The server's refill flag is off: the deck behaves exactly as before.
        api.pulse = listOf(card("a"))
        val pulse = pulse()

        pulse.pass("a")

        assertThat(pulseCalls).isEqualTo(1)
        assertThat(cards(pulse)).isEmpty()
        assertThat(pulse.deck.value.metered).isFalse()
        assertThat(pulse.deck.value.outOfCards).isFalse()
        assertThat(DeckCopy.cardsLeft(pulse.deck.value)).isNull()
    }

    @Test
    fun `nothing is refetched when the last meta said none remain`() = runTest {
        // daily_limit present, remaining_today omitted: none left.
        serve(batch("a", limit = 25))
        val pulse = pulse()

        pulse.pass("a")

        assertThat(pulseCalls).isEqualTo(1)
        assertThat(cards(pulse)).isEmpty()
        assertThat(pulse.deck.value.outOfCards).isTrue()
    }

    @Test
    fun `a refill that finds no one stops there`() = runTest {
        serve(batch("a", limit = 25, remaining = 10), batch(limit = 25, remaining = 9))
        val pulse = pulse()

        pulse.pass("a")

        // One refill, an empty answer, and no loop: all caught up, not out of cards.
        assertThat(pulseCalls).isEqualTo(2)
        assertThat(cards(pulse)).isEmpty()
        assertThat(pulse.deck.value.outOfCards).isFalse()
    }

    @Test
    fun `a refill that fails says why instead of all caught up`() = runTest {
        var served = false
        api.pulseResponse = {
            if (served) throw java.io.IOException("offline")
            served = true
            Response.success(batch("a", limit = 25, remaining = 10))
        }
        val pulse = pulse()

        pulse.pass("a")

        assertThat(pulse.deck.value.refilling).isFalse()
        assertThat(pulse.message.value?.text).isEqualTo(DatingCopy.NETWORK)
    }

    @Test
    fun `sparks and passes count the allowance down, saving for later does not`() = runTest {
        serve(batch("a", "b", "c", "d", limit = 25, remaining = 10))
        val pulse = pulse()

        pulse.pass("a")
        pulse.spark("b")
        assertThat(pulse.deck.value.remaining).isEqualTo(8)
        assertThat(DeckCopy.cardsLeft(pulse.deck.value)).isEqualTo("8 cards left today")

        pulse.stash("c")
        assertThat(pulse.deck.value.remaining).isEqualTo(8)
        assertThat(cards(pulse)).containsExactly("d")
    }

    // ── Out of cards ─────────────────────────────────────────────────────────

    @Test
    fun `the out-of-cards fixture is the out-of-cards state`() = runTest {
        serve(fixtureBatch("pulse_today_get_200_out_of_cards.json"))
        val pulse = pulse()

        assertThat(cards(pulse)).isEmpty()
        val deck = pulse.deck.value
        assertThat(deck.dailyLimit).isEqualTo(2)
        assertThat(deck.remaining).isEqualTo(0)
        assertThat(deck.outOfCards).isTrue()
        // The golden's timestamp is a placeholder: it parses to nothing, and
        // the pane then names no time rather than a wrong one.
        assertThat(deck.resetsAt).isNull()
        assertThat(DeckCopy.cardsLeft(deck)).isNull()
        assertThat(DeckCopy.outOfCardsBody(deck, Instant.parse("2026-10-02T10:00:00Z"), ZoneId.of("Asia/Kolkata")))
            .isEqualTo("You've been through all 2 of today's cards. More arrive over the next day.")
    }

    @Test
    fun `spending the last cards of the refill fixture ends on out of cards`() = runTest {
        val refill = fixtureBatch("pulse_today_get_200_refill.json")
        serve(refill, fixtureBatch("pulse_today_get_200_out_of_cards.json"))
        val pulse = pulse()
        assertThat(DeckCopy.cardsLeft(pulse.deck.value)).isEqualTo("2 cards left today")

        pulse.pass(refill.data.single().profile.userId)

        assertThat(pulseCalls).isEqualTo(2)
        assertThat(cards(pulse)).isEmpty()
        assertThat(pulse.deck.value.outOfCards).isTrue()
    }

    @Test
    fun `the reset time is read from the meta and shown on the viewer's clock`() = runTest {
        serve(batch(limit = 25, resetsAt = "2026-10-02T13:00:00Z"))
        val deck = pulse().deck.value
        val kolkata = ZoneId.of("Asia/Kolkata")

        assertThat(deck.resetsAt).isEqualTo(Instant.parse("2026-10-02T13:00:00Z"))
        assertThat(DeckCopy.outOfCardsBody(deck, Instant.parse("2026-10-02T10:00:00Z"), kolkata))
            .isEqualTo("You've been through all 25 of today's cards. More arrive from 6:30 PM today.")
        // Late in the evening the same moment is tomorrow's.
        assertThat(DeckCopy.whenLabel(Instant.parse("2026-10-02T20:00:00Z"), Instant.parse("2026-10-02T17:00:00Z"), kolkata))
            .isEqualTo("1:30 AM tomorrow")
        // A time already behind us is not offered.
        assertThat(DeckCopy.whenLabel(deck.resetsAt!!, Instant.parse("2026-10-02T13:00:00Z"), kolkata)).isNull()
    }

    @Test
    fun `an allowance the server did not send is read as none`() {
        val none = DeckUi()
        assertThat(none.metered).isFalse()
        assertThat(none.outOfCards).isFalse()
        assertThat(DeckCopy.cardsLeft(none)).isNull()
        assertThat(DeckCopy.cardsLeft(DeckUi(dailyLimit = 25, remaining = 1))).isEqualTo("1 card left today")
    }

    // ── The card leaves only when the server says so ─────────────────────────

    @Test
    fun `a refused spark brings the card back and says why`() = runTest {
        api.pulse = listOf(card("a"), card("b"))
        val gate = CompletableDeferred<Unit>()
        api.sparkGate = { gate.await() }
        api.sparkResponse = { refused(500, "INTERNAL") }
        val pulse = pulse()

        assertThat(pulse.spark("a")).isTrue()

        // In flight: the card is on its way out, and nothing else may start.
        assertThat(pulse.deck.value.leaving).isEqualTo(DeckLeaving("a", DeckExit.SPARK))
        assertThat(pulse.busy.value).isEqualTo("a")
        assertThat(pulse.pass("b")).isFalse()
        assertThat(api.passes).isEmpty()

        gate.complete(Unit)

        assertThat(cards(pulse)).containsExactly("a", "b").inOrder()
        assertThat(pulse.deck.value.leaving).isNull()
        assertThat(pulse.busy.value).isNull()
        assertThat(pulse.message.value?.type).isEqualTo(UsMessageType.Error)
    }

    @Test
    fun `a refused pass brings the card back too`() = runTest {
        api.pulse = listOf(card("a"))
        api.passResponse = { refusedWithFixture(400, "pulse_pass_400_reason_too_long.json") }
        val pulse = pulse()

        pulse.pass("a")

        assertThat(cards(pulse)).containsExactly("a")
        assertThat(pulse.deck.value.leaving).isNull()
        assertThat(pulse.message.value).isNotNull()
    }

    @Test
    fun `a card whose person is gone is dropped, not brought back`() = runTest {
        api.pulse = listOf(card("a"), card("b"))
        api.sparkResponse = { refusedWithFixture(404, "spark_create_404_candidate_unavailable.json") }
        val pulse = pulse()

        pulse.spark("a")

        assertThat(cards(pulse)).containsExactly("b")
        assertThat(pulse.message.value?.text).isEqualTo("This person isn't available any more.")
    }

    @Test
    fun `an accepted spark takes the card and leaves no marker on the next one`() = runTest {
        api.pulse = listOf(card("a"), card("b"))
        val pulse = pulse()

        pulse.spark("a")

        assertThat(cards(pulse)).containsExactly("b")
        assertThat(pulse.deck.value.leaving?.userId).isNotEqualTo("b")
        assertThat(pulse.busy.value).isNull()
    }

    @Test
    fun `Super Spark is off and does nothing`() = runTest {
        api.pulse = listOf(card("a"))
        val pulse = pulse()

        assertThat(pulse.deck.value.superSparkEnabled).isFalse()
        assertThat(pulse.superSpark("a")).isFalse()
        assertThat(api.sparks).isEmpty()
        assertThat(cards(pulse)).containsExactly("a")
    }

    // ── Out of sparks ────────────────────────────────────────────────────────

    @Test
    fun `the spark limit is its own state, with the limit and window the server sent`() = runTest {
        api.pulse = listOf(card("a"))
        api.sparkResponse = { refusedWithFixture(429, "spark_create_429_rate_limited.json") }
        val pulse = pulse()

        pulse.spark("a")

        val limit = pulse.deck.value.sparkLimit
        assertThat(limit).isEqualTo(SparkLimitUi(limit = 50, windowHours = 24, resetsAt = null))
        // The card comes back, and the pane replaces the message line.
        assertThat(cards(pulse)).containsExactly("a")
        assertThat(pulse.deck.value.leaving).isNull()
        assertThat(pulse.message.value).isNull()
        assertThat(DeckCopy.outOfSparksBody(limit!!, Instant.parse("2026-10-02T10:00:00Z"), ZoneId.of("Asia/Kolkata")))
            .isEqualTo("You can send 50 sparks every 24 hours. They come back within 24 hours. You can still pass or save people for later.")

        pulse.dismissSparkLimit()
        assertThat(pulse.deck.value.sparkLimit).isNull()
    }

    @Test
    fun `a reset time in the spark limit is preferred over the window`() = runTest {
        api.pulse = listOf(card("a"))
        api.sparkResponse = {
            refused(429, "SPARK_RATE_LIMITED", """{"limit":50,"window_hours":24,"resets_at":"2026-10-02T13:00:00Z"}""")
        }
        val pulse = pulse()

        pulse.spark("a")

        val limit = pulse.deck.value.sparkLimit!!
        assertThat(limit.resetsAt).isEqualTo(Instant.parse("2026-10-02T13:00:00Z"))
        assertThat(DeckCopy.outOfSparksBody(limit, Instant.parse("2026-10-02T10:00:00Z"), ZoneId.of("Asia/Kolkata")))
            .isEqualTo("You can send 50 sparks every 24 hours. You can spark again from 6:30 PM today. You can still pass or save people for later.")
    }

    @Test
    fun `a spark limit with no details still has something to say`() = runTest {
        api.pulse = listOf(card("a"))
        api.sparkResponse = { refused(429, "SPARK_RATE_LIMITED") }
        val pulse = pulse()

        pulse.spark("a")

        val limit = pulse.deck.value.sparkLimit!!
        assertThat(limit).isEqualTo(SparkLimitUi(0, 0, null))
        assertThat(DeckCopy.outOfSparksBody(limit, Instant.EPOCH, ZoneId.of("UTC")))
            .isEqualTo("They come back soon. You can still pass or save people for later.")
    }

    // ── The match screen ─────────────────────────────────────────────────────

    @Test
    fun `a mutual spark celebrates with the match's own card and opens its chat`() = runTest {
        api.pulse = listOf(card("a"))
        api.matches = listOf(match("match-1", "a", person("a", name = "Asha")))
        api.sparkResponse = { ok(SparkCreatedDto(matchId = "match-1", matched = true)) }
        val pulse = pulse()

        pulse.spark("a")

        val celebration = checkNotNull(pulse.celebration.value)
        assertThat(celebration.matchId).isEqualTo("match-1")
        assertThat(celebration.name).isEqualTo("Asha")
        // The variant a MATCH may see, from the server's card for this viewer.
        assertThat(celebration.photoUrl).isEqualTo("https://api.test/v1/dating/photos/photo-a/full")
        assertThat(celebration.conversationId).isEqualTo("conv-match-1")
        assertThat(cards(pulse)).isEmpty()
        // The match screen stands in for the "Spark sent" line.
        assertThat(pulse.message.value).isNull()

        pulse.sayHello()

        assertThat(pulse.hello.value).isEqualTo(HelloTarget.Chat("conv-match-1", "Asha"))
        assertThat(pulse.celebration.value).isNull()
        pulse.helloHandled()
        assertThat(pulse.hello.value).isNull()
    }

    @Test
    fun `say hello goes to the match while its chat is not ready`() = runTest {
        api.pulse = listOf(card("a"))
        api.matches = listOf(match("match-1", "a").copy(conversationId = null))
        api.sparkResponse = { ok(SparkCreatedDto(matchId = "match-1", matched = true)) }
        val pulse = pulse()
        pulse.spark("a")
        assertThat(pulse.celebration.value?.conversationId).isNull()

        pulse.sayHello()

        assertThat(pulse.hello.value).isEqualTo(HelloTarget.Match("match-1"))
    }

    @Test
    fun `say hello asks once more for a chat that was created in the meantime`() = runTest {
        api.pulse = listOf(card("a"))
        api.matches = listOf(match("match-1", "a").copy(conversationId = null))
        api.sparkResponse = { ok(SparkCreatedDto(matchId = "match-1", matched = true)) }
        val pulse = pulse()
        pulse.spark("a")

        api.matches = listOf(match("match-1", "a"))
        pulse.sayHello()

        assertThat(pulse.hello.value).isEqualTo(HelloTarget.Chat("conv-match-1", "Person a"))
    }

    @Test
    fun `a match that cannot be read still celebrates with what the card showed`() = runTest {
        api.pulse = listOf(card("a"))
        api.sparkResponse = { ok(SparkCreatedDto(matchId = "match-1", matched = true)) }
        val pulse = pulse()

        pulse.spark("a")

        val celebration = checkNotNull(pulse.celebration.value)
        assertThat(celebration.name).isEqualTo("Person a")
        assertThat(celebration.photoUrl).isEqualTo("https://api.test/v1/dating/photos/photo-a/blurred")
        assertThat(celebration.conversationId).isNull()

        pulse.dismissCelebration()
        assertThat(pulse.celebration.value).isNull()
        assertThat(pulse.hello.value).isNull()
    }

    @Test
    fun `a spark sent back celebrates the same way`() = runTest {
        api.incoming = listOf(spark("s-1", "a", person("a", name = "Asha")))
        api.matches = listOf(match("match-1", "a", person("a", name = "Asha")))
        api.acceptResponse = { ok(SparkCreatedDto(matchId = "match-1", matched = true)) }
        val sparks = SparksViewModel(repository, session, safety, urls)

        sparks.accept((sparks.state.value as ListState.Items).items.single())

        assertThat(sparks.celebration.value?.name).isEqualTo("Asha")
        sparks.sayHello()
        assertThat(sparks.hello.value).isEqualTo(HelloTarget.Chat("conv-match-1", "Asha"))
    }

}
