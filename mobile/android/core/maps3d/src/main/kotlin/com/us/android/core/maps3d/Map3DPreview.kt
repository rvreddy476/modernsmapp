package com.us.android.core.maps3d

import android.content.pm.PackageManager
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.google.android.gms.maps3d.Map3DInitConfig
import com.google.android.gms.maps3d.Map3DView
import com.google.android.gms.maps3d.GoogleMap3D
import com.google.android.gms.maps3d.OnMap3DViewReadyCallback
import com.google.android.gms.maps3d.model.Map3DMode

// The experimental SDK supports one active view. Never share a private marker set.
private var activePreview = false

@Composable
fun maps3DConfigured(): Boolean {
    val context = LocalContext.current
    return remember(context) {
        @Suppress("DEPRECATION")
        val info = context.packageManager.getApplicationInfo(context.packageName, PackageManager.GET_META_DATA)
        !info.metaData?.getString("com.google.android.geo.maps3d.API_KEY").isNullOrBlank()
    }
}

/** Optional scenery preview, not navigation, fare calculation or live tracking. */
@Composable
fun Map3DPreview(latitude: Double, longitude: Double, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    var failure by remember { mutableStateOf<String?>(null) }
    if (!maps3DConfigured() || !latitude.isFinite() || !longitude.isFinite() || latitude !in -90.0..90.0 || longitude !in -180.0..180.0) {
        Text("3D preview is unavailable. Use the standard map.", modifier)
        return
    }
    val view = remember {
        if (activePreview) null else {
            activePreview = true
            runCatching { Map3DView(context, Map3DInitConfig.create(mapMode = Map3DMode.HYBRID, centerLat = latitude, centerLng = longitude)).apply {
                onCreate(null)
                getMap3DViewAsync(object : OnMap3DViewReadyCallback {
                    override fun onMap3DViewReady(googleMap3D: GoogleMap3D) = Unit
                    override fun onError(error: Exception) { this@apply.post { failure = "3D preview could not load. Switch to the standard map." } }
                })
            } }.getOrElse { activePreview = false; null }
        }
    }
    if (view == null) { Text("Another 3D preview is open. Use the standard map.", modifier); return }
    DisposableEffect(view, lifecycle) {
        // Adding the observer replays the current state; don't dispatch it twice.
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> view.onStart()
                Lifecycle.Event.ON_RESUME -> view.onResume()
                Lifecycle.Event.ON_PAUSE -> view.onPause()
                Lifecycle.Event.ON_STOP -> view.onStop()
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        onDispose {
            lifecycle.removeObserver(observer)
            if (lifecycle.currentState.isAtLeast(Lifecycle.State.RESUMED)) view.onPause()
            if (lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) view.onStop()
            view.onDestroy()
            activePreview = false
        }
    }
    if (failure != null) Text(failure!!, modifier)
    else AndroidView(factory = { view }, modifier = modifier)
}
