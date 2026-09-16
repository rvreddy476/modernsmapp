package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiConfig
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.MatchDetailState
import com.us.android.feature.dating.home.MatchDetailViewModel
import com.us.android.feature.dating.home.MatchesViewModel
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.SparksViewModel
import com.us.android.feature.dating.home.personLine
import com.us.android.feature.dating.network.PulseMetaDto
import com.us.android.feature.dating.network.PulseTodayDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.photos.PhotoRules
import com.us.android.feature.dating.photos.PhotoVariant
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import retrofit2.Response

/**
 * The compact `person` the server now sends on matches, match detail and
 * incoming sparks: the name, age and distance BUCKET it carries reach the
 * screen, and its `photo_state` decides which image variant is loaded.
 */
class PersonCardTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)
    private val urls = DatingPhotoUrls(
        ApiConfig(baseUrl = "https://api.test", wsBaseUrl = "", clientVersion = "t", environment = "test", isDebug = true),
    )

    private val other = "user-other"

    @Test
    fun `a match shows the name, age and distance bucket the server sent`() = runTest {
        api.matches = listOf(match("m-1", other, person(other, name = "Asha", age = 30, bucket = "km_5_10")))
        val matches = MatchesViewModel(repository, session, urls)

        val row = (matches.state.value as ListState.Items).items.single()

        assertThat(row.name).isEqualTo("Asha")
        assertThat(row.age).isEqualTo(30)
        assertThat(row.distance).isEqualTo("5–10 km")
        assertThat(row.verified).isTrue()
        assertThat(personLine(row.name, row.age)).isEqualTo("Asha, 30")
    }

    @Test
    fun `an incoming spark shows the name, age and distance bucket the server sent`() = runTest {
        api.incoming = listOf(spark("s-1", other, person(other, name = "Asha", age = 30, bucket = "km_10_25")))
        val sparks = SparksViewModel(repository, session, safety, urls)

        val row = (sparks.state.value as ListState.Items).items.single()

        assertThat(row.name).isEqualTo("Asha")
        assertThat(row.age).isEqualTo(30)
        assertThat(row.distance).isEqualTo("10–25 km")
        assertThat(personLine(row.name, row.age)).isEqualTo("Asha, 30")
    }

    @Test
    fun `match detail shows the person without any deck loaded first`() = runTest {
        // Nothing was ever seen in this session's deck: the card is the server's.
        api.matches = listOf(match("m-1", other, person(other, name = "Asha", age = 30, bucket = "lt_5_km")))
        val detail = MatchDetailViewModel(SavedStateHandle(mapOf("matchId" to "m-1")), repository, session, safety, urls)

        val loaded = detail.state.value as MatchDetailState.Loaded

        assertThat(loaded.match.name).isEqualTo("Asha")
        assertThat(loaded.match.age).isEqualTo(30)
        assertThat(loaded.match.distance).isEqualTo("< 5 km")
        assertThat(loaded.match.otherUserId).isEqualTo(other)
    }

    @Test
    fun `an unknown distance bucket renders no distance at all`() = runTest {
        api.matches = listOf(match("m-1", other, person(other, bucket = "3.2 km")))
        val matches = MatchesViewModel(repository, session, urls)

        assertThat((matches.state.value as ListState.Items).items.single().distance).isNull()
    }

    // ── photo_state ─────────────────────────────────────────────────────────

    @Test
    fun `a blurred photo_state never renders the full image`() = runTest {
        api.matches = listOf(match("m-1", other, person(other, photoState = "blurred")))
        api.incoming = listOf(spark("s-1", other, person(other, photoState = "blurred")))
        val matches = MatchesViewModel(repository, session, urls)
        val sparks = SparksViewModel(repository, session, safety, urls)

        val matchPhoto = checkNotNull((matches.state.value as ListState.Items).items.single().photoUrl)
        val sparkPhoto = checkNotNull((sparks.state.value as ListState.Items).items.single().photoUrl)

        assertThat(matchPhoto).endsWith("/blurred")
        assertThat(matchPhoto).doesNotContain("/full")
        assertThat(sparkPhoto).endsWith("/blurred")
        assertThat(sparkPhoto).doesNotContain("/full")
    }

    @Test
    fun `a full photo_state renders the full image`() = runTest {
        api.matches = listOf(match("m-1", other, person(other, photoState = "full")))
        val matches = MatchesViewModel(repository, session, urls)

        assertThat((matches.state.value as ListState.Items).items.single().photoUrl).endsWith("/full")
    }

    @Test
    fun `an unknown or missing photo_state fails closed to blurred`() {
        assertThat(PhotoRules.variantForState("full")).isEqualTo(PhotoVariant.FULL)
        assertThat(PhotoRules.variantForState("blurred")).isEqualTo(PhotoVariant.BLURRED)
        // Anything else — blank, unknown, a future value — must not expose the photo.
        assertThat(PhotoRules.variantForState(null)).isEqualTo(PhotoVariant.BLURRED)
        assertThat(PhotoRules.variantForState("")).isEqualTo(PhotoVariant.BLURRED)
        assertThat(PhotoRules.variantForState("FULL")).isEqualTo(PhotoVariant.BLURRED)
        assertThat(PhotoRules.variantForState("unblurred")).isEqualTo(PhotoVariant.BLURRED)
        assertThat(PhotoRules.statePath("/v1/dating/photos/p-1/full", "unknown"))
            .isEqualTo("/v1/dating/photos/p-1/blurred")
    }

    @Test
    fun `a row without a person card still renders, with no name`() = runTest {
        api.matches = listOf(match("m-1", other, card = null))
        val matches = MatchesViewModel(repository, session, urls)

        val row = (matches.state.value as ListState.Items).items.single()

        assertThat(row.name).isNull()
        assertThat(row.age).isNull()
        assertThat(row.photoUrl).isNull()
        // otherOf still names the person, so blocking and sharing keep working.
        assertThat(row.otherUserId).isEqualTo(other)
        assertThat(personLine(row.name, row.age)).isNull()
    }

    // ── the pulse envelope ──────────────────────────────────────────────────

    @Test
    fun `cohort_gated is read from meta, from the top level, or from both`() = runTest {
        assertThat(PulseTodayDto(meta = PulseMetaDto(cohortGated = true)).gated).isTrue()
        // The older shape carried it only at the top level.
        assertThat(PulseTodayDto(cohortGated = true, meta = PulseMetaDto()).gated).isTrue()
        assertThat(PulseTodayDto(cohortGated = true).gated).isTrue()
        assertThat(PulseTodayDto(cohortGated = true, meta = PulseMetaDto(cohortGated = true)).gated).isTrue()
        assertThat(PulseTodayDto(meta = PulseMetaDto(cohortGated = false)).gated).isFalse()
        assertThat(PulseTodayDto().gated).isFalse()
    }

    @Test
    fun `a gated deck from meta alone reaches the screen as gated`() = runTest {
        api.pulseResponse = { Response.success(PulseTodayDto(data = emptyList(), meta = PulseMetaDto(size = 0, cohortGated = true))) }
        val pulse = PulseViewModel(repository, session, safety, urls)

        val state = pulse.state.value as ListState.Items
        assertThat(state.items).isEmpty()
        assertThat(state.gated).isTrue()
    }

    @Test
    fun `a gated deck from the top level alone reaches the screen as gated`() = runTest {
        api.pulseResponse = { Response.success(PulseTodayDto(data = emptyList(), meta = PulseMetaDto(size = 0), cohortGated = true)) }
        val pulse = PulseViewModel(repository, session, safety, urls)

        assertThat((pulse.state.value as ListState.Items).gated).isTrue()
    }
}
