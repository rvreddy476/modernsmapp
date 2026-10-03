package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.clips.ClipCopy
import com.us.android.feature.dating.clips.ClipEngine
import com.us.android.feature.dating.clips.ClipFileStore
import com.us.android.feature.dating.clips.ClipFocus
import com.us.android.feature.dating.clips.ClipKind
import com.us.android.feature.dating.clips.ClipPlayback
import com.us.android.feature.dating.clips.ClipPlayerPhase
import com.us.android.feature.dating.clips.ClipRules
import com.us.android.feature.dating.clips.ClipSource
import com.us.android.feature.dating.clips.ClipStatus
import com.us.android.feature.dating.clips.ClipUploader
import com.us.android.feature.dating.clips.PromptClipUi
import com.us.android.feature.dating.clips.VideoDurationReader
import com.us.android.feature.dating.clips.VoiceRecorder
import com.us.android.feature.dating.home.toUi
import com.us.android.feature.dating.network.CardClipDto
import com.us.android.feature.dating.network.DetailPromptDto
import com.us.android.feature.dating.network.ProfileDetailDto
import com.us.android.feature.dating.network.PromptAnswerDto
import com.us.android.feature.dating.network.PromptClipViewDto
import com.us.android.feature.dating.onboarding.ClipWork
import com.us.android.feature.dating.onboarding.PromptsViewModel
import com.us.android.feature.dating.photos.UploadOutcome
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Rule
import org.junit.Test
import retrofit2.Response
import java.io.File

/**
 * Mechanic M15, voice and video prompt answers: the card's player state
 * machine, the 30-second guards, how the owner's clip status reads, every
 * refusal, the CLIP_NOT_READY retry, and that no voice recording outlives its
 * upload or a cancel. The microphone, the picker and ExoPlayer are ports here:
 * none of their Android halves runs on the JVM.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PromptClipsTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val directory = File(System.getProperty("java.io.tmpdir"), "dating-clips-test-${System.nanoTime()}")

    @After
    fun tearDown() {
        directory.deleteRecursively()
    }

    // ── fakes ────────────────────────────────────────────────────────────────

    private class FakeEngine : ClipEngine {
        val loads = mutableListOf<String>()
        var runs = false
        var level = -1f
        var rewinds = 0
        var released = false
        override fun load(url: String) {
            loads += url
        }
        override fun setPlaying(play: Boolean) {
            runs = play
        }
        override fun setVolume(volume: Float) {
            level = volume
        }
        override fun rewind() {
            rewinds++
        }
        override fun release() {
            released = true
        }
    }

    private inner class FakeRecorder : VoiceRecorder {
        private val files = ClipFileStore(directory)
        var starts = 0
        var stops = 0
        var cancels = 0
        var startFails = false
        var usable = true
        var current: File? = null

        override fun start(): File? {
            if (startFails) return null
            starts++
            return files.next(starts.toLong()).also {
                it.writeBytes(byteArrayOf(1, 2, 3))
                current = it
            }
        }

        override fun stop(): Boolean {
            stops++
            return usable
        }

        override fun cancel() {
            cancels++
            current?.delete()
        }
    }

    private class FakeUploader : ClipUploader {
        val sources = mutableListOf<ClipSource>()

        /** The temp file as the uploader saw it: it must still exist while uploading. */
        val existedWhileUploading = mutableListOf<Boolean>()
        var outcome: UploadOutcome = UploadOutcome.Ready("media-1")
        override suspend fun upload(source: ClipSource, onProgress: (Float) -> Unit): UploadOutcome {
            sources += source
            (source as? ClipSource.Recorded)?.let { existedWhileUploading += it.file.exists() }
            onProgress(0.5f)
            return outcome
        }
    }

    private val api = FakeDatingApi().apply {
        promptsResponse = { ok(emptyList()) }
    }
    private val session = DatingSession()
    private val recorder = FakeRecorder()
    private val uploader = FakeUploader()
    private val durations = mutableMapOf<String, Long?>()
    private val reader = VideoDurationReader { uri -> durations[uri] }

    private fun TestScope.vm(): PromptsViewModel =
        PromptsViewModel(api.repository(), session, uploader, recorder, reader).also { advanceUntilIdle() }

    /** Records a voice answer for prompt 1 and stops it by hand after [seconds]. */
    private fun TestScope.recordAndStop(model: PromptsViewModel, seconds: Long = 5) {
        model.recordVoice(1, micPermitted = true)
        advanceTimeBy(seconds * 1_000L)
        runCurrent()
        model.stopRecording()
        runCurrent()
    }

    // ── the card's player ────────────────────────────────────────────────────

    private fun playback(kind: ClipKind, engines: MutableList<FakeEngine> = mutableListOf()) =
        ClipPlayback(kind, "https://api.test/v1/dating/people/u/prompts/1/clip") { FakeEngine().also(engines::add) }

    @Test
    fun `nothing loads or plays until Play is tapped`() {
        val engines = mutableListOf<FakeEngine>()
        val audio = playback(ClipKind.AUDIO, engines)
        val video = playback(ClipKind.VIDEO, engines)

        assertThat(audio.state.value.phase).isEqualTo(ClipPlayerPhase.IDLE)
        assertThat(video.state.value.phase).isEqualTo(ClipPlayerPhase.IDLE)
        assertThat(engines).isEmpty()
        // A video starts muted; a voice answer has nothing to mute.
        assertThat(video.state.value.muted).isTrue()
        assertThat(audio.state.value.muted).isFalse()
    }

    @Test
    fun `a voice answer loads on Play, plays with sound, pauses and resumes`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.AUDIO, engines)

        p.toggle()
        val engine = engines.single()
        assertThat(engine.loads).containsExactly("https://api.test/v1/dating/people/u/prompts/1/clip")
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.LOADING)
        assertThat(engine.runs).isTrue()
        assertThat(engine.level).isEqualTo(1f)

        p.onReady()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.PLAYING)

        p.toggle()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.PAUSED)
        assertThat(engine.runs).isFalse()

        p.toggle()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.PLAYING)
        // Resuming never loads again.
        assertThat(engine.loads).hasSize(1)
        assertThat(engines).hasSize(1)
    }

    @Test
    fun `a video plays muted until the viewer turns the sound on`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.VIDEO, engines)

        p.toggle()
        p.onReady()
        val engine = engines.single()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.PLAYING)
        assertThat(engine.level).isEqualTo(0f)

        p.toggleMute()
        assertThat(p.state.value.muted).isFalse()
        assertThat(engine.level).isEqualTo(1f)

        p.toggleMute()
        assertThat(engine.level).isEqualTo(0f)
    }

    @Test
    fun `mute does nothing to a voice answer`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.AUDIO, engines)
        p.toggle()
        p.toggleMute()
        assertThat(p.state.value.muted).isFalse()
        assertThat(engines.single().level).isEqualTo(1f)
    }

    @Test
    fun `paused while loading reads paused, then ready stays paused`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.AUDIO, engines)
        p.toggle()
        p.toggle()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.PAUSED)
        p.onReady()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.PAUSED)
        assertThat(engines.single().runs).isFalse()
    }

    @Test
    fun `played through goes back to the start, paused`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.AUDIO, engines)
        p.toggle()
        p.onReady()

        p.onEnded()

        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.PAUSED)
        assertThat(engines.single().rewinds).isEqualTo(1)
        assertThat(engines.single().runs).isFalse()
    }

    @Test
    fun `an error fails it, and Play tries again on the same player`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.VIDEO, engines)
        p.toggle()

        p.onError()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.FAILED)
        assertThat(engines.single().runs).isFalse()

        p.toggle()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.LOADING)
        assertThat(engines.single().loads).hasSize(2)
        assertThat(engines).hasSize(1)
    }

    @Test
    fun `release frees the player for good and ignores what arrives late`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.AUDIO, engines)
        p.toggle()
        p.onReady()

        p.release()
        val engine = engines.single()
        assertThat(engine.released).isTrue()
        assertThat(engine.runs).isFalse()
        assertThat(p.state.value.released).isTrue()
        assertThat(p.currentEngine).isNull()

        p.onReady()
        p.onError()
        p.toggle()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.IDLE)
        assertThat(engines).hasSize(1)
    }

    @Test
    fun `a clip released before it was ever played made no player`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.VIDEO, engines)
        p.release()
        assertThat(engines).isEmpty()
    }

    @Test
    fun `pause stops it, and does nothing when it was not playing`() {
        val engines = mutableListOf<FakeEngine>()
        val p = playback(ClipKind.AUDIO, engines)
        p.pause()
        assertThat(engines).isEmpty()

        p.toggle()
        p.onReady()
        p.pause()
        assertThat(p.state.value.phase).isEqualTo(ClipPlayerPhase.PAUSED)
        assertThat(engines.single().runs).isFalse()
    }

    @Test
    fun `starting one clip pauses the one before`() {
        val engines = mutableListOf<FakeEngine>()
        val first = playback(ClipKind.AUDIO, engines)
        val second = playback(ClipKind.VIDEO, engines)
        val focus = ClipFocus()

        focus.starting("a", first)
        first.toggle()
        first.onReady()
        focus.starting("b", second)
        second.toggle()

        assertThat(first.state.value.phase).isEqualTo(ClipPlayerPhase.PAUSED)
        assertThat(second.state.value.phase).isEqualTo(ClipPlayerPhase.LOADING)
    }

    // ── rules ────────────────────────────────────────────────────────────────

    @Test
    fun `thirty seconds is the most, and an unknown length is refused`() {
        assertThat(ClipRules.fits(30_000L)).isTrue()
        assertThat(ClipRules.fits(1L)).isTrue()
        assertThat(ClipRules.fits(30_001L)).isFalse()
        assertThat(ClipRules.fits(0L)).isFalse()
        assertThat(ClipRules.fits(null)).isFalse()
    }

    @Test
    fun `durations read as minutes and seconds`() {
        assertThat(ClipRules.duration(8_000L)).isEqualTo("0:08")
        assertThat(ClipRules.duration(30_000L)).isEqualTo("0:30")
        assertThat(ClipRules.duration(65_000L)).isEqualTo("1:05")
        assertThat(ClipRules.duration(0L)).isEmpty()
        assertThat(ClipRules.secondsLeft(24)).isEqualTo("0:24 left")
        assertThat(ClipRules.secondsLeft(0)).isEqualTo("0:00 left")
        assertThat(ClipCopy.summary(ClipKind.VIDEO, 0L)).isEqualTo("Video answer")
    }

    @Test
    fun `the owner's status reads in three ways and fails closed to being checked`() {
        assertThat(ClipStatus.fromWire("approved")).isEqualTo(ClipStatus.LIVE)
        assertThat(ClipStatus.fromWire("pending")).isEqualTo(ClipStatus.CHECKING)
        assertThat(ClipStatus.fromWire("pending_review")).isEqualTo(ClipStatus.CHECKING)
        assertThat(ClipStatus.fromWire("rejected")).isEqualTo(ClipStatus.REJECTED)
        assertThat(ClipStatus.fromWire("something_new")).isEqualTo(ClipStatus.CHECKING)

        val rejected = ClipRules.ownClip(answer(1, status = "rejected", reason = "This clip can't be shown on your profile."))!!
        assertThat(rejected.statusLine).isEqualTo("This clip can't be shown on your profile.")
        assertThat(ClipRules.ownClip(answer(1, status = "rejected", reason = null))!!.statusLine).isEqualTo(ClipCopy.REJECTED)
        assertThat(ClipRules.ownClip(answer(1, status = "pending"))!!.statusLine).isEqualTo("Being checked")
        assertThat(ClipRules.ownClip(answer(1, status = "pending_review"))!!.statusLine).isEqualTo("Being checked")
        assertThat(ClipRules.ownClip(answer(1, status = "approved"))!!.statusLine).isEqualTo("Live on your profile")
        // Go omits every clip field on an answer without one.
        assertThat(ClipRules.ownClip(PromptAnswerDto(promptId = 1, answer = "words"))).isNull()
        assertThat(ClipRules.ownClip(answer(1, kind = "gif"))).isNull()
    }

    @Test
    fun `a card loads only the exact clip route, and a clip-only answer is kept`() {
        val urls = photoUrls()
        val detail = ProfileDetailDto(
            prompts = listOf(
                DetailPromptDto(1, "Q1", "", CardClipDto("audio", 8_000L, "/v1/dating/people/u-1/prompts/1/clip")),
                DetailPromptDto(2, "Q2", "", CardClipDto("video", 9_000L, "https://elsewhere.test/clip.mp4")),
                DetailPromptDto(3, "Q3", "words", CardClipDto("gif", 9_000L, "/v1/dating/people/u-1/prompts/3/clip")),
                DetailPromptDto(4, "Q4", ""),
                DetailPromptDto(5, "Q5", "", CardClipDto("video", 0L, "/v1/dating/people/u-1/prompts/5/clip?x=1")),
            ),
        ).toUi(urls)!!

        assertThat(detail.prompts.map { it.promptId }).containsExactly(1, 3).inOrder()
        assertThat(detail.prompts[0].clip).isEqualTo(PromptClipUi(ClipKind.AUDIO, 8_000L, "https://api.test/v1/dating/people/u-1/prompts/1/clip"))
        // Words with a clip we cannot play: the words stay, the clip does not.
        assertThat(detail.prompts[1].answer).isEqualTo("words")
        assertThat(detail.prompts[1].clip).isNull()
    }

    // ── the editor: recording ────────────────────────────────────────────────

    @Test
    fun `without the microphone the explanation comes first and nothing is recorded`() = runTest(dispatcher) {
        val model = vm()

        model.recordVoice(1, micPermitted = false)
        assertThat(model.state.value.micRationaleFor).isEqualTo(1)
        assertThat(recorder.starts).isEqualTo(0)

        model.dismissMicRationale()
        assertThat(model.state.value.micRationaleFor).isNull()
        assertThat(recorder.starts).isEqualTo(0)
    }

    @Test
    fun `a refused microphone says so and records nothing, a granted one records`() = runTest(dispatcher) {
        val model = vm()

        model.recordVoice(1, micPermitted = false)
        model.micPermissionResult(granted = false)
        assertThat(recorder.starts).isEqualTo(0)
        assertThat(model.state.value.clipWork).isNull()
        assertThat(model.state.value.message?.text).isEqualTo(ClipCopy.MIC_DENIED)

        model.recordVoice(1, micPermitted = false)
        model.micPermissionResult(granted = true)
        assertThat(recorder.starts).isEqualTo(1)
        assertThat(model.state.value.clipWork).isEqualTo(ClipWork.Recording(1, 30))
        model.cancelRecording()
    }

    @Test
    fun `the countdown runs from 30 and the recording stops itself inside 30 seconds`() = runTest(dispatcher) {
        val model = vm()
        model.recordVoice(1, micPermitted = true)
        assertThat(model.state.value.clipWork).isEqualTo(ClipWork.Recording(1, 30))

        advanceTimeBy(10_000L)
        runCurrent()
        assertThat(model.state.value.clipWork).isEqualTo(ClipWork.Recording(1, 20))

        advanceTimeBy(19_000L)
        runCurrent()
        assertThat(model.state.value.clipWork).isEqualTo(ClipWork.Recording(1, 1))
        assertThat(recorder.stops).isEqualTo(0)

        // The last second is cut short: stopped at 29.6 s, before the server's limit.
        advanceTimeBy(PromptsViewModel.LAST_SECOND_MILLIS)
        runCurrent()
        assertThat(recorder.stops).isEqualTo(1)
        advanceUntilIdle()

        val sent = uploader.sources.single() as ClipSource.Recorded
        assertThat(sent.kind).isEqualTo(ClipKind.AUDIO)
        assertThat(api.clipWrites).containsExactly(1 to "media-1")
        assertThat(model.state.value.clipWork).isNull()
    }

    @Test
    fun `stop uploads at once, and the voice file is deleted after the upload`() = runTest(dispatcher) {
        val model = vm()
        recordAndStop(model)
        advanceUntilIdle()

        val sent = uploader.sources.single() as ClipSource.Recorded
        assertThat(uploader.existedWhileUploading).containsExactly(true)
        assertThat(sent.file.exists()).isFalse()
        assertThat(directory.listFiles().orEmpty()).isEmpty()
        assertThat(api.clipWrites).containsExactly(1 to "media-1")
    }

    @Test
    fun `a failed upload still deletes the voice file and says why`() = runTest(dispatcher) {
        uploader.outcome = UploadOutcome.Failed("The clip didn't upload. Check your connection and try again.")
        val model = vm()
        recordAndStop(model)
        advanceUntilIdle()

        assertThat((uploader.sources.single() as ClipSource.Recorded).file.exists()).isFalse()
        assertThat(api.clipWrites).isEmpty()
        assertThat(model.state.value.clipWork).isNull()
        assertThat(model.state.value.message?.text).isEqualTo("The clip didn't upload. Check your connection and try again.")
    }

    @Test
    fun `cancel keeps nothing - no upload and the temp file is gone`() = runTest(dispatcher) {
        val model = vm()
        model.recordVoice(1, micPermitted = true)
        val file = checkNotNull(recorder.current)
        assertThat(file.exists()).isTrue()

        model.cancelRecording()
        advanceUntilIdle()

        assertThat(recorder.cancels).isEqualTo(1)
        assertThat(file.exists()).isFalse()
        assertThat(uploader.sources).isEmpty()
        assertThat(model.state.value.clipWork).isNull()
        // The countdown went with it.
        advanceTimeBy(40_000L)
        assertThat(recorder.stops).isEqualTo(0)
    }

    @Test
    fun `an unusable recording is deleted and nothing is uploaded`() = runTest(dispatcher) {
        recorder.usable = false
        val model = vm()
        model.recordVoice(1, micPermitted = true)
        val file = checkNotNull(recorder.current)
        model.stopRecording()
        advanceUntilIdle()

        assertThat(file.exists()).isFalse()
        assertThat(uploader.sources).isEmpty()
        assertThat(model.state.value.message?.text).isEqualTo(ClipCopy.RECORD_FAILED)
    }

    @Test
    fun `a microphone that will not start says so`() = runTest(dispatcher) {
        recorder.startFails = true
        val model = vm()
        model.recordVoice(1, micPermitted = true)

        assertThat(model.state.value.clipWork).isNull()
        assertThat(model.state.value.message?.text).isEqualTo(ClipCopy.RECORD_FAILED)
    }

    @Test
    fun `one clip at a time`() = runTest(dispatcher) {
        val model = vm()
        model.recordVoice(1, micPermitted = true)
        model.recordVoice(2, micPermitted = true)
        model.videoPicked(2, "content://video/1")

        assertThat(recorder.starts).isEqualTo(1)
        assertThat(model.state.value.clipWork?.promptId).isEqualTo(1)
        model.cancelRecording()
    }

    // ── the editor: video ────────────────────────────────────────────────────

    @Test
    fun `a video over 30 seconds is refused before upload`() = runTest(dispatcher) {
        durations["content://video/long"] = 30_001L
        val model = vm()
        model.videoPicked(1, "content://video/long")
        advanceUntilIdle()

        assertThat(uploader.sources).isEmpty()
        assertThat(api.clipWrites).isEmpty()
        assertThat(model.state.value.clipWork).isNull()
        assertThat(model.state.value.message?.text).isEqualTo(ClipCopy.VIDEO_TOO_LONG)
    }

    @Test
    fun `a video whose length cannot be read is refused before upload`() = runTest(dispatcher) {
        val model = vm()
        model.videoPicked(1, "content://video/unknown")
        advanceUntilIdle()

        assertThat(uploader.sources).isEmpty()
        assertThat(model.state.value.message?.text).isEqualTo(ClipCopy.VIDEO_UNREADABLE)
    }

    @Test
    fun `a 30-second video uploads and attaches, live on the profile`() = runTest(dispatcher) {
        durations["content://video/ok"] = 30_000L
        val model = vm()
        model.videoPicked(1, "content://video/ok")
        advanceUntilIdle()

        assertThat(uploader.sources).containsExactly(ClipSource.Picked("content://video/ok"))
        assertThat(api.clipWrites).containsExactly(1 to "media-1")
        val clip = model.state.value.clips.getValue(1)
        assertThat(clip.kind).isEqualTo(ClipKind.VIDEO)
        assertThat(clip.status).isEqualTo(ClipStatus.LIVE)
        assertThat(model.state.value.message?.text).isEqualTo(ClipCopy.ADDED_LIVE)
        // A clip-only answer is an answer.
        assertThat(model.state.value.answers).containsEntry(1, "")
    }

    @Test
    fun `nothing chosen in the picker does nothing`() = runTest(dispatcher) {
        val model = vm()
        model.videoPicked(1, null)
        advanceUntilIdle()
        assertThat(model.state.value.clipWork).isNull()
        assertThat(model.state.value.message).isNull()
    }

    // ── the editor: attaching ────────────────────────────────────────────────

    @Test
    fun `a voice answer waiting for a moderator reads as being checked`() = runTest(dispatcher) {
        api.clipResponse = { _, _ -> ok(fixture("prompt_clip_put_200_pending_review.json", PromptClipViewDto.serializer())) }
        val model = vm()
        recordAndStop(model)
        advanceUntilIdle()

        val clip = model.state.value.clips.getValue(1)
        assertThat(clip.status).isEqualTo(ClipStatus.CHECKING)
        assertThat(clip.statusLine).isEqualTo("Being checked")
        assertThat(model.state.value.message?.text).isEqualTo(ClipCopy.ADDED_CHECKING)
    }

    @Test
    fun `a clip rejected on arrival says why`() = runTest(dispatcher) {
        api.clipResponse = { promptId, _ -> ok(PromptClipViewDto(promptId, "audio", 5_000L, "rejected", "This clip can't be shown on your profile.")) }
        val model = vm()
        recordAndStop(model)
        advanceUntilIdle()

        assertThat(model.state.value.clips.getValue(1).status).isEqualTo(ClipStatus.REJECTED)
        assertThat(model.state.value.message?.type).isEqualTo(UsMessageType.Error)
        assertThat(model.state.value.message?.text).isEqualTo("This clip can't be shown on your profile.")
    }

    @Test
    fun `CLIP_NOT_READY is retried with backoff until it attaches`() = runTest(dispatcher) {
        var answers = 0
        api.clipResponse = { promptId, _ ->
            answers++
            if (answers <= 2) {
                refusedWithFixture(409, "prompt_clip_put_409_not_ready.json")
            } else {
                ok(fixture("prompt_clip_put_200_approved.json", PromptClipViewDto.serializer()).copy(promptId = promptId))
            }
        }
        val model = vm()
        recordAndStop(model)
        runCurrent()
        assertThat(api.clipWrites).hasSize(1)
        assertThat(model.state.value.clipWork).isEqualTo(ClipWork.Attaching(1))

        advanceTimeBy(PromptsViewModel.NOT_READY_BACKOFF_MILLIS[0] - 1)
        runCurrent()
        assertThat(api.clipWrites).hasSize(1)
        advanceTimeBy(1)
        runCurrent()
        assertThat(api.clipWrites).hasSize(2)
        advanceTimeBy(PromptsViewModel.NOT_READY_BACKOFF_MILLIS[1])
        runCurrent()
        assertThat(api.clipWrites).hasSize(3)

        assertThat(model.state.value.clipWork).isNull()
        assertThat(model.state.value.clips.getValue(1).status).isEqualTo(ClipStatus.LIVE)
    }

    @Test
    fun `CLIP_NOT_READY every time gives up after three retries and says still processing`() = runTest(dispatcher) {
        api.clipResponse = { _, _ -> refusedWithFixture(409, "prompt_clip_put_409_not_ready.json") }
        val model = vm()
        recordAndStop(model)
        advanceUntilIdle()

        assertThat(api.clipWrites).hasSize(1 + PromptsViewModel.NOT_READY_BACKOFF_MILLIS.size)
        assertThat(api.clipWrites.map { it.second }.distinct()).containsExactly("media-1")
        assertThat(model.state.value.clipWork).isNull()
        assertThat(model.state.value.clips).isEmpty()
        assertThat(model.state.value.message?.text).isEqualTo("Your clip is still processing. Try again shortly.")
    }

    @Test
    fun `each refusal reads in its own words and is not retried`() = runTest(dispatcher) {
        val refusals: List<Pair<() -> Response<ApiEnvelope<PromptClipViewDto>>, String>> = listOf(
            { refusedWithFixture<PromptClipViewDto>(422, "prompt_clip_put_422_too_long.json") } to "Keep your clip to 30 seconds or less.",
            { refused<PromptClipViewDto>(422, "CLIP_UNSUPPORTED") } to ClipCopy.UNSUPPORTED,
            { refusedWithFixture<PromptClipViewDto>(404, "prompt_clip_put_404_media_not_found.json") } to ClipCopy.MEDIA_NOT_FOUND,
            { refused<PromptClipViewDto>(503, "CLIP_MEDIA_UNAVAILABLE") } to ClipCopy.UNAVAILABLE,
        )
        refusals.forEach { (response, words) ->
            api.clipWrites.clear()
            api.clipResponse = { _, _ -> response() }
            val model = vm()
            recordAndStop(model)
            advanceUntilIdle()

            assertThat(api.clipWrites).hasSize(1)
            assertThat(model.state.value.message?.text).isEqualTo(words)
            assertThat(model.state.value.clipWork).isNull()
            // Still offered: only the feature being off hides the controls.
            assertThat(model.state.value.clipsEnabled).isTrue()
        }
    }

    @Test
    fun `MECHANIC_NOT_ENABLED hides the clip controls for the session`() = runTest(dispatcher) {
        api.clipResponse = { _, _ -> refusedWithFixture(404, "prompt_clip_put_404_not_enabled.json") }
        val model = vm()
        recordAndStop(model)
        advanceUntilIdle()

        assertThat(model.state.value.clipsEnabled).isFalse()
        assertThat(model.state.value.clipWork).isNull()
        assertThat(session.isMechanicDisabled(ClipRules.MECHANIC)).isTrue()

        // Hidden means no new recording either, here or on the next visit.
        model.recordVoice(2, micPermitted = true)
        assertThat(recorder.starts).isEqualTo(1)
        assertThat(vm().state.value.clipsEnabled).isFalse()
    }

    // ── the editor: loading and removing ────────────────────────────────────

    @Test
    fun `the owner's clips load with the answers`() = runTest(dispatcher) {
        api.promptsResponse = {
            ok(listOf(answer(1, status = "approved"), answer(2, kind = "audio", status = "rejected", reason = "Not allowed."), PromptAnswerDto(promptId = 3, answer = "words")))
        }
        val model = vm()

        val clips = model.state.value.clips
        assertThat(clips.keys).containsExactly(1, 2)
        assertThat(clips.getValue(1).statusLine).isEqualTo(ClipCopy.LIVE)
        assertThat(clips.getValue(2).statusLine).isEqualTo("Not allowed.")
        assertThat(model.state.value.answers).containsExactly(1, "", 2, "", 3, "words")
    }

    @Test
    fun `removing a clip-only answer's clip removes the answer, words stay otherwise`() = runTest(dispatcher) {
        api.promptsResponse = { ok(listOf(answer(1, status = "approved"), answer(2, status = "approved", words = "Hello"))) }
        val model = vm()

        model.removeClip(1)
        advanceUntilIdle()
        model.removeClip(2)
        advanceUntilIdle()

        assertThat(api.clipDeletes).containsExactly(1, 2).inOrder()
        assertThat(model.state.value.clips).isEmpty()
        assertThat(model.state.value.answers).containsExactly(2, "Hello")
        assertThat(model.state.value.message?.text).isEqualTo(ClipCopy.REMOVED)
    }

    @Test
    fun `removing when the feature is off hides the controls`() = runTest(dispatcher) {
        api.promptsResponse = { ok(listOf(answer(1, status = "approved"))) }
        api.clipDeleteResponse = { refusedWithFixture(404, "prompt_clip_put_404_not_enabled.json") }
        val model = vm()

        model.removeClip(1)
        advanceUntilIdle()

        assertThat(model.state.value.clipsEnabled).isFalse()
        assertThat(model.state.value.clips.keys).containsExactly(1)
    }

    @Test
    fun `the words of an answer with a clip cannot be cleared on their own`() = runTest(dispatcher) {
        api.promptsResponse = { ok(listOf(answer(1, status = "approved", words = "Hello"))) }
        val model = vm()

        model.answer(1, "   ")
        advanceUntilIdle()

        assertThat(model.state.value.message?.text).isEqualTo(PromptsViewModel.KEEP_WORDS)
        assertThat(model.state.value.answers).containsExactly(1, "Hello")
    }

    private fun answer(
        promptId: Int,
        kind: String = "video",
        status: String = "approved",
        reason: String? = null,
        words: String = "",
    ) = PromptAnswerDto(
        promptId = promptId,
        answer = words,
        clipKind = kind,
        clipDurationMs = 12_000L,
        clipStatus = status,
        clipReason = reason,
    )
}
