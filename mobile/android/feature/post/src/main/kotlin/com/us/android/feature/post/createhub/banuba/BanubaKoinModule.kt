package com.us.android.feature.post.createhub.banuba

import android.content.Context
import android.content.Intent
import android.graphics.Typeface
import android.os.Bundle
import androidx.core.content.res.ResourcesCompat
import androidx.fragment.app.Fragment
import com.banuba.sdk.arcloud.data.source.ArEffectsRepositoryProvider
import com.banuba.sdk.cameraui.data.CameraConfig
import com.banuba.sdk.core.data.TrackData
import com.banuba.sdk.core.domain.DraftConfig
import com.banuba.sdk.core.ui.ContentFeatureProvider
import com.banuba.sdk.export.data.ExportParamsProvider
import com.banuba.sdk.veui.data.EditorConfig
import com.banuba.sdk.veui.domain.textonvideo.TextOnVideoTypeface
import com.banuba.sdk.veui.ui.TextOnVideoTypefaceProvider
import com.us.android.feature.post.createhub.REEL_MAX_DURATION_MS
import org.koin.android.ext.koin.androidContext
import org.koin.core.module.Module
import org.koin.core.qualifier.named
import org.koin.dsl.module
import java.io.File
import java.lang.ref.WeakReference
import com.us.android.core.designsystem.R as DesignR

/** Reel recording lengths offered by the camera's duration switcher: the cap, a minute, fifteen seconds. */
private const val ONE_MINUTE_MS = 60_000L
private const val FIFTEEN_SECONDS_MS = 15_000L

/**
 * Momentum's overrides of the SDK's Koin defaults. Registered last, so with
 * `allowOverride(true)` each of these replaces the vendor's binding.
 *
 *  - export: 1080p H.264 into the publish store ([MomentumExportParamsProvider]);
 *  - camera: five-minute cap (the reel cap), no external music;
 *  - editor: same cap, transitions on, stickers off (no Giphy key), music
 *    mixer off (music is off for now), drafts off;
 *  - text on video: Outfit and Figtree, not the SDK's Roboto.
 */
internal fun momentumBanubaModule(exportTarget: BanubaExportTarget): Module = module {
    // The SDK core resolves the AR-cloud effects repository even when no cloud
    // effects are used (a missing binding fails the graph at start), so it is
    // bound the way the vendor sample does: eagerly, on the backend repository.
    single<ArEffectsRepositoryProvider>(createdAtStart = true) {
        ArEffectsRepositoryProvider(arEffectsRepository = get(named("backendArEffectsRepository")))
    }
    factory<ExportParamsProvider> {
        MomentumExportParamsProvider(
            target = exportTarget,
            fallbackDir = File(androidContext().cacheDir, FALLBACK_EXPORT_DIR),
        )
    }
    single<CameraConfig> {
        CameraConfig(
            maxRecordedTotalVideoDurationMs = REEL_MAX_DURATION_MS,
            videoDurations = listOf(REEL_MAX_DURATION_MS, ONE_MINUTE_MS, FIFTEEN_SECONDS_MS),
            supportsExternalMusic = false,
            supportsTopAddAudio = false,
        )
    }
    single<EditorConfig> {
        EditorConfig(
            maxTotalVideoDurationMs = REEL_MAX_DURATION_MS,
            supportsTransitions = true,
            supportsStickersOnVideo = false,
            editorSupportsMusicMixer = false,
        )
    }
    factory<DraftConfig> { DraftConfig.DISABLED }
    single<ContentFeatureProvider<TrackData, Fragment>>(named(MUSIC_TRACK_PROVIDER)) { NoMusicTrackProvider() }
    single<TextOnVideoTypefaceProvider> { MomentumTextTypefaceProvider(androidContext()) }
}

private const val FALLBACK_EXPORT_DIR = "banuba_export"
private const val MUSIC_TRACK_PROVIDER = "musicTrackProvider"

/**
 * Music is off for now: the camera and editor hide their music entry points,
 * and should one still ask, there is no content and nothing to handle. The
 * binding must exist because the SDK's graph resolves it by name.
 */
internal class NoMusicTrackProvider : ContentFeatureProvider<TrackData, Fragment> {
    override fun requestContent(context: Context, extras: Bundle): ContentFeatureProvider.Result<TrackData>? = null

    override fun handleResult(
        hostComponent: WeakReference<Fragment>,
        intent: Intent,
        block: (TrackData?) -> Unit,
    ) = Unit
}

/**
 * The text tool's typefaces: Momentum's two faces from the design system's
 * font resources, so text burned into a reel is set like the rest of the app.
 */
internal class MomentumTextTypefaceProvider(private val context: Context) : TextOnVideoTypefaceProvider {
    override fun provide(): List<TextOnVideoTypeface> = listOf(
        typeface("Outfit", DesignR.font.outfit_variable),
        typeface("Figtree", DesignR.font.figtree_variable),
    )

    override fun clear() = Unit

    private fun typeface(name: String, resId: Int) = TextOnVideoTypeface(
        name = name,
        typeface = ResourcesCompat.getFont(context, resId) ?: Typeface.DEFAULT,
        ttfFile = null,
        resId = resId,
    )
}
