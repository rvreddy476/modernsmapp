package com.us.android.core.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.runtime.staticCompositionLocalOf

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
