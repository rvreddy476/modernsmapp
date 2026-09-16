package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.network.SelfieResultDto
import com.us.android.feature.dating.network.SelfieStatusDto
import com.us.android.feature.dating.network.VerificationStatusDto
import com.us.android.feature.dating.photos.UploadOutcome
import com.us.android.feature.dating.selfie.SelfieOutcomes
import com.us.android.feature.dating.selfie.SelfieState
import com.us.android.feature.dating.selfie.SelfieVideoUploader
import com.us.android.feature.dating.selfie.SelfieViewModel
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.File

/**
 * The blink-twice selfie check: consent before any challenge, a clip of at most
 * 4 s, clear retry copy for NOT_ENOUGH_BLINKS on a FRESH challenge, and the
 * daily limit.
 */
class SelfieFlowTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession()
    private val uploads = mutableListOf<File>()
    private var uploadOutcome: UploadOutcome = UploadOutcome.Ready("video-media-1")

    private val uploader = object : SelfieVideoUploader {
        override suspend fun upload(file: File, onProgress: (Float) -> Unit): UploadOutcome {
            uploads += file
            onProgress(1f)
            return uploadOutcome
        }
    }

    private val clip = File("clip.mp4")

    private fun vm(vararg granted: ConsentType): SelfieViewModel {
        api.granted += granted
        session.setConsents(consents(*granted))
        return SelfieViewModel(api.repository(), session, uploader)
    }

    @Test
    fun `consent is asked before a challenge is even requested`() = runTest {
        val model = vm()

        assertThat(model.state.value).isEqualTo(SelfieState.NeedsConsent)
        assertThat(api.calls).doesNotContain("challenge")

        model.onConsentAnswered(granted = true)

        assertThat(api.calls).containsExactly("consent:biometric_selfie=true", "challenge").inOrder()
        assertThat(model.state.value).isInstanceOf(SelfieState.Ready::class.java)
    }

    @Test
    fun `declining the biometric consent requests nothing`() = runTest {
        val model = vm()
        model.onConsentAnswered(granted = false)
        assertThat(model.state.value).isEqualTo(SelfieState.ConsentDeclined)
        assertThat(api.calls).isEmpty()
    }

    @Test
    fun `the server's CONSENT_REQUIRED on the challenge brings the consent back`() = runTest {
        api.challengeResponse = { refusedWithFixture(422, "selfie_challenge_422_consent_required.json") }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)
        assertThat(model.state.value).isEqualTo(SelfieState.NeedsConsent)
    }

    @Test
    fun `NOT_ENOUGH_BLINKS explains the blink and retries on a fresh challenge`() = runTest {
        api.selfieResponse = { ok(SelfieResultDto(status = "failed", passed = false, reason = "NOT_ENOUGH_BLINKS", attemptsRemaining = 3)) }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)
        val first = model.state.value as SelfieState.Ready
        assertThat(first.recordMillis).isAtMost(4_000L)

        model.onRecorded(clip)

        val retry = model.state.value as SelfieState.Retry
        assertThat(retry.reason).isEqualTo("NOT_ENOUGH_BLINKS")
        assertThat(retry.copy).contains("blink twice")
        assertThat(retry.attemptsRemaining).isEqualTo(3)
        assertThat(SelfieOutcomes.attemptsLine(retry.attemptsRemaining)).isEqualTo("3 attempts left today")
        assertThat(api.selfieSubmissions.single()).isEqualTo(
            com.us.android.feature.dating.network.SelfieSubmitRequest(first.challengeId, "video-media-1"),
        )

        api.selfieResponse = { ok(SelfieResultDto(status = "passed", passed = true, attemptsRemaining = 2)) }
        model.retry()
        val second = model.state.value as SelfieState.Ready
        assertThat(second.challengeId).isNotEqualTo(first.challengeId)

        model.onRecorded(clip)
        assertThat(model.state.value).isEqualTo(SelfieState.Passed)
        assertThat(api.selfieSubmissions.map { it.challengeId }).containsExactly(first.challengeId, second.challengeId).inOrder()
    }

    @Test
    fun `a borderline check goes to review, the daily limit stops retries`() = runTest {
        assertThat(SelfieOutcomes.fromResult(SelfieResultDto(status = "pending_review", reason = "MANUAL_REVIEW", attemptsRemaining = 2)))
            .isEqualTo(SelfieState.InReview)
        assertThat(SelfieOutcomes.fromResult(SelfieResultDto(status = "failed", reason = "NO_MATCH", attemptsRemaining = 0)))
            .isEqualTo(SelfieState.LimitReached)

        api.challengeResponse = { refused(429, "SELFIE_ATTEMPTS_EXCEEDED", """{"limit":5,"window_hours":24}""") }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)
        assertThat(model.state.value).isEqualTo(SelfieState.LimitReached)
    }

    @Test
    fun `every failure reason has its own words`() {
        val reasons = listOf("NOT_ENOUGH_BLINKS", "NO_FACE", "MULTIPLE_FACES", "FACE_CHANGED", "LOW_QUALITY", "NO_MATCH")
        val copies = reasons.map(SelfieOutcomes::copyFor)
        assertThat(copies.toSet()).hasSize(reasons.size)
        assertThat(copies).doesNotContain(SelfieOutcomes.copyFor("SOMETHING_NEW"))
    }

    @Test
    fun `a failed upload submits nothing and keeps the unused challenge`() = runTest {
        uploadOutcome = UploadOutcome.Failed("The video didn't upload. Check your connection and try again.")
        val model = vm(ConsentType.BIOMETRIC_SELFIE)
        val ready = model.state.value as SelfieState.Ready

        model.onRecorded(clip)

        assertThat(api.selfieSubmissions).isEmpty()
        val after = model.state.value as SelfieState.Ready
        assertThat(after.challengeId).isEqualTo(ready.challengeId)
        assertThat(after.note).contains("didn't upload")
    }

    @Test
    fun `an expired challenge is replaced without the person doing anything`() = runTest {
        api.selfieResponse = { refused(400, "SELFIE_CHALLENGE_INVALID") }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)
        val first = model.state.value as SelfieState.Ready

        model.onRecorded(clip)

        val fresh = model.state.value as SelfieState.Ready
        assertThat(fresh.challengeId).isNotEqualTo(first.challengeId)
    }

    @Test
    fun `the clip never exceeds the server's cap`() {
        assertThat(SelfieOutcomes.recordMillis(4_000)).isLessThan(4_000L)
        assertThat(SelfieOutcomes.recordMillis(10_000)).isLessThan(4_000L)
        assertThat(SelfieOutcomes.recordMillis(0)).isLessThan(4_000L)
        assertThat(SelfieOutcomes.recordMillis(3_000)).isLessThan(3_000L)
    }

    // ── MEDIA_NOT_READY (409) ───────────────────────────────────────────────

    @Test
    fun `MEDIA_NOT_READY shows the retry state and does not count an attempt`() = runTest {
        api.verificationResponse = {
            ok(VerificationStatusDto(selfie = SelfieStatusDto(state = "none", attemptsLeftToday = 5, attemptsPerDay = 5), nextStep = "submit_selfie"))
        }
        api.selfieResponse = { refused(409, SelfieOutcomes.CODE_MEDIA_NOT_READY) }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)
        val ready = model.state.value as SelfieState.Ready

        model.onRecorded(clip)

        val waiting = model.state.value as SelfieState.StillProcessing
        // The same challenge and the same clip: nothing was spent.
        assertThat(waiting.challengeId).isEqualTo(ready.challengeId)
        assertThat(waiting.mediaId).isEqualTo("video-media-1")
        assertThat(waiting.attemptsLeft).isEqualTo(5)
        assertThat(SelfieOutcomes.MEDIA_NOT_READY_COPY).contains("still processing")
        // No new challenge was asked for, so no attempt was burned.
        assertThat(api.calls.count { it == "challenge" }).isEqualTo(1)
    }

    @Test
    fun `retrying after MEDIA_NOT_READY resends the same clip and can pass`() = runTest {
        api.selfieResponse = { refused(409, SelfieOutcomes.CODE_MEDIA_NOT_READY) }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)
        model.onRecorded(clip)
        assertThat(model.state.value).isInstanceOf(SelfieState.StillProcessing::class.java)

        // Media has finished processing by the time they tap Try again.
        api.selfieResponse = { ok(SelfieResultDto(status = "passed", passed = true, attemptsRemaining = 5)) }
        model.resubmit()

        assertThat(model.state.value).isEqualTo(SelfieState.Passed)
        // One recording, two submissions of the SAME media id, one challenge.
        assertThat(uploads).hasSize(1)
        assertThat(api.selfieSubmissions.map { it.videoMediaId }).containsExactly("video-media-1", "video-media-1")
        assertThat(api.selfieSubmissions.map { it.challengeId }.distinct()).hasSize(1)
        assertThat(api.calls.count { it == "challenge" }).isEqualTo(1)
    }

    // ── GET /verification/status ────────────────────────────────────────────

    @Test
    fun `a passed check is read from the status, without asking for a challenge`() = runTest {
        api.verificationResponse = {
            ok(VerificationStatusDto(selfie = SelfieStatusDto(state = "passed"), verified = true, trustTier = "selfie", nextStep = "none"))
        }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)

        assertThat(model.state.value).isEqualTo(SelfieState.Passed)
        assertThat(api.calls).contains("verification")
        assertThat(api.calls).doesNotContain("challenge")
    }

    @Test
    fun `a review in progress is read from the status`() = runTest {
        api.verificationResponse = {
            ok(VerificationStatusDto(selfie = SelfieStatusDto(state = "review"), nextStep = "wait_for_review"))
        }

        assertThat(vm(ConsentType.BIOMETRIC_SELFIE).state.value).isEqualTo(SelfieState.InReview)
    }

    @Test
    fun `no attempts left today is read from the status, not inferred`() = runTest {
        api.verificationResponse = {
            ok(VerificationStatusDto(selfie = SelfieStatusDto(state = "failed", attemptsLeftToday = 0, attemptsPerDay = 5, windowHours = 24), nextStep = "retry_tomorrow"))
        }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)

        assertThat(model.state.value).isEqualTo(SelfieState.LimitReached)
        assertThat(api.calls).doesNotContain("challenge")
    }

    @Test
    fun `attempts left today come from the status on the very first attempt`() = runTest {
        api.verificationResponse = {
            ok(VerificationStatusDto(selfie = SelfieStatusDto(state = "failed", attemptsLeftToday = 3, attemptsPerDay = 5), nextStep = "submit_selfie"))
        }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)

        val ready = model.state.value as SelfieState.Ready
        assertThat(ready.attemptsLeft).isEqualTo(3)
        assertThat(SelfieOutcomes.attemptsLine(ready.attemptsLeft)).isEqualTo("3 attempts left today")
    }

    @Test
    fun `a status that cannot be read still lets the person record`() = runTest {
        api.verificationResponse = { refused(503, "UNAVAILABLE") }
        val model = vm(ConsentType.BIOMETRIC_SELFIE)

        // The check is not blocked by a status read that failed.
        assertThat(model.state.value).isInstanceOf(SelfieState.Ready::class.java)
        assertThat(api.calls).contains("challenge")
    }
}
