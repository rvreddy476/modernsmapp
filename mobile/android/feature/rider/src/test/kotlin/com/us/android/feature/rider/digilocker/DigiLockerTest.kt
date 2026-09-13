package com.us.android.feature.rider.digilocker

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import com.us.android.core.food.network.DeliveryKycDto
import com.us.android.core.food.network.DigiLockerStartDto
import com.us.android.core.food.repository.FoodError
import com.us.android.feature.rider.RiderFixtures
import org.junit.Test

class DigiLockerTest {

    private val pending = PendingDigiLocker(state = "s-123", expiresAt = "2026-09-13T06:40:00Z")

    @Test
    fun `the http return link food-service redirects to is recognised and decoded`() {
        val link = DigiLockerReturnLink.parse(
            "https://rider.feast.example/rider/digilocker/return?code=mock-abc%2B1&state=s-123",
        )
        assertThat(link).isEqualTo(DigiLockerReturn(code = "mock-abc+1", state = "s-123", error = null, errorDescription = null))
        assertThat(DigiLockerReturnLink.parse("http://10.0.2.2/rider/digilocker/return?state=s-123&error=access_denied&error_description=user%20cancelled"))
            .isEqualTo(DigiLockerReturn(code = null, state = "s-123", error = "access_denied", errorDescription = "user cancelled"))
    }

    @Test
    fun `the dev custom scheme is recognised, other links are not`() {
        assertThat(DigiLockerReturnLink.parse("feastrider://digilocker/return?code=c&state=s")?.code).isEqualTo("c")
        for (other in listOf(
            "https://rider.feast.example/rider/offers/1",
            "feastrider://digilocker/elsewhere?code=c&state=s",
            "intent://digilocker/return?code=c",
            "javascript:alert(1)",
            "",
            null,
        )) {
            assertWithMessage(other.toString()).that(DigiLockerReturnLink.parse(other)).isNull()
        }
    }

    @Test
    fun `the start fixture's state is what the device keeps and expects back`() {
        val start = RiderFixtures.success("kyc_digilocker_start_200.json", DigiLockerStartDto.serializer()).value
        val saved = PendingDigiLocker(start.state, start.expiresAt)
        val echoed = DigiLockerReturn(code = "mock-1", state = start.state, error = null, errorDescription = null)

        assertThat(DigiLockerReturnPolicy.check(echoed, saved)).isEqualTo(ReturnCheck.Complete(state = "<state>", code = "mock-1"))
    }

    @Test
    fun `a state that is not this device's is never posted`() {
        val foreign = DigiLockerReturn(code = "mock-1", state = "s-OTHER", error = null, errorDescription = null)
        assertThat(DigiLockerReturnPolicy.check(foreign, pending)).isEqualTo(ReturnCheck.StateMismatch)

        val stateless = foreign.copy(state = null)
        assertThat(DigiLockerReturnPolicy.check(stateless, pending)).isEqualTo(ReturnCheck.StateMismatch)

        // Nothing started here at all.
        assertThat(DigiLockerReturnPolicy.check(foreign.copy(state = "s-123"), null)).isEqualTo(ReturnCheck.NothingPending)
    }

    @Test
    fun `a matching state with a provider error or no code is not posted either`() {
        val declined = DigiLockerReturn(code = null, state = "s-123", error = "access_denied", errorDescription = null)
        assertThat(DigiLockerReturnPolicy.check(declined, pending)).isEqualTo(ReturnCheck.Declined("access_denied"))

        val noCode = DigiLockerReturn(code = " ", state = "s-123", error = null, errorDescription = null)
        assertThat(DigiLockerReturnPolicy.check(noCode, pending)).isEqualTo(ReturnCheck.MissingCode)
    }

    @Test
    fun `every callback fixture maps to what the screen tells the rider`() {
        val expected = mapOf(
            "kyc_digilocker_callback_403_state_not_yours.json" to DigiLockerOutcome.StartedByAnotherAccount,
            "kyc_digilocker_callback_409_document_in_use.json" to DigiLockerOutcome.DocumentInUse,
            "kyc_digilocker_callback_409_state_used.json" to DigiLockerOutcome.LinkUsed,
            "kyc_digilocker_callback_410_state_expired.json" to DigiLockerOutcome.LinkExpired,
            "kyc_digilocker_callback_422_state_invalid.json" to DigiLockerOutcome.LinkInvalid,
            "kyc_digilocker_callback_502_provider_failed.json" to DigiLockerOutcome.ProviderFailed,
        )
        assertThat(RiderFixtures.names("kyc_digilocker_callback_").filterNot { it.contains("_200") })
            .containsExactlyElementsIn(expected.keys + "kyc_digilocker_callback_422_code_required.json")
        for ((name, outcome) in expected) {
            assertWithMessage(name).that(DigiLockerOutcome.from(RiderFixtures.failure(name))).isEqualTo(outcome)
        }
        assertThat(DigiLockerOutcome.from(RiderFixtures.failure("kyc_digilocker_callback_422_code_required.json")))
            .isInstanceOf(DigiLockerOutcome.Failed::class.java)

        val verified = DigiLockerOutcome.from(RiderFixtures.success("kyc_digilocker_callback_200.json", DeliveryKycDto.serializer()))
        assertThat((verified as DigiLockerOutcome.Verified).kyc.missing).containsExactly("selfie", "payout_account").inOrder()

        assertThat(DigiLockerOutcome.LinkExpired.startAgain).isTrue()
        assertThat(DigiLockerOutcome.StartedByAnotherAccount.startAgain).isTrue()
        assertThat(DigiLockerOutcome.DocumentInUse.startAgain).isFalse()
    }

    @Test
    fun `a start that cannot run says DigiLocker is unavailable`() {
        for (name in listOf("kyc_digilocker_start_503_not_configured.json", "kyc_digilocker_start_503_pii_not_configured.json")) {
            assertWithMessage(name).that(DigiLockerOutcome.fromError(RiderFixtures.failure(name).error)).isEqualTo(DigiLockerOutcome.NotAvailable)
        }
        assertThat(RiderFixtures.failure("kyc_digilocker_start_404_no_profile.json").error).isEqualTo(FoodError.NotFound)
    }
}
