package com.us.android.feature.post.createhub.banuba

import com.banuba.sdk.core.VideoResolution
import com.banuba.sdk.export.data.ExportParams
import com.banuba.sdk.export.data.ExportParamsProvider
import com.banuba.sdk.ve.domain.VideoRangeList
import com.banuba.sdk.ve.effects.Effects
import com.banuba.sdk.ve.effects.music.MusicEffect
import java.io.File
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Where the next Banuba export lands: the publish view model's export target
 * for the reel being made, set by the launcher just before the editor opens.
 *
 * The SDK asks its [ExportParamsProvider] at export time with no way to pass
 * a per-launch argument through the activity intent, so this one-slot holder
 * bridges the two. A process holds at most one reel flow, so one slot is
 * exactly enough.
 */
@Singleton
class BanubaExportTarget @Inject constructor() {
    @Volatile
    var path: String? = null
}

/**
 * The export the reel pipeline expects: one 1080p H.264 MP4 at full volume,
 * with every effect burned in, written into the publish store's directory
 * under the creation key — the same file the Media3 studio would have
 * written, so [com.us.android.feature.post.createhub.ReelPublishViewModel.onReelExported]
 * gets a file path either way. HEVC is off because the upload pipeline and
 * the web player are H.264.
 */
internal class MomentumExportParamsProvider(
    private val target: BanubaExportTarget,
    private val fallbackDir: File,
) : ExportParamsProvider {

    override fun provideExportParams(
        effects: Effects,
        videoRangeList: VideoRangeList,
        musicEffects: List<MusicEffect>,
        videoVolume: Float,
    ): List<ExportParams> {
        val file = target.path?.let(::File)
        val dir = (file?.parentFile ?: fallbackDir).apply { mkdirs() }
        val name = file?.nameWithoutExtension?.takeIf { it.isNotBlank() } ?: DEFAULT_FILE_NAME
        val params = ExportParams.Builder(VideoResolution.Exact.FHD)
            .effects(effects)
            .videoRangeList(videoRangeList)
            .fileName(name)
            .destDir(dir)
            .musicEffects(musicEffects)
            .volumeVideo(videoVolume)
            .useHevcIfPossible(false)
            .build()
        return listOf(params)
    }

    private companion object {
        const val DEFAULT_FILE_NAME = "reel"
    }
}
