package com.us.android.feature.post.createhub.banuba

import com.banuba.sdk.core.VideoResolution
import com.banuba.sdk.ve.domain.VideoRangeList
import com.banuba.sdk.ve.effects.Effects
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.io.File
import java.util.Stack

/** Robolectric because the vendor's [com.banuba.sdk.export.data.ExportParams.Builder] reads `Uri.EMPTY` on construction. */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class MomentumExportParamsProviderTest {

    private val fallback = File(System.getProperty("java.io.tmpdir"), "banuba-fallback-test")

    private fun provide(target: BanubaExportTarget) = MomentumExportParamsProvider(target, fallback)
        .provideExportParams(
            effects = Effects(Stack(), Stack()),
            videoRangeList = VideoRangeList(emptyList()),
            musicEffects = emptyList(),
            videoVolume = 1f,
        )

    @Test
    fun `one export at 1080p H264 into the publish store under the creation key`() {
        val store = File(System.getProperty("java.io.tmpdir"), "reel_publish")
        val target = BanubaExportTarget().apply { path = File(store, "abc-123.video").absolutePath }

        val params = provide(target)

        assertEquals(1, params.size)
        val export = params.single()
        assertEquals(VideoResolution.Exact.FHD, export.resolution)
        assertFalse(export.useHevcIfPossible)
        assertEquals(store.absolutePath, export.destDir.absolutePath)
        assertEquals("abc-123", export.fileName)
        assertEquals(1f, export.volumeVideo)
        assertTrue(export.musicEffects.isEmpty())
    }

    @Test
    fun `without a target the export falls back to the cache directory`() {
        val params = provide(BanubaExportTarget())

        val export = params.single()
        assertEquals(fallback.absolutePath, export.destDir.absolutePath)
        assertEquals("reel", export.fileName)
        assertEquals(VideoResolution.Exact.FHD, export.resolution)
    }
}
