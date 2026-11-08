package com.us.android.feature.kitchen.ui

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.view.Window
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.platform.LocalView
import com.us.android.core.common.window.SecureWindow

/**
 * Keeps the window out of screenshots, screen recording and the recents
 * thumbnail (FLAG_SECURE) while the calling screen is shown: bank details,
 * PAN / GSTIN, licences and the selfie (food audit L4). Holds are counted by
 * [SecureWindow], so two of these overlapping during a navigation never clear
 * each other's flag.
 */
@Composable
fun SecureScreen() {
    val view = LocalView.current
    DisposableEffect(view) {
        val hold = view.context.findWindow()?.let(SecureWindow::hold)
        onDispose { hold?.release() }
    }
}

private tailrec fun Context.findWindow(): Window? = when (this) {
    is Activity -> window
    is ContextWrapper -> baseContext.findWindow()
    else -> null
}
