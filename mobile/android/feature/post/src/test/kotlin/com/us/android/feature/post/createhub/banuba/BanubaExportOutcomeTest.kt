package com.us.android.feature.post.createhub.banuba

import android.net.Uri
import android.os.Bundle
import com.banuba.sdk.export.data.ExportError
import com.banuba.sdk.export.data.ExportResult
import com.banuba.sdk.export.data.ExportedVideo
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/** Robolectric for a real [Uri] and [Bundle]; the vendor's result types are Parcelable over both. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class BanubaExportOutcomeTest {

    private fun success(vararg videos: ExportedVideo) = ExportResult.Success(
        videos.toList(),
        Uri.EMPTY,
        Uri.EMPTY,
        Bundle(),
    )

    private fun video(uri: Uri) = ExportedVideo(uri, 1080, 1920, Uri.EMPTY, Uri.EMPTY, 5_000L)

    @Test
    fun `a success with a file on disk is Exported with its path`() {
        val result = success(video(Uri.parse("file:///data/cache/reel_publish/key.mp4")))

        assertEquals(BanubaExportOutcome.Exported("/data/cache/reel_publish/key.mp4"), exportOutcomeOf(result))
    }

    @Test
    fun `the first video is the reel`() {
        val result = success(
            video(Uri.parse("file:///data/first.mp4")),
            video(Uri.parse("file:///data/second.mp4")),
        )

        assertEquals(BanubaExportOutcome.Exported("/data/first.mp4"), exportOutcomeOf(result))
    }

    @Test
    fun `a success without a video is Failed`() {
        assertTrue(exportOutcomeOf(success()) is BanubaExportOutcome.Failed)
    }

    @Test
    fun `a video that is not a plain file is Failed`() {
        val result = success(video(Uri.parse("content://media/external/video/1")))

        assertTrue(exportOutcomeOf(result) is BanubaExportOutcome.Failed)
    }

    @Test
    fun `an error carries a message for the person`() {
        val outcome = exportOutcomeOf(ExportResult.Error(ExportError.INVALID_LICENSE))

        assertEquals(BanubaExportOutcome.Failed("The advanced editor licence is not valid."), outcome)
    }

    @Test
    fun `no result is a cancel`() {
        assertEquals(BanubaExportOutcome.Cancelled, exportOutcomeOf(null))
    }

    @Test
    fun `an inactive export is a cancel`() {
        assertEquals(BanubaExportOutcome.Cancelled, exportOutcomeOf(ExportResult.Inactive))
    }
}
