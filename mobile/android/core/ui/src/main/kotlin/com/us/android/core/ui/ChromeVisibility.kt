package com.us.android.core.ui

import androidx.activity.compose.LocalActivity
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow

/**
 * What a screen asks of the app shell's chrome — today, only whether the
 * bottom navigation bar should be out of the way.
 *
 * The shell (`:app`) decides bar visibility from the ROUTE: a tab root shows
 * it, a pushed screen does not. A screen that wants the bar gone while it is
 * a tab root — Reels' full mode, where a double-tap leaves nothing but the
 * video — cannot reach the shell directly (features do not depend on the
 * app module), so it writes here and the shell reads. This lives in `:core:ui`
 * because every feature and the shell can see it, and it holds no logic
 * beyond a flag: the shell still owns the decision, this is the request.
 *
 * The flag is a request scoped to the composition that made it: [HideShellBottomBar]
 * clears it when the caller leaves, so the bar can never stay hidden under a
 * screen that did not ask for that.
 */
@Stable
class ChromeVisibility {
    /** True while some screen has asked the shell to hide its bottom bar. */
    var bottomBarHidden: Boolean by mutableStateOf(false)
        private set

    fun hideBottomBar(hidden: Boolean) {
        bottomBarHidden = hidden
    }

    private val _reselected = MutableSharedFlow<Unit>(extraBufferCapacity = 1)

    /**
     * The bar item of the tab ALREADY on screen was tapped again (2026-10-18:
     * on Home that is "refresh and go to the top"). An event, not state: no
     * replay, so a screen composed later never sees an old tap; the shell
     * sends it only while that tab is the current one, so whoever collects
     * it is that tab.
     */
    val reselected: SharedFlow<Unit> = _reselected.asSharedFlow()

    fun onTabReselected() {
        _reselected.tryEmit(Unit)
    }
}

/**
 * The shell's chrome state, provided by the app's nav host. Null outside a
 * shell — previews, tests, a screen hosted on its own — where there is no
 * bar to hide and the request is a no-op.
 */
val LocalChromeVisibility = staticCompositionLocalOf<ChromeVisibility?> { null }

/**
 * Asks the shell to hide its bottom bar while [hidden] is true, and takes
 * the request back the moment the calling composition leaves — a tab switch,
 * a pushed screen, Back — so the bar returns whenever the user leaves the
 * screen that hid it, whatever state that screen was left in.
 */
@Composable
fun HideShellBottomBar(hidden: Boolean) {
    val chrome = LocalChromeVisibility.current ?: return
    DisposableEffect(chrome, hidden) {
        chrome.hideBottomBar(hidden)
        onDispose { chrome.hideBottomBar(false) }
    }
}

/**
 * Keeps the STATUS bar's glyphs light while the calling screen is shown, and
 * puts them back the way they were when it leaves (2026-10-02).
 *
 * The app follows the device's light / dark setting, so on a light device
 * the status bar draws dark glyphs. A screen that is a video stage whatever
 * the theme (Reels, where the frame runs under the status bar) would have
 * those dark glyphs over a dark frame, unreadable; it asks for light ones
 * here. Outside an Activity (a preview, a test) it does nothing.
 */
@Composable
fun LightStatusBarGlyphs() = HoldLightGlyphs(navigation = false)

/**
 * [LightStatusBarGlyphs] for a screen that is a stage from edge to edge
 * (2026-10-02): the photo studio, a call, a live room. Their dark ground runs
 * under the NAVIGATION bar too, so on a light device the three-button bar's
 * dark glyphs would sit on it unreadable. Both bars are put back the way they
 * were when the screen leaves.
 */
@Composable
fun LightSystemBarGlyphs() = HoldLightGlyphs(navigation = true)

@Composable
private fun HoldLightGlyphs(navigation: Boolean) {
    val activity = LocalActivity.current ?: return
    val view = LocalView.current
    DisposableEffect(activity, view) {
        val controller = WindowCompat.getInsetsController(activity.window, view)
        val ledgers = glyphLedgers.getOrPut(activity.window) { GlyphLedgers() }
        controller.isAppearanceLightStatusBars = ledgers.status.acquire(controller.isAppearanceLightStatusBars)
        if (navigation) {
            controller.isAppearanceLightNavigationBars =
                ledgers.navigation.acquire(controller.isAppearanceLightNavigationBars)
        }
        onDispose {
            ledgers.status.release()?.let { controller.isAppearanceLightStatusBars = it }
            if (navigation) ledgers.navigation.release()?.let { controller.isAppearanceLightNavigationBars = it }
        }
    }
}

private class GlyphLedgers {
    val status = LightGlyphLedger()
    val navigation = LightGlyphLedger()
}

/** One ledger pair per window; weak so a destroyed activity's window is not kept. */
private val glyphLedgers = java.util.WeakHashMap<android.view.Window, GlyphLedgers>()

/**
 * Who is holding a bar's glyphs light, and what to put back when nobody is
 * (2026-10-19). Pure, so the overlap is a test.
 *
 * Each screen used to save the bar's appearance when it appeared and restore
 * it when it left. Navigation overlaps the two: the camera handed to the photo
 * studio, the studio's hold began while the camera's was still on and saved
 * "light glyphs" as the original; the camera then restored the real original,
 * and when the studio's dark editor gave way to its light details step, it
 * put LIGHT glyphs back — a white clock on a white screen. Counting the holds
 * keeps the first holder's original until the last holder leaves.
 */
class LightGlyphLedger {
    private var holds = 0
    private var original = false

    /** Starts a hold on a bar currently drawn with [current]; returns what to draw now. */
    fun acquire(current: Boolean): Boolean {
        if (holds == 0) original = current
        holds++
        return false
    }

    /** Ends a hold; returns the appearance to put back once the last hold ends, else null. */
    fun release(): Boolean? {
        if (holds == 0) return null
        holds--
        return if (holds == 0) original else null
    }
}
