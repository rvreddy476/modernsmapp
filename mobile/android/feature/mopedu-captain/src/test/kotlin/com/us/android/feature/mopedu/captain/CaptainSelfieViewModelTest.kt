package com.us.android.feature.mopedu.captain

import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.mopedu.captain.data.CaptainError
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.selfie.CaptainSelfieViewModel
import com.us.android.feature.mopedu.captain.selfie.SelfieShot
import com.us.android.feature.mopedu.captain.selfie.SelfieStep
import com.us.android.feature.mopedu.captain.upload.CaptainDocumentUploader
import com.us.android.feature.mopedu.captain.upload.UploadOutcome
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/** The uploader as the ViewModels see it: one scripted outcome, every URI it was handed. */
class FakeCaptainDocumentUploader : CaptainDocumentUploader {
    var outcome: UploadOutcome = UploadOutcome.Ready("media-1")
    val uploads = mutableListOf<String>()

    override suspend fun uploadImage(uri: String, onProgress: (Float) -> Unit): UploadOutcome {
        uploads += uri
        onProgress(0.5f)
        return outcome
    }
}

@OptIn(ExperimentalCoroutinesApi::class)
class CaptainSelfieViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repo = FakeCaptainRepository()
    private val uploader = FakeCaptainDocumentUploader()
    private val shot = SelfieShot(path = "/cache/selfies/selfie-1.jpg", uri = "content://captain.selfie/selfies/selfie-1.jpg")

    private fun vm() = CaptainSelfieViewModel(repo, uploader)

    @Test
    fun `the selfie record is submitted only after a confirmed media id, as profile_photo`() = runTest(dispatcher) {
        uploader.outcome = UploadOutcome.Ready("media-selfie-9")
        val model = vm()
        model.onCaptured(shot)
        assertThat(model.state.value.step).isEqualTo(SelfieStep.Review(shot))
        model.submit()
        assertThat(model.state.value.step).isEqualTo(SelfieStep.Uploading(shot, 0f))
        assertThat(repo.submittedDocuments).isEmpty()
        runCurrent()
        assertThat(uploader.uploads).containsExactly(shot.uri)
        assertThat(repo.submittedDocuments).containsExactly("profile_photo")
        assertThat(repo.submittedMediaIds).containsExactly("media-selfie-9")
        assertThat(model.state.value.step).isEqualTo(SelfieStep.Submitted("pending"))
        assertThat(model.state.value.message?.type).isEqualTo(UsMessageType.Success)
    }

    @Test
    fun `a failed upload never submits and leaves the captain on the review step with the reason`() = runTest(dispatcher) {
        uploader.outcome = UploadOutcome.Failed("The photo didn't upload. Check your connection and try again.")
        val model = vm()
        model.onCaptured(shot)
        model.submit()
        runCurrent()
        assertThat(uploader.uploads).containsExactly(shot.uri)
        assertThat(repo.submittedDocuments).isEmpty()
        assertThat(repo.submittedMediaIds).isEmpty()
        assertThat(model.state.value.step).isEqualTo(SelfieStep.Review(shot))
        assertThat(model.state.value.message?.text).isEqualTo("The photo didn't upload. Check your connection and try again.")
        assertThat(model.state.value.message?.type).isEqualTo(UsMessageType.Error)
    }

    @Test
    fun `a rejected record goes back to review, and nothing is submitted without a shot`() = runTest(dispatcher) {
        repo.submitDocumentAnswer = CaptainResult.Failure(CaptainError.Network(null))
        val model = vm()
        model.submit()
        runCurrent()
        assertThat(uploader.uploads).isEmpty()
        model.onCaptured(shot)
        model.submit()
        runCurrent()
        assertThat(repo.submittedDocuments).containsExactly("profile_photo")
        assertThat(model.state.value.step).isEqualTo(SelfieStep.Review(shot))
        assertThat(model.state.value.message?.type).isEqualTo(UsMessageType.Error)
    }

    @Test
    fun `one retake only`() = runTest(dispatcher) {
        val model = vm()
        model.onCaptured(shot)
        assertThat(model.state.value.canRetake).isTrue()
        model.retake()
        assertThat(model.state.value.step).isEqualTo(SelfieStep.Camera)
        assertThat(model.state.value.canRetake).isFalse()
        val second = shot.copy(path = "/cache/selfies/selfie-2.jpg", uri = "content://captain.selfie/selfies/selfie-2.jpg")
        model.onCaptured(second)
        model.retake()
        assertThat(model.state.value.step).isEqualTo(SelfieStep.Review(second))
    }
}
