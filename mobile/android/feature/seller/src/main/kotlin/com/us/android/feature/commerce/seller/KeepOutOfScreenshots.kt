package com.us.android.feature.commerce.seller

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.view.Window
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.platform.LocalView
import com.us.android.core.common.window.SecureWindow

/**
 * Keeps the window out of screenshots, screen recordings and the recents
 * snapshot while the calling screen is shown (audit L4).
 *
 * The payout screen shows a bank account number and IFSC; the document screen
 * an identity document's type and number. Both used to be capturable by any
 * screen recorder and left in the app switcher's thumbnail.
 *
 * Through [SecureWindow]'s shared count, so a screen leaving during a
 * navigation cannot clear the flag another protected screen still holds.
 */
@Composable
internal fun KeepOutOfScreenshots() {
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
