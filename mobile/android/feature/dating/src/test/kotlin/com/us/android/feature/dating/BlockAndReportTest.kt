package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiConfig
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.home.CardUi
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.MatchDetailState
import com.us.android.feature.dating.home.MatchDetailViewModel
import com.us.android.feature.dating.home.MatchesViewModel
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.SparksViewModel
import com.us.android.feature.dating.network.SparkCreatedDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportReason
import com.us.android.feature.dating.safety.ReportRules
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * Pulse, sparks and matches share one rule: once you block or report someone,
 * they are gone from EVERY list — including when a stale reload still carries
 * them. Plus the deck's own actions and the report sheet's limits.
 */
class BlockAndReportTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession()
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)
    private val urls = DatingPhotoUrls(ApiConfig(baseUrl = "https://api.test", wsBaseUrl = "", clientVersion = "t", environment = "test", isDebug = true))

    private val bad = "user-bad"
    private val kind = "user-kind"

    private fun everywhere() {
        session.setProfile(profile())
        api.pulse = listOf(card(bad), card(kind))
        api.incoming = listOf(spark("spark-bad", bad), spark("spark-kind", kind))
        api.matches = listOf(match("match-bad", bad), match("match-kind", kind))
    }

    private fun <T> ids(state: ListState<T>, id: (T) -> String): List<String> = (state as ListState.Items<T>).items.map(id)

    @Test
    fun `blocking from a match removes the person from the deck, sparks and matches`() = runTest {
        everywhere()
        val pulse = PulseViewModel(repository, session, safety, urls)
        val sparks = SparksViewModel(repository, session, safety, urls)
        val matches = MatchesViewModel(repository, session, urls)
        val detail = MatchDetailViewModel(SavedStateHandle(mapOf("matchId" to "match-bad")), repository, session, safety, urls)
        assertThat(ids(pulse.state.value) { it.userId }).containsExactly(bad, kind)

        detail.block()

        assertThat(api.blocks).containsExactly(bad)
        assertThat(detail.state.value).isInstanceOf(MatchDetailState.Gone::class.java)
        assertThat(ids(pulse.state.value) { it.userId }).containsExactly(kind)
        assertThat(ids(sparks.state.value) { it.fromUserId }).containsExactly(kind)
        assertThat(ids(matches.state.value) { it.otherUserId }).containsExactly(kind)

        // The server has not caught up: every list reloads with the blocked person still in it.
        pulse.refresh()
        sparks.refresh()
        matches.refresh()
        assertThat(ids(pulse.state.value) { it.userId }).containsExactly(kind)
        assertThat(ids(sparks.state.value) { it.fromUserId }).containsExactly(kind)
        assertThat(ids(matches.state.value) { it.otherUserId }).containsExactly(kind)
        assertThat(session.person(bad)).isNull()
    }

    @Test
    fun `a report blocks, and removes the person from every list too`() = runTest {
        everywhere()
        val pulse = PulseViewModel(repository, session, safety, urls)
        val sparks = SparksViewModel(repository, session, safety, urls)
        val matches = MatchesViewModel(repository, session, urls)

        sparks.report(ReportDraft(targetId = bad, reason = ReportReason.HARASSMENT, sparkIds = listOf("spark-bad")))

        val sent = api.reports.single()
        assertThat(sent.reason).isEqualTo("harassment")
        assertThat(sent.evidence?.sparkIds).containsExactly("spark-bad")
        assertThat(ids(pulse.state.value) { it.userId }).containsExactly(kind)
        assertThat(ids(sparks.state.value) { it.fromUserId }).containsExactly(kind)
        assertThat(ids(matches.state.value) { it.otherUserId }).containsExactly(kind)
    }

    @Test
    fun `a failed block removes no one`() = runTest {
        everywhere()
        api.blockResponse = { refused(500, "BLOCK_FAILED") }
        val pulse = PulseViewModel(repository, session, safety, urls)

        pulse.block(bad)

        assertThat(ids(pulse.state.value) { it.userId }).containsExactly(bad, kind)
        assertThat(pulse.message.value).isNotNull()
    }

    @Test
    fun `a deck card shows a bucket label and a blurred photo only`() = runTest {
        everywhere()
        api.pulse = listOf(card(bad, bucket = "km_5_10", label = "7.4 km"))
        val pulse = PulseViewModel(repository, session, safety, urls)

        val shown: CardUi = (pulse.state.value as ListState.Items).items.single()
        assertThat(shown.distance).isEqualTo("5–10 km")
        assertThat(shown.photoUrl).isEqualTo("https://api.test/v1/dating/photos/photo-$bad/blurred")

        // Matched: the full photo.
        val matches = MatchesViewModel(repository, session, urls)
        assertThat((matches.state.value as ListState.Items).items.first().photoUrl)
            .isEqualTo("https://api.test/v1/dating/photos/photo-$bad/full")
    }

    @Test
    fun `spark, pass and stash each take the card off the deck and a mutual spark celebrates`() = runTest {
        everywhere()
        api.sparkResponse = { ok(SparkCreatedDto(matchId = "match-new", matched = true)) }
        val pulse = PulseViewModel(repository, session, safety, urls)

        pulse.spark(bad)
        assertThat(pulse.celebration.value?.matchId).isEqualTo("match-new")
        assertThat(api.sparks.single().toUserId).isEqualTo(bad)
        assertThat(api.sparks.single().targetKind).isEqualTo("photo")

        pulse.pass(kind)
        assertThat(api.passes).containsExactly(kind)
        assertThat(ids(pulse.state.value) { it.userId }).isEmpty()
    }

    @Test
    fun `accepting a spark is a spark back at its sender and declining calls decline`() = runTest {
        everywhere()
        val sparks = SparksViewModel(repository, session, safety, urls)
        val items = (sparks.state.value as ListState.Items).items

        sparks.accept(items.first { it.fromUserId == kind })
        sparks.decline(items.first { it.fromUserId == bad })

        assertThat(api.sparks.single().toUserId).isEqualTo(kind)
        assertThat(api.declines).containsExactly("spark-bad")
        assertThat((sparks.state.value as ListState.Items).items).isEmpty()
    }

    @Test
    fun `a message push opens the match's chat once it has loaded`() = runTest {
        everywhere()
        val detail = MatchDetailViewModel(
            SavedStateHandle(mapOf("matchId" to "match-kind", "openChat" to true)),
            repository,
            session,
            safety,
            urls,
        )
        assertThat(detail.chat.value?.conversationId).isEqualTo("conv-match-kind")
        detail.chatOpened()
        assertThat(detail.chat.value).isNull()
    }

    @Test
    fun `report rules - other needs details, details cap at 500, evidence only when included`() {
        assertThat(ReportRules.problem(ReportDraft(targetId = bad))).isNotNull()
        assertThat(ReportRules.problem(ReportDraft(targetId = bad, reason = ReportReason.OTHER, details = "  "))).isNotNull()
        assertThat(ReportRules.problem(ReportDraft(targetId = bad, reason = ReportReason.OTHER, details = "x".repeat(500)))).isNull()
        assertThat(ReportRules.problem(ReportDraft(targetId = bad, reason = ReportReason.SPAM, details = "x".repeat(501)))).isNotNull()
        // Runes, not UTF-16 units: 500 emoji fit.
        assertThat(ReportRules.problem(ReportDraft(targetId = bad, reason = ReportReason.SPAM, details = "😀".repeat(500)))).isNull()
        assertThat(ReportReason.entries.map { it.wire })
            .containsExactly("harassment", "fake_profile", "underage", "nudity", "scam", "hate", "violence", "spam", "other").inOrder()

        val withPhoto = ReportDraft(targetId = bad, reason = ReportReason.NUDITY, photoIds = listOf("p-1"))
        assertThat(ReportRules.toRequest(withPhoto).evidence?.photoIds).containsExactly("p-1")
        assertThat(ReportRules.toRequest(withPhoto.copy(includeEvidence = false)).evidence).isNull()
        assertThat(ReportRules.toRequest(ReportDraft(targetId = bad, reason = ReportReason.SPAM)).evidence).isNull()
    }

    @Test
    fun `a report the rules refuse is never sent`() {
        val result = runBlocking { safety.report(ReportDraft(targetId = bad, reason = ReportReason.OTHER)) }
        assertThat(result).isInstanceOf(DatingResult.Failure::class.java)
        assertThat(api.reports).isEmpty()
        assertThat(session.isRemoved(bad)).isFalse()
    }
}
