package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.filters.FiltersField
import com.us.android.feature.dating.filters.FiltersViewModel
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.PersonState
import com.us.android.feature.dating.home.PersonViewModel
import com.us.android.feature.dating.home.PulseViewModel
import com.us.android.feature.dating.home.glanceInterests
import com.us.android.feature.dating.network.DatingPersonDto
import com.us.android.feature.dating.network.OptionDto
import com.us.android.feature.dating.network.PassFiltersDto
import com.us.android.feature.dating.network.PreferencesDto
import com.us.android.feature.dating.network.PrivacyDto
import com.us.android.feature.dating.network.ProfileDetailDto
import com.us.android.feature.dating.network.ProfileOptionsDto
import com.us.android.feature.dating.privacy.PrivacyViewModel
import com.us.android.feature.dating.profile.AboutMeField
import com.us.android.feature.dating.profile.AboutMePhase
import com.us.android.feature.dating.profile.AboutMeViewModel
import com.us.android.feature.dating.profile.BasicLine
import com.us.android.feature.dating.profile.LifestyleBasic
import com.us.android.feature.dating.profile.ProfileOptionsStore
import com.us.android.feature.dating.profile.ProfileOptionsUi
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * Mechanic M6: the option lists, "About me", Filters (free and with a pass),
 * the privacy switch that moved, and cards that show interests and basics by
 * the server's labels.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ProfileBasicsAndFiltersTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val repository = api.repository()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val store = ProfileOptionsStore(repository)

    private val goldenOptions = ProfileOptionsUi.from(fixture("profile_options_get_200.json", ProfileOptionsDto.serializer()))

    // ── the option lists ────────────────────────────────────────────────────

    @Test
    fun `the option lists are read once and kept for the session`() = runTest {
        val first = store.get()
        val second = store.get()

        assertThat(first).isNotNull()
        assertThat(second).isSameInstanceAs(first)
        assertThat(api.calls.count { it == "profile-options" }).isEqualTo(1)
    }

    @Test
    fun `a failed options read keeps nothing, so the next one asks again`() = runTest {
        api.profileOptionsResponse = { refused(503, "UNAVAILABLE") }
        assertThat(store.get()).isNull()

        api.profileOptionsResponse = { ok(fixture("profile_options_get_200.json", ProfileOptionsDto.serializer())) }
        assertThat(store.get()).isNotNull()
        assertThat(api.calls.count { it == "profile-options" }).isEqualTo(2)
    }

    @Test
    fun `options without a code or label are dropped and missing limits fall back`() {
        val ui = ProfileOptionsUi.from(
            ProfileOptionsDto(interests = listOf(OptionDto("", "Nameless"), OptionDto("art", ""), OptionDto("books", "Books"))),
        )

        assertThat(ui.interests.map { it.code }).containsExactly("books")
        assertThat(ui.maxInterests).isEqualTo(10)
        assertThat(ui.maxLanguages).isEqualTo(8)
        assertThat(ui.heightRange).isEqualTo(120..230)
    }

    // ── About me ────────────────────────────────────────────────────────────

    private fun aboutMe() = AboutMeViewModel(repository, store, session)

    private fun profileWithBasics() = profile().copy(
        interests = listOf("books", "astrology"),
        heightCm = 172,
        languagePrefs = listOf("en", "telugu"),
        drinking = "socially",
        smoking = "sometimes",
    )

    @Test
    fun `about me starts from the profile, keeping only codes the lists name`() = runTest {
        api.profileResponse = { ok(profileWithBasics()) }
        val vm = aboutMe()

        val state = vm.state.value
        assertThat(state.phase).isEqualTo(AboutMePhase.READY)
        assertThat(state.draft.interests).containsExactly("books")
        assertThat(state.draft.heightCm).isEqualTo(172)
        assertThat(state.draft.languages).containsExactly("en")
        assertThat(state.draft.basic(LifestyleBasic.DRINKING)).isEqualTo("socially")
        // "sometimes" is an exercise answer, not a smoking one.
        assertThat(state.draft.basic(LifestyleBasic.SMOKING)).isNull()
    }

    @Test
    fun `saving sends only what changed, and prefer not to say is an empty string`() = runTest {
        api.profileResponse = { ok(profileWithBasics()) }
        val vm = aboutMe()

        vm.toggleInterest("yoga")
        vm.setBasic(LifestyleBasic.DRINKING, null)
        vm.save()

        val sent = api.upserts.single()
        assertThat(sent.interests).containsExactly("books", "yoga").inOrder()
        assertThat(sent.drinking).isEqualTo("")
        // Untouched: left out, so "telugu" written before the fixed list survives.
        assertThat(sent.languagePrefs).isNull()
        assertThat(sent.heightCm).isNull()
        assertThat(sent.smoking).isNull()
        assertThat(vm.state.value.savedCount).isEqualTo(1)
    }

    @Test
    fun `the interest limit is kept before the server is asked`() = runTest {
        val vm = aboutMe()
        goldenOptions.interests.take(10).forEach { vm.toggleInterest(it.code) }

        vm.toggleInterest(goldenOptions.interests[10].code)

        assertThat(vm.state.value.draft.interests).hasSize(10)
        assertThat(vm.state.value.fieldErrors[AboutMeField.INTERESTS]).isEqualTo("You can pick up to 10 interests.")
    }

    @Test
    fun `a refused interest lands under the interests picker`() = runTest {
        api.upsertResponse = { refusedWithFixture(400, "profile_upsert_400_invalid_interest.json") }
        val vm = aboutMe()
        vm.toggleInterest("yoga")

        vm.save()

        val state = vm.state.value
        assertThat(state.fieldErrors.keys).containsExactly(AboutMeField.INTERESTS)
        assertThat(state.fieldErrors[AboutMeField.INTERESTS]).contains("interests")
        assertThat(state.message).isNull()
        assertThat(state.savedCount).isEqualTo(0)
    }

    @Test
    fun `a refused height lands under the height picker with the server's range`() = runTest {
        api.upsertResponse = { refusedWithFixture(400, "profile_upsert_400_invalid_height.json") }
        val vm = aboutMe()
        vm.setHeight(180)

        vm.save()

        assertThat(vm.state.value.fieldErrors[AboutMeField.HEIGHT]).isEqualTo("Height needs to be between 120 and 230 cm.")
    }

    @Test
    fun `about me with nothing changed sends nothing and closes`() = runTest {
        val vm = aboutMe()

        vm.save()

        assertThat(api.calls).doesNotContain("upsert")
        assertThat(vm.state.value.savedCount).isEqualTo(1)
    }

    // ── Filters ─────────────────────────────────────────────────────────────

    private fun filters() = FiltersViewModel(repository, store, session)

    private fun noPass(pass: PassFiltersDto = PassFiltersDto(active = false)) = PreferencesDto(
        userId = ME,
        minAge = 21,
        maxAge = 35,
        distanceKm = 25,
        interestedInGender = "man",
        distanceBucket = "km_10_25",
        passFilters = pass,
    )

    @Test
    fun `with the flag off there is no pass section and the distance stays numeric`() = runTest {
        // The default preferences carry no pass_filters: the flag is off.
        val vm = filters()

        assertThat(vm.state.value.flagOn).isFalse()
        assertThat(vm.state.value.locked).isFalse()

        vm.setDistanceKm(10)
        vm.save()

        val sent = api.preferenceWrites.single()
        assertThat(sent.distanceKm).isEqualTo(10)
        assertThat(sent.distanceBucket).isNull()
        assertThat(sent.passFilters).isNull()
        assertThat(api.calls).doesNotContain("profile-options")
    }

    @Test
    fun `filters load from the server's preferences`() = runTest {
        api.preferences = fixture("preferences_get_200_filters.json", PreferencesDto.serializer())
        val vm = filters()

        val state = vm.state.value
        assertThat(state.flagOn).isTrue()
        assertThat(state.passActive).isTrue()
        assertThat(state.locked).isFalse()
        assertThat(state.draft.minAge).isEqualTo(24)
        assertThat(state.draft.maxAge).isEqualTo(34)
        assertThat(state.draft.distanceBucket).isEqualTo("km_5_10")
        assertThat(state.draft.intents).containsExactly("serious")
        val pass = state.draft.pass
        assertThat(pass.verifiedOnly).isTrue()
        assertThat(pass.minHeightCm).isEqualTo(160)
        assertThat(pass.maxHeightCm).isEqualTo(190)
        assertThat(pass.languages).containsExactly("en", "te").inOrder()
        assertThat(pass.basic(LifestyleBasic.DRINKING)).containsExactly("never", "socially").inOrder()
        assertThat(pass.basic(LifestyleBasic.EXERCISE)).isEmpty()
    }

    @Test
    fun `saving a pass filter sends the whole set and the deck reloads`() = runTest {
        api.preferences = fixture("preferences_get_200_filters.json", PreferencesDto.serializer())
        api.preferencesWriteResponse = { ok(fixture("preferences_put_200_filters.json", PreferencesDto.serializer())) }
        val pulse = PulseViewModel(repository, session, SafetyActions(repository, session), photoUrls())
        val deckReads = api.calls.count { it == "pulse" }
        val vm = filters()

        vm.toggleBasic(LifestyleBasic.DIET, "vegan")
        vm.save()

        val pass = checkNotNull(api.preferenceWrites.single().passFilters)
        assertThat(pass.verifiedOnly).isTrue()
        assertThat(pass.minHeightCm).isEqualTo(160)
        assertThat(pass.languages).containsExactly("en", "te").inOrder()
        assertThat(pass.diet).containsExactly("vegetarian", "vegan").inOrder()
        assertThat(pass.exercise).isEmpty()
        // Only the pass set changed: the free fields stay out of the body.
        assertThat(api.preferenceWrites.single().minAge).isNull()
        assertThat(api.preferenceWrites.single().distanceBucket).isNull()
        assertThat(vm.state.value.savedCount).isEqualTo(1)
        assertThat(session.filtersVersion.value).isEqualTo(1)
        assertThat(api.calls.count { it == "pulse" }).isEqualTo(deckReads + 1)
        assertThat(pulse.state.value).isInstanceOf(ListState.Items::class.java)
    }

    @Test
    fun `a free filter is saved with the bucket code`() = runTest {
        api.preferences = noPass()
        val vm = filters()

        vm.setDistanceBucket("lt_5_km")
        vm.toggleIntent("serious")
        vm.save()

        val sent = api.preferenceWrites.single()
        assertThat(sent.distanceBucket).isEqualTo("lt_5_km")
        assertThat(sent.distanceKm).isNull()
        assertThat(sent.intentFilter).containsExactly("serious")
        assertThat(sent.passFilters).isNull()
    }

    @Test
    fun `without a pass the section is locked, and setting a filter opens the upsell`() = runTest {
        api.preferences = noPass()
        val vm = filters()

        assertThat(vm.state.value.locked).isTrue()

        vm.toggleLanguage("en")

        assertThat(vm.state.value.upsell).isTrue()
        assertThat(vm.state.value.draft.pass.languages).isEmpty()
        vm.dismissUpsell()
        vm.save()
        assertThat(api.preferenceWrites).isEmpty()
    }

    @Test
    fun `without a pass, stored filters can still be cleared`() = runTest {
        api.preferences = noPass(PassFiltersDto(active = false, languages = listOf("en"), diet = listOf("vegan")))
        val vm = filters()

        vm.clearPassFilters()
        vm.save()

        val pass = checkNotNull(api.preferenceWrites.single().passFilters)
        assertThat(pass.verifiedOnly).isFalse()
        assertThat(pass.minHeightCm).isNull()
        assertThat(pass.languages).isEmpty()
        assertThat(pass.diet).isEmpty()
        assertThat(vm.state.value.upsell).isFalse()
    }

    @Test
    fun `FILTERS_REQUIRE_PASS on a save opens the upsell and locks the section`() = runTest {
        api.preferences = fixture("preferences_get_200_filters.json", PreferencesDto.serializer())
        api.preferencesWriteResponse = { refusedWithFixture(403, "preferences_put_403_filters_require_pass.json") }
        val vm = filters()

        vm.toggleLanguage("hi")
        vm.save()

        val state = vm.state.value
        assertThat(state.upsell).isTrue()
        assertThat(state.passActive).isFalse()
        assertThat(state.locked).isTrue()
        assertThat(state.savedCount).isEqualTo(0)
        assertThat(session.filtersVersion.value).isEqualTo(0)
    }

    @Test
    fun `a refused distance bucket lands under the distance control`() = runTest {
        api.preferences = noPass()
        api.preferencesWriteResponse = { refusedWithFixture(400, "preferences_put_400_invalid_distance_bucket.json") }
        val vm = filters()

        vm.setDistanceBucket("gt_25_km")
        vm.save()

        assertThat(vm.state.value.fieldErrors[FiltersField.DISTANCE]).isEqualTo("That distance isn't available any more. Pick another one.")
    }

    @Test
    fun `turning verified only off here turns the old privacy switch off too`() = runTest {
        api.privacyState = PrivacyDto(verifiedOnlyFilter = true)
        api.preferences = noPass(PassFiltersDto(active = true))
        val vm = filters()

        // The old switch counts: Filters shows it on.
        assertThat(vm.state.value.draft.pass.verifiedOnly).isTrue()

        vm.setVerifiedOnly(false)
        vm.save()

        assertThat(api.preferenceWrites.single().passFilters?.verifiedOnly).isFalse()
        assertThat(api.privacyWrites.single().verifiedOnlyFilter).isFalse()
        assertThat(vm.state.value.savedCount).isEqualTo(1)
    }

    // ── Privacy ─────────────────────────────────────────────────────────────

    @Test
    fun `with the filters flag on, the privacy verified-only switch is not drawn`() = runTest {
        api.preferences = noPass()
        val vm = PrivacyViewModel(repository, session, UnconfinedTestDispatcher(testScheduler))

        assertThat(vm.state.value.verifiedOnlyInFilters).isTrue()
    }

    @Test
    fun `with the filters flag off, the privacy switch stays`() = runTest {
        val vm = PrivacyViewModel(repository, session, UnconfinedTestDispatcher(testScheduler))

        assertThat(vm.state.value.verifiedOnlyInFilters).isFalse()
    }

    // ── Cards ───────────────────────────────────────────────────────────────

    @Test
    fun `a deck card shows interests and basics by label and ignores unknown codes`() = runTest {
        api.pulse = listOf(
            card(
                "user-other",
                detail = ProfileDetailDto(
                    interests = listOf("books", "cricket", "astrology"),
                    heightCm = 172,
                    drinking = "socially",
                    smoking = "vaping",
                    photos = listOf(galleryPhoto("p-1", "full")),
                ),
            ),
        )
        val pulse = PulseViewModel(repository, session, SafetyActions(repository, session), photoUrls())

        val row = (pulse.state.value as ListState.Items).items.single()
        val basics = goldenOptions.basicsOf(checkNotNull(row.detail).basics)

        assertThat(basics.interests).containsExactly("Books", "Cricket").inOrder()
        assertThat(basics.height).isEqualTo("172 cm")
        assertThat(basics.lines).containsExactly(BasicLine("Drinking", "Socially"))
        assertThat(row.glanceInterests(goldenOptions)).containsExactly("Books", "Cricket").inOrder()
        // Until the lists load, the card shows no interests at all — never codes.
        assertThat(row.glanceInterests(null)).isEmpty()
    }

    @Test
    fun `the person golden renders every basic with the server's labels`() = runTest {
        val golden = fixture("person_get_200_basics.json", DatingPersonDto.serializer())
        api.people = mapOf("user-other" to golden.copy(userId = "user-other"))
        val view = PersonViewModel(SavedStateHandle(mapOf("userId" to "user-other")), repository, photoUrls())

        val detail = checkNotNull((view.state.value as PersonState.Loaded).person.detail)
        val basics = goldenOptions.basicsOf(detail.basics)

        assertThat(basics.interests).containsExactly("Books", "Cricket", "Yoga").inOrder()
        assertThat(basics.height).isEqualTo("172 cm")
        assertThat(basics.lines).containsExactly(
            BasicLine("Drinking", "Socially"),
            BasicLine("Smoking", "Never"),
            BasicLine("Exercise", "Often"),
            BasicLine("Diet", "Vegetarian"),
        ).inOrder()
        assertThat(goldenOptions.languageLabels(detail.languages)).containsExactly("English", "Telugu").inOrder()
    }

    @Test
    fun `a detail block with only basics in it is still a detail`() = runTest {
        api.pulse = listOf(card("user-other", detail = ProfileDetailDto(interests = listOf("yoga"))))
        val pulse = PulseViewModel(repository, session, SafetyActions(repository, session), photoUrls())

        val detail = (pulse.state.value as ListState.Items).items.single().detail

        assertThat(detail?.basics?.interests).containsExactly("yoga")
    }

    @Test
    fun `a language code becomes its label and older free text is kept as written`() {
        assertThat(goldenOptions.languageLabels(listOf("en", "telugu", " ", "en"))).containsExactly("English", "telugu").inOrder()
    }

    @Test
    fun `a height outside the server's range shows nothing`() {
        assertThat(goldenOptions.heightLabel(90)).isNull()
        assertThat(goldenOptions.heightLabel(null)).isNull()
        assertThat(goldenOptions.heightLabel(230)).isEqualTo("230 cm")
    }
}
