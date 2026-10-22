package com.us.android.core.designsystem.theme

import android.provider.Settings
import androidx.compose.animation.core.AnimationSpec
import androidx.compose.animation.core.FiniteAnimationSpec
import androidx.compose.animation.core.snap
import androidx.compose.runtime.Composable
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.platform.LocalContext

/**
 * The system's "remove animations" setting (Settings › Accessibility, or
 * Developer options' animator duration scale at 0), read ONCE at the theme
 * root and handed to every screen through [UsTheme.reducedMotion] (UI audit
 * 2026-10-21: only the end drawer and the Pulse deck honoured it; the reels
 * chrome, the press springs and the Tube cards ignored it).
 *
 * What it means for a screen: a thing that would slide, fade, spring or
 * spin instead appears where it ends up. Movement the user makes with a
 * finger (a drag, a swipe) still follows the finger: that is theirs, not
 * an animation. The helpers below are the two spellings most screens need.
 */
val LocalUsReducedMotion = staticCompositionLocalOf { false }

/** True when the device asks for no animations. Read from the system once per context. */
@Composable
fun rememberReducedMotion(): Boolean {
    val context = LocalContext.current
    return remember(context) {
        Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f) == 0f
    }
}

/** [spec] under normal motion; an instant [snap] when the device asks for none. */
@Composable
@ReadOnlyComposable
fun <T> AnimationSpec<T>.orSnap(): AnimationSpec<T> = if (LocalUsReducedMotion.current) snap() else this

/** [FiniteAnimationSpec] flavour of [orSnap], for enter/exit transitions. */
@Composable
@ReadOnlyComposable
fun <T> FiniteAnimationSpec<T>.orSnapFinite(): FiniteAnimationSpec<T> = if (LocalUsReducedMotion.current) snap() else this
