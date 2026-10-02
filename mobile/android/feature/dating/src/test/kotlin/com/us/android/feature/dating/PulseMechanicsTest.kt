package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.home.AllowanceUi
import com.us.android.feature.dating.home.CardUi
import com.us.android.feature.dating.home.DeckCopy
import com.us.android.feature.dating.home.DeckUi
import com.us.android.feature.dating.home.IncomingSparkUi
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.SparkLimitUi
import com.us.android.feature.dating.home.SparksViewModel
import com.us.android.feature.dating.home.toUi
import com.us.android.feature.dating.network.AllowanceDto
import com.us.android.feature.dating.network.AllowancesDto
import com.us.android.feature.dating.network.PremiumMeDto
import com.us.android.feature.dating.network.RewindDto
import com.us.android.feature.dating.network.SparkDto
import com.us.android.feature.dating.network.SparkRequest
import com.us.android.feature.dating.network.SuperSparkAllowanceDto
import com.us.android.feature.dating.premium.balancesLine
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonObject
import org.junit.Rule
import org.junit.Test
import java.time.Instant
import java.time.ZoneId

/**
 * Mechanics M10 (allowances), M2 (undo a pass) and M3 (Super Spark) in the
 * deck's view model: what switches each on, when undo is offered, the card it
 * restores, and each allowance's own "out of" state.
 */
class PulseMechanicsTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)
    private val urls = photoUrls()
    private val kolkata = ZoneId.of("Asia/Kolkata")
    private val morning = Instant.parse("2026-10-02T10:00:00Z")

    private fun pulse() = PulseViewModel(repository, session, safety, urls)

    private fun cards(viewModel: PulseViewModel): List<String> =
        (viewModel.state.value as ListState.Items<CardUi>).items.map { it.userId }

    private fun allowancesFixture(name: String = "allowances_get_200.json") = fixture(name, AllowancesDto.serializer())

    private fun mechanicsOn(allowances: AllowancesDto = allowancesFixture()) {
        api.allowancesResponse = { ok(allowances) }
    }

    private val allowanceReads get() = api.calls.count { it == "allowances" }

    // ── Allowances: what switches a mechanic on ─────────────────────────────

    @Test
    fun `mechanics absent from the allowances are off`() = runTest {
        mechanicsOn(allowancesFixture("allowances_get_200_mechanics_off.json"))
        api.pulse = listOf(card("a"), card("b"))
        val pulse = pulse()

        pulse.pass("a")

        val deck = pulse.deck.value
        assertThat(deck.rewind).isNull()
        assertThat(deck.superSpark).isNull()
        assertThat(deck.superSparkEnabled).isFalse()
        // A pass the server took, and still no undo: the mechanic is off.
        assertThat(deck.canRewind).isFalse()
        assertThat(pulse.rewind()).isFalse()
        assertThat(DeckCopy.undosLeft(deck)).isNull()
        assertThat(DeckCopy.superSparksLeft(deck)).isNull()
        assertThat(api.calls).doesNotContain("rewind")
    }

    @Test
    fun `the allowances fixture switches undo and Super Spark on`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"))
        val deck = pulse().deck.value

        assertThat(deck.superSparkEnabled).isTrue()
        assertThat(deck.superSpark).isEqualTo(AllowanceUi(dailyLimit = 1, remaining = 1))
        assertThat(deck.superSparkBalance).isEqualTo(0)
        assertThat(deck.rewind).isEqualTo(AllowanceUi(dailyLimit = 1, remaining = 1))
        assertThat(DeckCopy.superSparksLeft(deck)).isEqualTo("1 Super Spark left today")
        assertThat(DeckCopy.undosLeft(deck)).isEqualTo("1 undo left today")
        // On, but not offered until a pass.
        assertThat(deck.canRewind).isFalse()
    }

    @Test
    fun `an unlimited allowance carries no counts`() {
        val unlimited = AllowanceDto(unlimited = true).toUi()
        assertThat(unlimited).isEqualTo(AllowanceUi(unlimited = true))
        // Counts the server should not send next to unlimited are ignored.
        assertThat(AllowanceDto(unlimited = true, dailyLimit = 5, remainingToday = 2).toUi()).isEqualTo(AllowanceUi(unlimited = true))
        assertThat(DeckCopy.undosLeft(DeckUi(rewind = unlimited))).isEqualTo("Unlimited undos")
    }

    @Test
    fun `remaining absent means none left`() {
        // Go omits remaining_today at 0.
        val spent = AllowanceDto(dailyLimit = 1).toUi()
        assertThat(spent.remaining).isEqualTo(0)
        assertThat(DeckCopy.undosLeft(DeckUi(rewind = spent))).isEqualTo("No undos left today")
        assertThat(DeckCopy.superSparksLeft(DeckUi(superSpark = spent))).isEqualTo("No Super Sparks left today")
        // Pack Super Sparks are spent once the daily one is gone.
        assertThat(DeckCopy.superSparksLeft(DeckUi(superSpark = spent, superSparkBalance = 3))).isEqualTo("3 Super Sparks from packs")
        assertThat(DeckCopy.superSparksLeft(DeckUi(superSpark = AllowanceUi(dailyLimit = 5, remaining = 2), superSparkBalance = 1)))
            .isEqualTo("2 Super Sparks left today · 1 from packs")
    }

    @Test
    fun `the purchased balance is read from the allowances`() = runTest {
        mechanicsOn(AllowancesDto(superSpark = SuperSparkAllowanceDto(dailyLimit = 1, purchasedBalance = 4, resetsAt = "2026-10-02T13:00:00Z")))
        val deck = pulse().deck.value

        assertThat(deck.superSpark).isEqualTo(AllowanceUi(dailyLimit = 1, remaining = 0, resetsAt = Instant.parse("2026-10-02T13:00:00Z")))
        assertThat(deck.superSparkBalance).isEqualTo(4)
        assertThat(DeckCopy.superSparksLeft(deck)).isEqualTo("4 Super Sparks from packs")
    }

    @Test
    fun `a failed allowances read keeps the last answer`() = runTest {
        mechanicsOn()
        val pulse = pulse()
        assertThat(pulse.deck.value.superSparkEnabled).isTrue()

        api.allowancesResponse = { offline() }
        pulse.refreshAllowances()

        assertThat(pulse.deck.value.superSparkEnabled).isTrue()
        assertThat(pulse.deck.value.rewind).isNotNull()
    }

    // ── Undo a pass (M2) ────────────────────────────────────────────────────

    @Test
    fun `undo is offered only while the last action was a pass the server took`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"), card("b"), card("c"), card("d"), card("e"))
        val pulse = pulse()
        assertThat(pulse.deck.value.canRewind).isFalse()

        pulse.spark("a")
        assertThat(pulse.deck.value.canRewind).isFalse()

        pulse.pass("b")
        assertThat(pulse.deck.value.canRewind).isTrue()
        assertThat(pulse.deck.value.rewindable).isEqualTo("b")

        // Anything after the pass makes it no longer the last action.
        pulse.stash("c")
        assertThat(pulse.deck.value.canRewind).isFalse()

        // A refused pass is no pass at all.
        api.passResponse = { refused(500, "INTERNAL") }
        pulse.pass("d")
        assertThat(pulse.deck.value.canRewind).isFalse()
    }

    @Test
    fun `undo puts the server's card back on top`() = runTest {
        mechanicsOn()
        val rewound = fixture("pulse_rewind_post_200.json", RewindDto.serializer())
        val passed = rewound.candidateId
        api.pulse = listOf(card(passed), card("b"))
        api.rewindResponse = { ok(rewound) }
        val pulse = pulse()
        pulse.pass(passed)
        assertThat(cards(pulse)).containsExactly("b")
        val readsBefore = allowanceReads

        assertThat(pulse.rewind()).isTrue()

        assertThat(api.calls.count { it == "rewind" }).isEqualTo(1)
        assertThat(cards(pulse)).containsExactly(passed, "b").inOrder()
        // The server's card, not the one this process had.
        assertThat((pulse.state.value as ListState.Items).items.first().lastActive).isEqualTo("Active today")
        val deck = pulse.deck.value
        // The pass marker must not send the card straight back out.
        assertThat(deck.leaving).isNull()
        // One step only.
        assertThat(deck.canRewind).isFalse()
        assertThat(pulse.busy.value).isNull()
        // The allowances are read again after the undo.
        assertThat(allowanceReads).isGreaterThan(readsBefore)
        assertThat(pulse.message.value?.text).isEqualTo("Pass undone.")
        // The card came with the answer: no refetch.
        assertThat(api.calls.count { it == "pulse" }).isEqualTo(1)
    }

    @Test
    fun `the allowance the undo returned is shown`() = runTest {
        // Read once at load, and never again, so the undo's own allowance is what stays.
        var reads = 0
        api.allowancesResponse = {
            reads++
            if (reads == 1) ok(allowancesFixture()) else offline()
        }
        val rewound = fixture("pulse_rewind_post_200.json", RewindDto.serializer())
        api.pulse = listOf(card(rewound.candidateId))
        api.rewindResponse = { ok(rewound) }
        val pulse = pulse()
        pulse.pass(rewound.candidateId)

        pulse.rewind()

        assertThat(pulse.deck.value.rewind).isEqualTo(AllowanceUi(dailyLimit = 1, remaining = 0))
        assertThat(DeckCopy.undosLeft(pulse.deck.value)).isEqualTo("No undos left today")
    }

    @Test
    fun `undo without a card refetches the deck`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"), card("b"))
        api.rewindResponse = { ok(RewindDto(rewound = true, candidateId = "a", allowance = AllowanceDto(dailyLimit = 1))) }
        val pulse = pulse()
        pulse.pass("a")
        assertThat(cards(pulse)).containsExactly("b")

        pulse.rewind()

        assertThat(api.calls.count { it == "pulse" }).isEqualTo(2)
        assertThat(cards(pulse)).containsExactly("a", "b").inOrder()
        assertThat(pulse.deck.value.leaving).isNull()
    }

    @Test
    fun `undo works from the empty deck`() = runTest {
        mechanicsOn()
        val rewound = fixture("pulse_rewind_post_200.json", RewindDto.serializer())
        api.pulse = listOf(card(rewound.candidateId))
        api.rewindResponse = { ok(rewound) }
        val pulse = pulse()
        pulse.pass(rewound.candidateId)
        assertThat(cards(pulse)).isEmpty()
        assertThat(pulse.deck.value.canRewind).isTrue()

        pulse.rewind()

        assertThat(cards(pulse)).containsExactly(rewound.candidateId)
    }

    @Test
    fun `out of undos is its own state with the reset time`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"), card("b"))
        api.rewindResponse = { refusedWithFixture(429, "pulse_rewind_429_limit_reached.json") }
        val pulse = pulse()
        pulse.pass("a")

        pulse.rewind()

        val limit = checkNotNull(pulse.deck.value.rewindLimit)
        // The golden's timestamp is a placeholder, so no time is named.
        assertThat(limit).isEqualTo(SparkLimitUi(limit = 1, windowHours = 24, resetsAt = null))
        assertThat(cards(pulse)).containsExactly("b")
        assertThat(pulse.message.value).isNull()
        // The pass is still there to undo once undos come back.
        assertThat(pulse.deck.value.canRewind).isTrue()
        assertThat(DeckCopy.outOfUndosBody(limit, morning, kolkata)).isEqualTo(
            "You can undo 1 pass every 24 hours. Undos come back within 24 hours. With a Premium pass you can undo as often as you like.",
        )

        pulse.dismissRewindLimit()
        assertThat(pulse.deck.value.rewindLimit).isNull()
    }

    @Test
    fun `an undo limit with a reset time names it on the viewer's clock`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"))
        api.rewindResponse = { refused(429, "REWIND_LIMIT_REACHED", """{"limit":1,"window_hours":24,"resets_at":"2026-10-02T13:00:00Z"}""") }
        val pulse = pulse()
        pulse.pass("a")

        pulse.rewind()

        assertThat(DeckCopy.outOfUndosBody(checkNotNull(pulse.deck.value.rewindLimit), morning, kolkata)).isEqualTo(
            "You can undo 1 pass every 24 hours. You can undo again from 6:30 PM today. With a Premium pass you can undo as often as you like.",
        )
    }

    @Test
    fun `nothing to undo hides the control`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"), card("b"))
        api.rewindResponse = { refusedWithFixture(409, "pulse_rewind_409_nothing_to_undo.json") }
        val pulse = pulse()
        pulse.pass("a")

        pulse.rewind()

        assertThat(pulse.deck.value.canRewind).isFalse()
        // The mechanic itself stays on: the next pass offers undo again.
        assertThat(pulse.deck.value.rewind).isNotNull()
        assertThat(pulse.deck.value.rewindLimit).isNull()
        assertThat(pulse.message.value).isNull()
        assertThat(cards(pulse)).containsExactly("b")
    }

    @Test
    fun `not enabled hides undo for the session`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"), card("b"), card("c"))
        api.rewindResponse = { refusedWithFixture(404, "pulse_rewind_404_not_enabled.json") }
        val pulse = pulse()
        pulse.pass("a")

        pulse.rewind()

        assertThat(pulse.deck.value.rewind).isNull()
        assertThat(pulse.deck.value.canRewind).isFalse()
        // The allowances still name it, and it stays hidden.
        pulse.refreshAllowances()
        pulse.pass("b")
        assertThat(pulse.deck.value.rewind).isNull()
        assertThat(pulse.deck.value.canRewind).isFalse()
        // And on the next visit, for the rest of the session.
        assertThat(pulse().deck.value.rewind).isNull()
        assertThat(api.calls.count { it == "rewind" }).isEqualTo(1)
    }

    @Test
    fun `an undo whose person is gone hides the control and says why`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"), card("b"))
        api.rewindResponse = { refused(404, "CANDIDATE_UNAVAILABLE") }
        val pulse = pulse()
        pulse.pass("a")

        pulse.rewind()

        assertThat(pulse.deck.value.canRewind).isFalse()
        assertThat(cards(pulse)).containsExactly("b")
        assertThat(pulse.message.value?.text).isEqualTo("This person isn't available any more.")
    }

    // ── Super Spark (M3) ────────────────────────────────────────────────────

    @Test
    fun `a Super Spark sends super true and an ordinary spark does not`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"), card("b"))
        api.sparkResponse = { ok(fixture("spark_create_post_201_super.json", com.us.android.feature.dating.network.SparkCreatedDto.serializer())) }
        val pulse = pulse()
        val readsBefore = allowanceReads

        assertThat(pulse.superSpark("a")).isTrue()

        val request = api.sparks.single()
        assertThat(request.superSpark).isTrue()
        assertThat(testJson.encodeToJsonElement(SparkRequest.serializer(), request).jsonObject["super"].toString()).isEqualTo("true")
        assertThat(cards(pulse)).containsExactly("b")
        assertThat(pulse.message.value?.text).isEqualTo("Super Spark sent. You'll be first in their sparks.")
        assertThat(allowanceReads).isGreaterThan(readsBefore)

        api.sparkResponse = { ok(com.us.android.feature.dating.network.SparkCreatedDto()) }
        pulse.spark("b")
        val plain = api.sparks.last()
        assertThat(plain.superSpark).isNull()
        assertThat(testJson.encodeToJsonElement(SparkRequest.serializer(), plain).jsonObject.keys).doesNotContain("super")
    }

    @Test
    fun `a Super Spark is counted down locally, daily allowance first and then packs`() = runTest {
        // One read at load; later reads fail, so the local count is what shows.
        var reads = 0
        api.allowancesResponse = {
            reads++
            if (reads == 1) ok(AllowancesDto(superSpark = SuperSparkAllowanceDto(dailyLimit = 1, remainingToday = 1, purchasedBalance = 2))) else offline()
        }
        api.pulse = listOf(card("a"), card("b"), card("c"))
        val pulse = pulse()

        pulse.superSpark("a")
        assertThat(pulse.deck.value.superSpark?.remaining).isEqualTo(0)
        assertThat(pulse.deck.value.superSparkBalance).isEqualTo(2)

        pulse.superSpark("b")
        assertThat(pulse.deck.value.superSparkBalance).isEqualTo(1)
        assertThat(DeckCopy.superSparksLeft(pulse.deck.value)).isEqualTo("1 Super Spark from packs")
    }

    @Test
    fun `out of Super Sparks is its own state with the reset time and the pack balance`() = runTest {
        // A stale read said some were left; the refusal is the truth, and later reads fail.
        var reads = 0
        api.allowancesResponse = {
            reads++
            if (reads == 1) ok(AllowancesDto(superSpark = SuperSparkAllowanceDto(dailyLimit = 1, remainingToday = 1, purchasedBalance = 2))) else offline()
        }
        api.pulse = listOf(card("a"))
        api.sparkResponse = { refusedWithFixture(429, "spark_create_429_super_limit_reached.json") }
        val pulse = pulse()

        pulse.superSpark("a")

        val deck = pulse.deck.value
        val limit = checkNotNull(deck.superSparkLimit)
        assertThat(limit).isEqualTo(SparkLimitUi(limit = 1, windowHours = 24, resetsAt = null))
        // The server says the daily one AND every pack one are used.
        assertThat(deck.superSparkBalance).isEqualTo(0)
        assertThat(deck.superSpark?.remaining).isEqualTo(0)
        // The card comes back, and the pane replaces the message line.
        assertThat(cards(pulse)).containsExactly("a")
        assertThat(deck.leaving).isNull()
        assertThat(pulse.message.value).isNull()
        assertThat(DeckCopy.outOfSuperSparksBody(limit, deck.superSparkBalance, morning, kolkata)).isEqualTo(
            "You get 1 Super Spark every 24 hours. More arrive within 24 hours. Super Sparks from packs: 0. A pack adds more straight away.",
        )
        assertThat(
            DeckCopy.outOfSuperSparksBody(SparkLimitUi(1, 24, Instant.parse("2026-10-02T13:00:00Z")), 0, morning, kolkata),
        ).isEqualTo("You get 1 Super Spark every 24 hours. More arrive from 6:30 PM today. Super Sparks from packs: 0. A pack adds more straight away.")

        pulse.dismissSuperSparkLimit()
        assertThat(pulse.deck.value.superSparkLimit).isNull()
    }

    @Test
    fun `Super Spark is hidden and refused when absent from the allowances`() = runTest {
        mechanicsOn(allowancesFixture("allowances_get_200_mechanics_off.json"))
        api.pulse = listOf(card("a"))
        val pulse = pulse()

        assertThat(pulse.deck.value.superSparkEnabled).isFalse()
        assertThat(pulse.superSpark("a")).isFalse()
        assertThat(api.sparks).isEmpty()
        assertThat(cards(pulse)).containsExactly("a")
    }

    @Test
    fun `a Super Spark refused as not enabled switches it off for the session`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"))
        api.sparkResponse = { refusedWithFixture(404, "pulse_rewind_404_not_enabled.json") }
        val pulse = pulse()

        pulse.superSpark("a")

        assertThat(pulse.deck.value.superSparkEnabled).isFalse()
        assertThat(cards(pulse)).containsExactly("a")
        pulse.refreshAllowances()
        assertThat(pulse.deck.value.superSparkEnabled).isFalse()
        assertThat(pulse.superSpark("a")).isFalse()
        assertThat(api.sparks).hasSize(1)
    }

    @Test
    fun `a Super Spark that matches celebrates like a spark`() = runTest {
        mechanicsOn()
        api.pulse = listOf(card("a"))
        api.matches = listOf(match("match-1", "a", person("a", name = "Asha")))
        api.sparkResponse = { ok(com.us.android.feature.dating.network.SparkCreatedDto(matchId = "match-1", matched = true)) }
        val pulse = pulse()

        pulse.superSpark("a")

        assertThat(pulse.celebration.value?.name).isEqualTo("Asha")
        assertThat(pulse.message.value).isNull()
    }

    // ── Incoming Super Sparks ───────────────────────────────────────────────

    @Test
    fun `incoming Super Sparks are marked and keep the server's order`() = runTest {
        val rows = testJson.decodeFromString(
            com.us.android.core.network.ApiEnvelope.serializer(ListSerializer(SparkDto.serializer())),
            fixtureText("sparks_incoming_get_200_super_first.json"),
        ).data.orEmpty()
        // Distinct senders, so the list keys stay distinct.
        api.incoming = rows.mapIndexed { i, row -> row.copy(id = "s-$i", fromUserId = "u-$i") }
        val sparks = SparksViewModel(repository, session, safety, urls)

        val items = (sparks.state.value as ListState.Items<IncomingSparkUi>).items
        assertThat(items.map { it.superSpark }).containsExactly(true, false).inOrder()
        assertThat(items.map { it.sparkId }).containsExactly("s-0", "s-1").inOrder()
    }

    // ── Premium ─────────────────────────────────────────────────────────────

    @Test
    fun `the Super Spark balance sits beside the Boost balance`() {
        assertThat(balancesLine(boosts = 1, superSparks = 3)).isEqualTo("Boosts: 1 · Super Sparks: 3")
        assertThat(balancesLine(boosts = 0, superSparks = 3)).isEqualTo("Super Sparks: 3")
        assertThat(balancesLine(boosts = 1, superSparks = 0)).isEqualTo("Boosts: 1")
        assertThat(balancesLine(boosts = 0, superSparks = 0)).isNull()
        // Omitted at 0 on the wire.
        val me = testJson.decodeFromString(PremiumMeDto.serializer(), """{"is_premium":false,"super_spark_balance":3}""")
        assertThat(me.superSparkBalance).isEqualTo(3)
        assertThat(testJson.decodeFromString(PremiumMeDto.serializer(), """{"is_premium":false}""").superSparkBalance).isEqualTo(0)
    }
}
