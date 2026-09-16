package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiConfig
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.MatchesViewModel
import com.us.android.feature.dating.home.PersonState
import com.us.android.feature.dating.home.PersonViewModel
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.SparksViewModel
import com.us.android.feature.dating.network.DetailPromptDto
import com.us.android.feature.dating.network.ProfileDetailDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The pre-match `detail` block on the three surfaces that decide about someone:
 * the deck card, an incoming spark and the person view.
 *
 * The rule that matters most here is PER PHOTO: the server applies the D6
 * variant to each photo's OWN visibility, so one gallery can mix a full photo
 * with a blurred one, and the app must never upgrade the blurred one.
 */
class PersonDetailTest {

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

    // ── the deck card ───────────────────────────────────────────────────────

    @Test
    fun `the deck card shows the bio and the prompt answers`() = runTest {
        api.pulse = listOf(card(other, detail = detail()))
        val pulse = PulseViewModel(repository, session, safety, urls)

        val detail = checkNotNull((pulse.state.value as ListState.Items).items.single().detail)

        assertThat(detail.bio).isEqualTo("Filter coffee and long drives.")
        assertThat(detail.prompts.map { it.promptId }).containsExactly(1, 2).inOrder()
        assertThat(detail.prompts.first().question).isEqualTo("My ideal Sunday is...")
        assertThat(detail.prompts.first().answer).isEqualTo("Dosa and a bookshop.")
        assertThat(detail.languages).containsExactly("telugu", "english").inOrder()
    }

    @Test
    fun `the deck card keeps the distance bucket and the match reasons`() = runTest {
        api.pulse = listOf(card(other, bucket = "km_5_10", detail = detail()))
        val pulse = PulseViewModel(repository, session, safety, urls)

        val row = (pulse.state.value as ListState.Items).items.single()

        // The detail block did not displace what the card already showed.
        assertThat(row.distance).isEqualTo("5–10 km")
        assertThat(row.detail).isNotNull()
    }

    @Test
    fun `the gallery shows every photo, primary first`() = runTest {
        api.pulse = listOf(
            card(
                other,
                detail = detail(
                    photos = listOf(
                        galleryPhoto("p-primary", "full"),
                        galleryPhoto("p-second", "full"),
                        galleryPhoto("p-third", "blurred"),
                    ),
                ),
            ),
        )
        val pulse = PulseViewModel(repository, session, safety, urls)

        val gallery = checkNotNull((pulse.state.value as ListState.Items).items.single().detail).gallery

        assertThat(gallery.map { it.photoId }).containsExactly("p-primary", "p-second", "p-third").inOrder()
    }

    // ── the per-photo rule ──────────────────────────────────────────────────

    @Test
    fun `a blurred photo in a gallery never yields a full url while a full one does`() = runTest {
        api.pulse = listOf(
            card(
                other,
                detail = detail(
                    photos = listOf(galleryPhoto("p-open", "full"), galleryPhoto("p-sealed", "blurred")),
                ),
            ),
        )
        val pulse = PulseViewModel(repository, session, safety, urls)

        val gallery = checkNotNull((pulse.state.value as ListState.Items).items.single().detail).gallery

        // The SAME gallery, two verdicts: the server decided per photo.
        assertThat(gallery.first().url).endsWith("/full")
        assertThat(gallery.last().url).endsWith("/blurred")
        assertThat(gallery.last().url).doesNotContain("/full")
    }

    @Test
    fun `an unknown or missing photo state in a gallery fails closed to blurred`() = runTest {
        api.pulse = listOf(
            card(
                other,
                detail = detail(
                    photos = listOf(
                        galleryPhoto("p-blank", "").copy(url = "/v1/dating/photos/p-blank/full"),
                        galleryPhoto("p-shouty", "FULL").copy(url = "/v1/dating/photos/p-shouty/full"),
                        galleryPhoto("p-future", "partial").copy(url = "/v1/dating/photos/p-future/full"),
                    ),
                ),
            ),
        )
        val pulse = PulseViewModel(repository, session, safety, urls)

        val gallery = checkNotNull((pulse.state.value as ListState.Items).items.single().detail).gallery

        // The url SAID full; only the state decides, and anything unrecognised is blurred.
        assertThat(gallery).hasSize(3)
        gallery.forEach { assertThat(it.url).doesNotContain("/full") }
    }

    // ── nothing rendered for an empty field ─────────────────────────────────

    @Test
    fun `an empty bio or empty prompts renders nothing`() = runTest {
        api.pulse = listOf(
            card(
                other,
                detail = ProfileDetailDto(
                    bio = "   ",
                    prompts = listOf(DetailPromptDto(promptId = 3, question = "", answer = "")),
                    languages = listOf(" "),
                    photos = listOf(galleryPhoto("p-1", "full")),
                ),
            ),
        )
        val pulse = PulseViewModel(repository, session, safety, urls)

        val detail = checkNotNull((pulse.state.value as ListState.Items).items.single().detail)

        // No blank heading can appear: there is nothing to head.
        assertThat(detail.bio).isNull()
        assertThat(detail.prompts).isEmpty()
        assertThat(detail.languages).isEmpty()
        assertThat(detail.gallery).hasSize(1)
    }

    @Test
    fun `a detail block with nothing in it is no detail at all`() = runTest {
        api.pulse = listOf(card(other, detail = ProfileDetailDto()))
        val pulse = PulseViewModel(repository, session, safety, urls)

        assertThat((pulse.state.value as ListState.Items).items.single().detail).isNull()
    }

    @Test
    fun `a card the server sent no detail for still renders`() = runTest {
        api.pulse = listOf(card(other))
        val pulse = PulseViewModel(repository, session, safety, urls)

        val row = (pulse.state.value as ListState.Items).items.single()

        assertThat(row.detail).isNull()
        assertThat(row.photoUrl).isNotNull()
    }

    // ── the incoming spark and the person view ──────────────────────────────

    @Test
    fun `an incoming spark carries the same detail as the deck`() = runTest {
        api.incoming = listOf(spark("s-1", other, person(other, detail = detail())))
        val sparks = SparksViewModel(repository, session, safety, urls)

        val detail = checkNotNull((sparks.state.value as ListState.Items).items.single().detail)

        assertThat(detail.bio).isEqualTo("Filter coffee and long drives.")
        assertThat(detail.prompts).hasSize(2)
        assertThat(detail.gallery.single().url).endsWith("/full")
    }

    @Test
    fun `the person view shows the detail, gallery included`() = runTest {
        api.people = mapOf(
            other to person(
                other,
                name = "Asha",
                detail = detail(photos = listOf(galleryPhoto("p-1", "full"), galleryPhoto("p-2", "blurred"))),
            ),
        )
        val view = PersonViewModel(SavedStateHandle(mapOf("userId" to other)), repository, urls)

        val loaded = view.state.value as PersonState.Loaded

        assertThat(loaded.person.name).isEqualTo("Asha")
        val detail = checkNotNull(loaded.person.detail)
        assertThat(detail.bio).isEqualTo("Filter coffee and long drives.")
        assertThat(detail.gallery.map { it.photoId }).containsExactly("p-1", "p-2").inOrder()
        assertThat(detail.gallery.last().url).doesNotContain("/full")
    }

    @Test
    fun `a person the viewer may not see is gone, not an error`() = runTest {
        api.people = emptyMap()
        val view = PersonViewModel(SavedStateHandle(mapOf("userId" to other)), repository, urls)

        assertThat(view.state.value).isInstanceOf(PersonState.Gone::class.java)
    }

    // ── the compact surfaces stay compact ───────────────────────────────────

    @Test
    fun `the match list neither receives nor synthesises a detail block`() = runTest {
        api.matches = listOf(match("m-1", other, person(other)))
        val matches = MatchesViewModel(repository, session, urls)

        val row = (matches.state.value as ListState.Items).items.single()

        // MatchUi has no detail at all: a safety surface carries no gallery.
        assertThat(row.photoUrl).isNotNull()
        assertThat(row.name).isNotNull()
    }
}
