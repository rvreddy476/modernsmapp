package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.dating.onboarding.ageFrom
import com.us.android.feature.dating.photos.PhotoRules
import com.us.android.feature.dating.photos.PhotoVariant
import org.junit.Test
import java.time.LocalDate

/** The pure rules: the onboarding gate, distance buckets, blurred-until-matched photos. */
class DatingRulesTest {

    // ── Onboarding gate ──────────────────────────────────────────────────────

    @Test
    fun `the gate follows the server's machine, draft to active, in order`() {
        val prefs = testPrefs()
        val machine = listOf(
            "draft" to OnboardingStep.BASICS,
            "pending_photo" to OnboardingStep.PHOTOS,
            "pending_selfie" to OnboardingStep.SELFIE,
            "active" to OnboardingStep.READY,
        )
        assertThat(OnboardingGate.stepFor(null, null)).isEqualTo(OnboardingStep.CREATE)
        machine.forEach { (status, step) ->
            assertThat(OnboardingGate.stepFor(profile(status = status, gender = null), prefs)).isEqualTo(
                if (status == "draft") OnboardingStep.BASICS else step,
            )
        }
        // Each later status maps to a later step: the machine is never walked backwards or skipped.
        val steps = machine.map { (status, _) -> OnboardingGate.stepFor(profile(status = status), prefs) }
        assertThat(steps.map { it.ordinal }).isInOrder()
    }

    @Test
    fun `inside draft, basics then location then preferences`() {
        assertThat(OnboardingGate.stepFor(profile("draft", gender = null), testPrefs())).isEqualTo(OnboardingStep.BASICS)
        assertThat(OnboardingGate.stepFor(profile("draft", city = null), testPrefs())).isEqualTo(OnboardingStep.LOCATION)
        assertThat(OnboardingGate.stepFor(profile("draft"), testPrefs(interestedIn = null))).isEqualTo(OnboardingStep.PREFERENCES)
        assertThat(OnboardingGate.stepFor(profile("draft"), null)).isEqualTo(OnboardingStep.PREFERENCES)
    }

    @Test
    fun `nothing but active opens Pulse — holds and unknown statuses are held`() {
        listOf("draft", "pending_photo", "pending_selfie", "pending_review", "paused", "restricted", "suspended", "deleted", "", "activated", "ACTIVE")
            .forEach { status ->
                assertThat(OnboardingGate.stepFor(profile(status = status), testPrefs())).isNotEqualTo(OnboardingStep.READY)
            }
        assertThat(OnboardingGate.stepFor(profile("pending_review"), null)).isEqualTo(OnboardingStep.REVIEW)
        assertThat(OnboardingGate.stepFor(profile("paused"), null)).isEqualTo(OnboardingStep.PAUSED)
        assertThat(OnboardingGate.stepFor(profile("suspended"), null)).isEqualTo(OnboardingStep.HELD)
        assertThat(OnboardingGate.stepFor(profile("restricted"), null)).isEqualTo(OnboardingStep.HELD)
        assertThat(OnboardingGate.stepFor(profile("something_new"), null)).isEqualTo(OnboardingStep.HELD)
    }

    @Test
    fun `name and birth date come from identity`() {
        assertThat(OnboardingGate.identityIncomplete(profile(firstName = null))).isTrue()
        assertThat(OnboardingGate.identityIncomplete(profile(birthDate = null))).isTrue()
        assertThat(OnboardingGate.identityIncomplete(profile())).isFalse()
        assertThat(ageFrom("1995-04-12T00:00:00Z", LocalDate.of(2026, 9, 16))).isEqualTo(31)
        assertThat(ageFrom("1995-09-17T00:00:00Z", LocalDate.of(2026, 9, 16))).isEqualTo(30)
        assertThat(ageFrom("not a date")).isNull()
    }

    // ── Distance ────────────────────────────────────────────────────────────

    @Test
    fun `distance renders the four bucket labels only`() {
        assertThat(DistanceBucket.labelFor("lt_5_km")).isEqualTo("< 5 km")
        assertThat(DistanceBucket.labelFor("km_5_10")).isEqualTo("5–10 km")
        assertThat(DistanceBucket.labelFor("km_10_25")).isEqualTo("10–25 km")
        assertThat(DistanceBucket.labelFor("gt_25_km")).isEqualTo("25+ km")
    }

    @Test
    fun `an unknown code, a number or a server label never renders`() {
        listOf(null, "", "3.2 km", "3200", "lt_1_km", "LT_5_KM", "distance_km:4").forEach { code ->
            assertThat(DistanceBucket.labelFor(code)).isNull()
        }
        // A card whose server label is an exact number but whose bucket is unknown shows nothing.
        val sneaky = card("u-1", bucket = "exact", label = "3.27 km").profile
        assertThat(DistanceBucket.labelFor(sneaky.distanceBucket)).isNull()
        // No label the app can produce contains a decimal or an exact figure.
        DistanceBucket.entries.forEach { assertThat(it.label).doesNotContainMatch("\\d+\\.\\d") }
    }

    // ── Photos ──────────────────────────────────────────────────────────────

    @Test
    fun `someone you have not matched with is shown blurred, even when the server offers full`() {
        val serverPath = "/v1/dating/photos/0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0003/full"
        assertThat(PhotoRules.viewerPath(serverPath, matched = false))
            .isEqualTo("/v1/dating/photos/0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0003/blurred")
        assertThat(PhotoRules.variantFor(matched = false)).isEqualTo(PhotoVariant.BLURRED)
    }

    @Test
    fun `a match is shown the full photo`() {
        assertThat(PhotoRules.viewerPath("/v1/dating/photos/p-1/blurred", matched = true)).isEqualTo("/v1/dating/photos/p-1/full")
        assertThat(PhotoRules.variantFor(matched = true)).isEqualTo(PhotoVariant.FULL)
    }

    @Test
    fun `only a dating photo path is ever loaded`() {
        listOf(
            null,
            "",
            "https://cdn.example/signed.jpg?X-Amz-Signature=abc",
            "/v1/media/p-1/full",
            "/v1/dating/photos/p-1/original",
            "/v1/dating/photos//full",
        ).forEach { assertThat(PhotoRules.viewerPath(it, matched = false)).isNull() }
    }

    private fun testPrefs(interestedIn: String? = "man") =
        com.us.android.feature.dating.network.PreferencesDto(userId = ME, interestedInGender = interestedIn, distanceKm = 25)
}
