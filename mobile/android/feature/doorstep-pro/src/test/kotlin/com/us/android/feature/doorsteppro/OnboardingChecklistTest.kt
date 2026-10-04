package com.us.android.feature.doorsteppro

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.doorsteppro.data.ProReadinessDto
import com.us.android.feature.doorsteppro.domain.OnboardingChecklist
import com.us.android.feature.doorsteppro.domain.OnboardingStep
import com.us.android.feature.doorsteppro.domain.ProStatus
import com.us.android.feature.doorsteppro.domain.StepState
import org.junit.Test

class OnboardingChecklistTest {

    private fun readiness(
        status: String = "draft",
        missing: List<String>,
        completed: List<String>,
        recommended: List<String> = listOf("pan"),
        canGoOnDuty: Boolean = false,
    ) = ProReadinessDto(status, missing, completed, recommended, canGoOnDuty)

    @Test
    fun `the checklist is always in the contract's order, whatever order the server lists steps in`() {
        val shuffled = readiness(
            missing = listOf("agreement", "bank", "profile", "police_certificate", "weekly_hours", "service_area", "selfie_face_match"),
            completed = listOf("skills", "aadhaar_digilocker"),
        )
        assertThat(OnboardingChecklist.of(shuffled).items.map { it.step.wire }).containsExactly(
            "profile",
            "aadhaar_digilocker",
            "selfie_face_match",
            "skills",
            "service_area",
            "weekly_hours",
            "bank",
            "police_certificate",
            "agreement",
            "pan",
        ).inOrder()
        assertThat(OnboardingStep.entries.map { it.wire }).isEqualTo(OnboardingChecklist.of(shuffled).items.map { it.step.wire })
    }

    @Test
    fun `next is the first step the professional can do now, skipping done and waiting steps`() {
        val checklist = OnboardingChecklist.of(
            readiness(
                missing = listOf("selfie_face_match", "service_area", "bank"),
                completed = listOf("profile", "skills", "weekly_hours", "police_certificate", "agreement"),
            ),
        )
        // Aadhaar is neither missing nor completed here: the server is the authority, so it counts as done.
        assertThat(checklist.items.first { it.step == OnboardingStep.AADHAAR }.state).isEqualTo(StepState.DONE)
        assertThat(checklist.next).isEqualTo(OnboardingStep.SELFIE)

        val beforeAadhaar = OnboardingChecklist.of(
            readiness(missing = listOf("aadhaar_digilocker", "selfie_face_match", "bank"), completed = listOf("profile")),
        )
        assertThat(beforeAadhaar.items.first { it.step == OnboardingStep.SELFIE }.state).isEqualTo(StepState.WAITING)
        assertThat(beforeAadhaar.next).isEqualTo(OnboardingStep.AADHAAR)
    }

    @Test
    fun `a document sent from this device shows in review, not to do, while the account is still a draft`() {
        val data = readiness(missing = listOf("police_certificate"), completed = OnboardingStep.entries.map { it.wire } - "police_certificate" - "pan")
        assertThat(OnboardingChecklist.of(data).items.first { it.step == OnboardingStep.POLICE_CERTIFICATE }.state).isEqualTo(StepState.TO_DO)

        val sent = OnboardingChecklist.of(data, submittedForReview = setOf(OnboardingStep.POLICE_CERTIFICATE))
        assertThat(sent.items.first { it.step == OnboardingStep.POLICE_CERTIFICATE }.state).isEqualTo(StepState.IN_REVIEW)
        assertThat(sent.next).isNull()
        assertThat(sent.waitingForReview).isTrue()
        assertThat(sent.requiredLeft).isEqualTo(1)
        assertThat(sent.requiredTotal).isEqualTo(9)
    }

    @Test
    fun `pending verification means every missing step awaits Doorstep`() {
        val checklist = OnboardingChecklist.of(
            readiness(status = "pending_verification", missing = listOf("skills", "police_certificate"), completed = listOf("profile")),
        )
        assertThat(checklist.status).isEqualTo(ProStatus.PENDING_VERIFICATION)
        assertThat(checklist.items.filter { it.state == StepState.IN_REVIEW }.map { it.step })
            .containsExactly(OnboardingStep.SKILLS, OnboardingStep.POLICE_CERTIFICATE).inOrder()
        assertThat(checklist.waitingForReview).isTrue()
    }

    @Test
    fun `PAN is never a step to do, and an unknown server step is kept, not hidden`() {
        val checklist = OnboardingChecklist.of(readiness(missing = listOf("profile", "pan", "insurance"), completed = emptyList()))
        assertThat(checklist.items.first { it.step == OnboardingStep.PAN }.state).isEqualTo(StepState.OPTIONAL)
        assertThat(checklist.unknownMissing).containsExactly("insurance")
        assertThat(checklist.waitingForReview).isFalse()
        assertThat(checklist.items.first { it.step == OnboardingStep.PAN }.actionable).isTrue()
    }

    @Test
    fun `an unknown account status reads as unknown, not as approved`() {
        assertThat(ProStatus.of("approved")).isEqualTo(ProStatus.APPROVED)
        assertThat(ProStatus.of("archived")).isEqualTo(ProStatus.UNKNOWN)
        assertThat(ProStatus.of(null)).isEqualTo(ProStatus.UNKNOWN)
        assertThat(ProStatus.of("")).isEqualTo(ProStatus.UNKNOWN)
    }
}
