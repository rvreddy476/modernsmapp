package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.network.UpsertProfileRequest
import com.us.android.feature.dating.onboarding.DatingRootState
import com.us.android.feature.dating.onboarding.DatingRootViewModel
import com.us.android.feature.dating.onboarding.OnboardingViewModel
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The entry and the draft steps: the pilot gate's 404 is a calm "not available",
 * and sensitive fields never leave the device before their consent.
 */
class OnboardingConsentTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession()

    // ── Access ──────────────────────────────────────────────────────────────

    @Test
    fun `a 404 on the first call is not-available, and nothing else is asked`() = runTest {
        api.consentsResponse = { gatewayNotFound() }
        val root = DatingRootViewModel(api.repository(), session)

        assertThat(root.state.value).isEqualTo(DatingRootState.NotAvailable)
        assertThat(api.calls).containsExactly("consents")
    }

    @Test
    fun `any 404 on the access probe is not-available, even one shaped like dating's`() = runTest {
        api.consentsResponse = { refused(404, "NOT_FOUND") }
        val root = DatingRootViewModel(api.repository(), session)
        assertThat(root.state.value).isEqualTo(DatingRootState.NotAvailable)
    }

    @Test
    fun `an allowlisted account with no profile starts onboarding, not an error`() = runTest {
        api.profileResponse = { refused(404, "NOT_FOUND") }
        val root = DatingRootViewModel(api.repository(), session)

        val step = root.state.value as DatingRootState.Step
        assertThat(step.step).isEqualTo(OnboardingStep.CREATE)
        assertThat(api.calls).containsExactly("consents", "profile").inOrder()
    }

    @Test
    fun `a server error on the probe is an error with retry, not not-available`() = runTest {
        api.consentsResponse = { refused(500, "QUERY_FAILED") }
        val root = DatingRootViewModel(api.repository(), session)
        assertThat(root.state.value).isInstanceOf(DatingRootState.Failed::class.java)
    }

    @Test
    fun `an active profile opens Pulse`() = runTest {
        val root = DatingRootViewModel(api.repository(), session)
        assertThat((root.state.value as DatingRootState.Step).step).isEqualTo(OnboardingStep.READY)
        assertThat(session.myUserId).isEqualTo(ME)
    }

    // ── Consent in flow ─────────────────────────────────────────────────────

    private fun onboarding(): OnboardingViewModel {
        session.setConsents((kotlinx.coroutines.runBlocking { api.repository().consents() } as DatingResult.Success).value)
        api.calls.clear()
        return OnboardingViewModel(api.repository(), session, FakeLocation())
    }

    private val basicsWithReligion = UpsertProfileRequest(intent = "serious", gender = "woman", religion = "Hindu")

    @Test
    fun `religion is not sent until its consent is granted`() = runTest {
        val vm = onboarding()

        vm.saveBasics(basicsWithReligion)

        assertThat(vm.state.value.consentPrompt).isEqualTo(ConsentType.SENSITIVE_RELIGION)
        assertThat(api.upserts).isEmpty()

        vm.onConsentAnswered(granted = true)

        assertThat(api.calls).containsExactly("consent:sensitive_religion=true", "upsert").inOrder()
        assertThat(api.upserts.single().religion).isEqualTo("Hindu")
        assertThat(vm.state.value.savedCount).isEqualTo(1)
    }

    @Test
    fun `a declined consent saves the rest without the field`() = runTest {
        val vm = onboarding()

        vm.saveBasics(basicsWithReligion)
        vm.onConsentAnswered(granted = false)

        assertThat(api.calls).containsExactly("upsert")
        assertThat(api.upserts.single().religion).isNull()
        assertThat(api.upserts.single().gender).isEqualTo("woman")
    }

    @Test
    fun `religion and community are each asked, in order, before the one save`() = runTest {
        val vm = onboarding()

        vm.saveBasics(basicsWithReligion.copy(community = "Telugu"))
        assertThat(vm.state.value.consentPrompt).isEqualTo(ConsentType.SENSITIVE_RELIGION)
        vm.onConsentAnswered(granted = true)
        assertThat(vm.state.value.consentPrompt).isEqualTo(ConsentType.SENSITIVE_COMMUNITY)
        assertThat(api.upserts).isEmpty()
        vm.onConsentAnswered(granted = true)

        assertThat(api.calls).containsExactly(
            "consent:sensitive_religion=true",
            "consent:sensitive_community=true",
            "upsert",
        ).inOrder()
        assertThat(api.upserts.single().community).isEqualTo("Telugu")
    }

    @Test
    fun `a profile without sensitive fields asks for nothing`() = runTest {
        val vm = onboarding()
        vm.saveBasics(UpsertProfileRequest(intent = "casual", gender = "man"))
        assertThat(vm.state.value.consentPrompt).isNull()
        assertThat(api.calls).containsExactly("upsert")
    }

    @Test
    fun `the server's own CONSENT_REQUIRED brings the prompt back, and the save resumes after it`() = runTest {
        val vm = onboarding()
        session.setConsents(consents(ConsentType.SENSITIVE_RELIGION)) // the app thinks it is granted
        var first = true
        api.upsertResponse = {
            if (first) {
                first = false
                refusedWithFixture(422, "profile_upsert_422_consent_required.json")
            } else {
                ok(profile("draft"))
            }
        }

        vm.saveBasics(basicsWithReligion)
        assertThat(vm.state.value.consentPrompt).isEqualTo(ConsentType.SENSITIVE_RELIGION)
        vm.onConsentAnswered(granted = true)

        assertThat(api.calls).containsExactly("upsert", "consent:sensitive_religion=true", "upsert").inOrder()
        assertThat(vm.state.value.savedCount).isEqualTo(1)
    }

    @Test
    fun `a location rate limit explains the limits`() = runTest {
        val vm = onboarding()
        api.upsertResponse = { refusedWithFixture(429, "profile_upsert_429_location_change_rate_limited.json") }

        vm.onUseMyLocation()

        assertThat(vm.state.value.message?.text).contains("once every 15 minutes, up to 10 times a day")
        assertThat(api.upserts.single().latitude).isNotNull()
    }

    @Test
    fun `without permission the rationale comes first and nothing is fetched`() = runTest {
        val location = FakeLocation(permission = false)
        val vm = OnboardingViewModel(api.repository(), session, location)

        vm.onUseMyLocation()

        assertThat(vm.state.value.location).isEqualTo(com.us.android.feature.dating.location.LocationStep.ExplainingPermission)
        assertThat(api.upserts).isEmpty()
    }
}
