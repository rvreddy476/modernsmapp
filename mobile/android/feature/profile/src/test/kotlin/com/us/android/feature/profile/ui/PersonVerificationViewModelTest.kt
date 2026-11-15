package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.profile.data.OFFICIAL_FALLBACK_MESSAGE
import com.us.android.feature.profile.data.PersonAttempt
import com.us.android.feature.profile.data.PersonSelfieUploader
import com.us.android.feature.profile.data.PersonStart
import com.us.android.feature.profile.data.PersonStatus
import com.us.android.feature.profile.data.PersonVerificationReturns
import com.us.android.feature.profile.data.PersonVerificationSource
import com.us.android.feature.profile.data.SelfieUpload
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.File

/** Request verification › Verify you're a real person (2026-11-15): every step is the server's `next`. */
class PersonVerificationViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val source = FakePersonSource()
    private val uploads = ArrayDeque<SelfieUpload>()
    private val uploaded = mutableListOf<File>()
    private val uploader = PersonSelfieUploader { file ->
        uploaded += file
        uploads.removeFirstOrNull() ?: SelfieUpload.Failed("upload failed")
    }
    private val returns = PersonVerificationReturns()

    private fun vm() = PersonVerificationViewModel(source, uploader, returns)

    private val clip = File("selfie.mp4")

    @Test
    fun `each next draws its own step`() {
        val cases = mapOf("digilocker" to PersonStep.DigiLocker, "selfie" to PersonStep.Selfie, "" to PersonStep.Done)
        cases.forEach { (next, step) ->
            source.statuses += ok(next)
            assertThat(vm().state.value.step).isEqualTo(step)
        }
    }

    @Test
    fun `a failed first read shows the server's sentence and no step`() {
        source.statuses += refusal("PROFILE_NOT_FOUND", "Create your profile first.", 404)

        val state = vm().state.value

        assertThat(state.step).isNull()
        assertThat(state.loadError).isEqualTo("Create your profile first.")
    }

    @Test
    fun `start with a real authorize url asks the screen to open it`() = runTest {
        source.statuses += ok("digilocker")
        source.startResult = AppResult.Success(PersonStart(authorizeUrl = REAL_URL))
        val vm = vm()
        val opened = mutableListOf<String>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.open.collect { opened += it } }

        vm.start()

        assertThat(opened).containsExactly(REAL_URL)
        assertThat(source.callbacks).isEmpty()
        assertThat(vm.state.value.starting).isFalse()
    }

    @Test
    fun `start in mock mode skips the browser, posts the callback, then the selfie moves to done`() = runTest {
        source.statuses += ok("digilocker")
        source.startResult = AppResult.Success(PersonStart(authorizeUrl = MOCK_URL))
        source.callbackResult = ok("selfie")
        source.selfieResult = ok("")
        uploads += SelfieUpload.Ready("media-1")
        val vm = vm()
        val opened = mutableListOf<String>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { vm.open.collect { opened += it } }

        vm.start()

        assertThat(opened).isEmpty()
        assertThat(source.callbacks).containsExactly("st-1" to "mock-code")
        assertThat(vm.state.value.step).isEqualTo(PersonStep.Selfie)

        vm.onSelfieRecorded(clip)

        assertThat(uploaded).containsExactly(clip)
        assertThat(source.selfies).containsExactly("media-1")
        assertThat(vm.state.value.step).isEqualTo(PersonStep.Done)
        assertThat(vm.state.value.checking).isFalse()
    }

    @Test
    fun `an App Link return is posted once`() {
        source.statuses += ok("digilocker")
        source.callbackResult = ok("selfie")
        val vm = vm()

        returns.offerRedirect("https://cleestudio.com/settings/verification/person/return?code=c9&state=s9")

        assertThat(source.callbacks).containsExactly("s9" to "c9")
        assertThat(returns.pending.value).isNull()
        assertThat(vm.state.value.step).isEqualTo(PersonStep.Selfie)
    }

    @Test
    fun `a refused callback shows the server's sentence`() {
        source.statuses += ok("digilocker")
        source.startResult = AppResult.Success(PersonStart(authorizeUrl = MOCK_URL))
        source.callbackResult = refusal("NAME_MISMATCH", "Your display name must match the name on your Aadhaar")
        val vm = vm()

        vm.start()

        assertThat(vm.state.value.error).isEqualTo("Your display name must match the name on your Aadhaar")
        assertThat(vm.state.value.step).isEqualTo(PersonStep.DigiLocker)
        assertThat(source.statusReads).isEqualTo(1)
    }

    @Test
    fun `a refused selfie re-reads and shows the refused line over DigiLocker`() {
        listOf("LIVENESS_FAILED", "FACE_MISMATCH", "NO_PENDING_ATTEMPT", "INVALID_STATE").forEach { code ->
            val source = FakePersonSource()
            source.statuses += ok("selfie")
            source.statuses += AppResult.Success(
                PersonStatus(next = "digilocker", attempt = PersonAttempt(status = "refused", refusalCode = code)),
            )
            source.selfieResult = refusal(code, "That didn't pass.")
            uploads += SelfieUpload.Ready("media-2")
            val vm = PersonVerificationViewModel(source, uploader, returns)

            vm.onSelfieRecorded(clip)

            assertThat(source.statusReads).isEqualTo(2)
            assertThat(vm.state.value.step).isEqualTo(PersonStep.DigiLocker)
            assertThat(vm.state.value.showRefused).isTrue()
            assertThat(vm.state.value.error).isEqualTo("That didn't pass.")
        }
    }

    @Test
    fun `other selfie refusals stay on the selfie step without a re-read`() {
        source.statuses += ok("selfie")
        source.selfieResult = refusal("FACE_CHECK_UNAVAILABLE", "Try again in a few minutes.", 503)
        uploads += SelfieUpload.Ready("media-3")
        val vm = vm()

        vm.onSelfieRecorded(clip)

        assertThat(source.statusReads).isEqualTo(1)
        assertThat(vm.state.value.step).isEqualTo(PersonStep.Selfie)
        assertThat(vm.state.value.error).isEqualTo("Try again in a few minutes.")
    }

    @Test
    fun `a failed upload never posts the selfie`() {
        source.statuses += ok("selfie")
        uploads += SelfieUpload.Failed("The video didn't upload. Check your connection and try again.")
        val vm = vm()

        vm.onSelfieRecorded(clip)

        assertThat(source.selfies).isEmpty()
        assertThat(vm.state.value.error).isEqualTo("The video didn't upload. Check your connection and try again.")
        assertThat(vm.state.value.checking).isFalse()
    }

    @Test
    fun `a refusal with no sentence falls back to the plain line`() {
        source.statuses += ok("digilocker")
        source.startResult = AppResult.Failure(AppError.NoNetwork())
        val vm = vm()

        vm.start()

        assertThat(vm.state.value.error).isEqualTo(OFFICIAL_FALLBACK_MESSAGE)
    }

    @Test
    fun `the refused line shows only over DigiLocker`() {
        val refused = PersonAttempt(status = "refused")
        assertThat(PersonVerificationUiState(status = PersonStatus(next = "digilocker", attempt = refused)).showRefused)
            .isTrue()
        assertThat(PersonVerificationUiState(status = PersonStatus(next = "selfie", attempt = refused)).showRefused)
            .isFalse()
        assertThat(PersonVerificationUiState(status = PersonStatus(next = "digilocker")).showRefused).isFalse()
    }

    @Test
    fun `the row is hidden on 503 and on any failure, and reads verified when done`() {
        val entrySource = FakePersonSource()
        val entry = PersonVerificationEntryViewModel(entrySource)

        entrySource.statuses += refusal("PERSON_VERIFICATION_DISABLED", "Not open yet.", 503)
        entry.refresh()
        assertThat(entry.state.value).isEqualTo(PersonEntryState(shown = false))

        entrySource.statuses += AppResult.Failure(AppError.NoNetwork())
        entry.refresh()
        assertThat(entry.state.value.shown).isFalse()

        entrySource.statuses += ok("digilocker")
        entry.refresh()
        assertThat(entry.state.value).isEqualTo(PersonEntryState(shown = true, verified = false))

        entrySource.statuses += ok("")
        entry.refresh()
        assertThat(entry.state.value).isEqualTo(PersonEntryState(shown = true, verified = true))
    }

    private fun ok(next: String): AppResult<PersonStatus> = AppResult.Success(PersonStatus(next = next))

    private fun refusal(code: String, message: String, status: Int = 422) =
        AppResult.Failure(AppError.Unknown(code = code, statusCode = status, message = message))

    private companion object {
        const val REAL_URL = "https://digilocker.example/public/oauth2/1/authorize?state=st-1"
        const val MOCK_URL = "http://localhost:3000/settings/verification/person/return?code=mock-code&state=st-1"
    }
}

/** Answers each call from its queue; records what was sent. */
private class FakePersonSource : PersonVerificationSource {
    val statuses = ArrayDeque<AppResult<PersonStatus>>()
    var statusReads = 0
    var startResult: AppResult<PersonStart> = AppResult.Failure(AppError.NoNetwork())
    var callbackResult: AppResult<PersonStatus> = AppResult.Failure(AppError.NoNetwork())
    var selfieResult: AppResult<PersonStatus> = AppResult.Failure(AppError.NoNetwork())
    val callbacks = mutableListOf<Pair<String, String>>()
    val selfies = mutableListOf<String>()

    override suspend fun status(): AppResult<PersonStatus> {
        statusReads++
        return statuses.removeFirstOrNull() ?: AppResult.Failure(AppError.NoNetwork())
    }

    override suspend fun start(): AppResult<PersonStart> = startResult

    override suspend fun callback(state: String, code: String): AppResult<PersonStatus> =
        callbackResult.also { callbacks += state to code }

    override suspend fun selfie(mediaId: String): AppResult<PersonStatus> = selfieResult.also { selfies += mediaId }
}
