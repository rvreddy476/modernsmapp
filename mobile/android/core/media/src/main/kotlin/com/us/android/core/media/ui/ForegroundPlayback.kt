package com.us.android.core.media.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.rememberUpdatedState
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner

/**
 * Playback that runs only while its screen is in front (2026-11-07): [pause]
 * on `ON_STOP`, [resume] on `ON_START`.
 *
 * An OBSERVER, not state read in composition, on purpose. A stopped screen
 * draws no frames, so composition does not run: a pause driven by a
 * recomposition never happens (the chat viewer and the reel studio kept
 * playing, with sound, behind Home), and a resume keyed on screen state
 * never re-runs, because nothing on screen changed (Reels came back
 * frozen). Both must be told by the lifecycle itself.
 *
 * The observer also hears `ON_START` when it is first added to a started
 * screen, so [resume] must be safe before the surface's player exists.
 */
@Composable
fun ForegroundPlayback(pause: () -> Unit = {}, resume: () -> Unit = {}) {
    val owner = LocalLifecycleOwner.current
    val latestPause by rememberUpdatedState(pause)
    val latestResume by rememberUpdatedState(resume)
    DisposableEffect(owner) {
        val observer = foregroundPlaybackObserver(pause = { latestPause() }, resume = { latestResume() })
        owner.lifecycle.addObserver(observer)
        onDispose { owner.lifecycle.removeObserver(observer) }
    }
}

/** The observer [ForegroundPlayback] adds; apart so the mapping is a JVM test. */
fun foregroundPlaybackObserver(pause: () -> Unit, resume: () -> Unit): LifecycleEventObserver =
    LifecycleEventObserver { _, event ->
        when (event) {
            Lifecycle.Event.ON_STOP -> pause()
            Lifecycle.Event.ON_START -> resume()
            else -> Unit
        }
    }
