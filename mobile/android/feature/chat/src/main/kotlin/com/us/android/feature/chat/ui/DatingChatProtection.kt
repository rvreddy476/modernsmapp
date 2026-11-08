package com.us.android.feature.chat.ui

import android.app.Activity
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.ui.platform.LocalContext
import com.us.android.core.chat.data.Conversation
import com.us.android.core.common.window.SecureWindow

/**
 * Whether an open thread keeps the window out of screenshots and the recents
 * snapshot for Pulse (dating audit M3): a Pulse match's chat always does.
 *
 * Unlike Pulse's own screens this does not read dating's screen-protection
 * switch: chat may not depend on the dating feature, and the founder's rule
 * for that switch is "a failed read means ON" — so the chat, which cannot
 * read it, is protected whatever it says.
 */
internal fun protectsThread(conversation: Conversation?): Boolean = conversation?.isDatingChat == true

/** Holds FLAG_SECURE through the shared count ([SecureWindow]) while [protect] is true. */
@Composable
internal fun DatingChatProtection(protect: Boolean) {
    if (!protect) return
    val context = LocalContext.current
    DisposableEffect(context) {
        val hold = (context as? Activity)?.window?.let(SecureWindow::hold)
        onDispose { hold?.release() }
    }
}
