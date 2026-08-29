package com.us.android.feature.call.ui

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.us.android.core.call.engine.CallEngine
import org.webrtc.RendererCommon
import org.webrtc.SurfaceViewRenderer
import org.webrtc.VideoTrack

/**
 * Remote video full-bleed, local preview picture-in-picture. Renderers are
 * classic views ([SurfaceViewRenderer]) initialized against the engine's EGL
 * context; each is released with its composition.
 */
@Composable
internal fun CallVideoLayer(
    engine: CallEngine?,
    remoteAvailable: Boolean,
    localEnabled: Boolean,
) {
    engine ?: return
    Box(modifier = Modifier.fillMaxSize()) {
        if (remoteAvailable) {
            TrackRenderer(
                engine = engine,
                trackOf = { it.remoteVideoTrack() },
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            Text(
                text = "Waiting for video…",
                color = Color.Gray,
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.align(Alignment.Center),
            )
        }
        if (localEnabled) {
            TrackRenderer(
                engine = engine,
                trackOf = { it.localVideoTrack() },
                modifier = Modifier
                    .align(Alignment.TopEnd)
                    .padding(16.dp)
                    .size(width = 110.dp, height = 160.dp),
            )
        }
    }
}

@Composable
private fun TrackRenderer(
    engine: CallEngine,
    trackOf: (CallEngine) -> VideoTrack?,
    modifier: Modifier,
) {
    var renderer: SurfaceViewRenderer? = null
    var sunkTrack: VideoTrack? = null
    AndroidView(
        modifier = modifier,
        factory = { context ->
            SurfaceViewRenderer(context).apply {
                val egl = engine.eglContext()
                if (egl != null) init(egl, null)
                setScalingType(RendererCommon.ScalingType.SCALE_ASPECT_FILL)
                renderer = this
            }
        },
        update = { view ->
            val track = trackOf(engine)
            if (track !== sunkTrack) {
                sunkTrack?.removeSink(view)
                track?.addSink(view)
                sunkTrack = track
            }
        },
    )
    DisposableEffect(engine) {
        onDispose {
            runCatching { renderer?.let { sunkTrack?.removeSink(it) } }
            runCatching { renderer?.release() }
        }
    }
}
