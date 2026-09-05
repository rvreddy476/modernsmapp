package com.us.android.feature.post.createhub.studio

import android.graphics.Bitmap
import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.post.createhub.CoverFrame
import com.us.android.feature.post.createhub.Filmstrip
import com.us.android.feature.post.createhub.ReelFrameExtractor
import com.us.android.feature.post.createhub.ReelFrameSeeker
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.File

/**
 * The studio's ViewModel: a picked source becomes an edit with the right
 * defaults, every tool changes exactly its part, and Next renders through
 * the exporter — reporting the percent, answering the file, and stopping
 * on Cancel.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ReelStudioViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private class FakeExporter : ReelExporter {
        val exports = mutableListOf<ReelEdit>()
        val targets = mutableListOf<File>()
        var outcome: ExportOutcome = ExportOutcome.Done("/cache/out.video", 3_000L)
        var progress: List<Int> = listOf(10, 42, 100)

        /** Held open until completed, so a test can cancel mid-export. */
        var hold: CompletableDeferred<Unit>? = null

        override suspend fun export(edit: ReelEdit, target: File, onProgress: (Int) -> Unit): ExportOutcome {
            exports += edit
            targets += target
            progress.forEach(onProgress)
            hold?.await()
            return outcome
        }
    }

    private val landscape = ReelSourceReader { uri -> ReelSource(uri, width = 1920, height = 1080, durationUs = TEN_S) }
    private val portrait = ReelSourceReader { uri -> ReelSource(uri, width = 1080, height = 1920, durationUs = TEN_S) }
    private val unreadable = ReelSourceReader { null }

    private val frames = ReelFrameExtractor { _, count ->
        Filmstrip.timestampsUs(TEN_S, count).mapIndexed { index, timeUs -> CoverFrame(index, timeUs, mockk<Bitmap>()) }
    }
    private val seeker = ReelFrameSeeker { _, _ -> mockk<Bitmap>() }

    private fun viewModel(
        sources: ReelSourceReader = portrait,
        exporter: FakeExporter = FakeExporter(),
    ) = ReelStudioViewModel(sources = sources, frames = frames, seeker = seeker, exporter = exporter)

    @Test
    fun `a picked source becomes an edit with the source's size, length, strip and first frame`() = runTest {
        val vm = viewModel(sources = landscape)

        vm.setSource("content://v/1")
        advanceUntilIdle()

        val state = vm.state.value
        val edit = state.edit!!
        assertThat(edit.width).isEqualTo(1920)
        assertThat(edit.height).isEqualTo(1080)
        assertThat(edit.durationUs).isEqualTo(TEN_S)
        assertThat(edit.mode).isEqualTo(FrameMode.FIT)
        assertThat(state.frames).hasSize(Filmstrip.FRAME_COUNT)
        assertThat(state.thumbnail).isNotNull()
        assertThat(state.reading).isFalse()
        assertThat(state.canExport).isTrue()
    }

    @Test
    fun `an unreadable source says so and offers nothing to export`() = runTest {
        val vm = viewModel(sources = unreadable)

        vm.setSource("content://v/1")
        advanceUntilIdle()

        assertThat(vm.state.value.unreadable).isTrue()
        assertThat(vm.state.value.edit).isNull()
        assertThat(vm.state.value.canExport).isFalse()
    }

    @Test
    fun `each tool changes its own part of the edit`() = runTest {
        val vm = viewModel(sources = landscape)
        vm.setSource("content://v/1")
        advanceUntilIdle()

        vm.setMode(FrameMode.FILL)
        vm.pan(100f, 400f)
        vm.setTrimStart(1_000_000L)
        vm.setTrimEnd(7_000_000L)
        vm.setSpeed(ReelSpeed.DOUBLE)
        vm.setLook(ReelLook.WARM)
        vm.setText("Sunday skate")
        vm.setTextStyle(TextPillStyle.NAVY)
        vm.moveText(0.2f, 1.4f)
        vm.selectTool(StudioTool.TEXT)

        val edit = vm.state.value.edit!!
        assertThat(edit.mode).isEqualTo(FrameMode.FILL)
        assertThat(edit.pan).isLessThan(0f)
        assertThat(edit.trimStartUs).isEqualTo(1_000_000L)
        assertThat(edit.trimEndUs).isEqualTo(7_000_000L)
        assertThat(edit.speed).isEqualTo(ReelSpeed.DOUBLE)
        assertThat(edit.look).isEqualTo(ReelLook.WARM)
        assertThat(edit.text).isEqualTo(TextPill("Sunday skate", TextPillStyle.NAVY, x = 0.2f, y = 1f))
        assertThat(vm.state.value.tool).isEqualTo(StudioTool.TEXT)

        vm.setText("")
        assertThat(vm.state.value.edit!!.text).isNull()
        vm.setText("one\nline\r\nonly")
        assertThat(vm.state.value.edit!!.text?.text).isEqualTo("one line only")
        vm.removeText()
        assertThat(vm.state.value.edit!!.text).isNull()
    }

    @Test
    fun `Next exports the edit to the target, reports the percent and hands the file over`() = runTest {
        val exporter = FakeExporter()
        val vm = viewModel(exporter = exporter)
        vm.setSource("content://v/1")
        advanceUntilIdle()
        vm.setLook(ReelLook.MONO)

        vm.startExport("/cache/reel_publish/key-1.video")
        advanceUntilIdle()

        assertThat(exporter.exports.single().look).isEqualTo(ReelLook.MONO)
        assertThat(exporter.targets.single().path).endsWith("key-1.video")
        assertThat(vm.state.value.export).isNull()
        assertThat(vm.state.value.exportedPath).isEqualTo("/cache/out.video")
        assertThat(vm.state.value.playing).isFalse()

        vm.consumeExported()
        assertThat(vm.state.value.exportedPath).isNull()
    }

    @Test
    fun `the sheet's percent follows the exporter and Cancel stops it without a file`() = runTest {
        val exporter = FakeExporter().apply {
            progress = listOf(42)
            hold = CompletableDeferred()
        }
        val vm = viewModel(exporter = exporter)
        vm.setSource("content://v/1")
        advanceUntilIdle()

        vm.startExport("/cache/key-1.video")
        advanceUntilIdle()
        assertThat(vm.state.value.export).isEqualTo(ReelStudioViewModel.Export(42))
        assertThat(vm.state.value.isExporting).isTrue()
        assertThat(vm.state.value.canExport).isFalse()

        vm.cancelExport()
        advanceUntilIdle()

        assertThat(vm.state.value.export).isNull()
        assertThat(vm.state.value.exportedPath).isNull()
        assertThat(vm.state.value.exportError).isNull()
    }

    @Test
    fun `a failed export says why and Next can be tried again`() = runTest {
        val exporter = FakeExporter().apply { outcome = ExportOutcome.Failed("Couldn't prepare the reel.") }
        val vm = viewModel(exporter = exporter)
        vm.setSource("content://v/1")
        advanceUntilIdle()

        vm.startExport("/cache/key-1.video")
        advanceUntilIdle()

        assertThat(vm.state.value.exportError).isEqualTo("Couldn't prepare the reel.")
        assertThat(vm.state.value.exportedPath).isNull()
        assertThat(vm.state.value.canExport).isTrue()
        vm.dismissExportError()
        assertThat(vm.state.value.exportError).isNull()
    }

    @Test
    fun `an edit over the reel cap cannot be exported until it is trimmed or sped up`() = runTest {
        val long = ReelSourceReader { uri -> ReelSource(uri, 1080, 1920, durationUs = 600L * 1_000_000L) }
        val exporter = FakeExporter()
        val vm = viewModel(sources = long, exporter = exporter)
        vm.setSource("content://v/1")
        advanceUntilIdle()

        assertThat(vm.state.value.canExport).isFalse()
        vm.startExport("/cache/key-1.video")
        advanceUntilIdle()
        assertThat(exporter.exports).isEmpty()

        vm.setSpeed(ReelSpeed.DOUBLE)
        assertThat(vm.state.value.canExport).isTrue()
    }

    @Test
    fun `clear forgets everything`() = runTest {
        val vm = viewModel()
        vm.setSource("content://v/1")
        advanceUntilIdle()

        vm.clear()

        assertThat(vm.state.value).isEqualTo(ReelStudioViewModel.StudioState())
    }

    private companion object {
        const val TEN_S = 10_000_000L
    }
}
