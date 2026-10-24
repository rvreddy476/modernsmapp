package com.us.android.feature.chat.ui.hidden

import android.app.Activity
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.hidden.HiddenChats
import com.us.android.core.chat.hidden.HiddenTokenSnapshot
import com.us.android.core.chat.hidden.withChatOpen
import com.us.android.core.common.window.SecureWindow
import com.us.android.core.designsystem.theme.UsTheme
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/**
 * What a chat surface needs to know about the hidden chat it shows: whether
 * it is one, how its media URLs carry the open token, and how to say "the
 * person is still here" to the 1-minute idle re-lock. The default is a
 * normal chat: URLs untouched, nothing to touch.
 */
class HiddenChatContext(
    val hidden: Boolean,
    private val openToken: () -> String?,
    val touch: () -> Unit,
) {
    /** The one switch for a hidden chat's media (see [withChatOpen]). */
    fun mediaUrl(url: String): String = if (hidden) withChatOpen(url, openToken()) else url

    companion object {
        val Normal = HiddenChatContext(hidden = false, openToken = { null }, touch = {})
    }
}

val LocalHiddenChat = staticCompositionLocalOf { HiddenChatContext.Normal }

@HiltViewModel
class HiddenGuardViewModel @Inject constructor(
    private val hidden: HiddenChats,
) : ViewModel() {
    val snapshot: StateFlow<HiddenTokenSnapshot> = hidden.tokens.snapshot
    val lockedIds: StateFlow<Set<String>> = hidden.tokens.lockedIds

    fun touch() = hidden.touch()

    fun openToken(): String? = hidden.tokens.open()
}

/** What the guard does for one surface. */
internal enum class GuardVerdict { NotHidden, Show, Relock }

/**
 * The rule, pure: the hidden list needs the list revealed; a hidden chat needs
 * the chats opened; anything else is not the guard's business.
 */
internal fun guardVerdict(
    conversationId: String?,
    requireReveal: Boolean,
    lockedIds: Set<String>,
    snapshot: HiddenTokenSnapshot,
): GuardVerdict {
    val hiddenChat = !conversationId.isNullOrBlank() && conversationId in lockedIds
    return when {
        requireReveal -> if (snapshot.revealed) GuardVerdict.Show else GuardVerdict.Relock
        hiddenChat -> if (snapshot.opened) GuardVerdict.Show else GuardVerdict.Relock
        else -> GuardVerdict.NotHidden
    }
}

/**
 * Wraps the hidden list ([requireReveal]) or any chat surface about
 * [conversationId]. For a hidden one:
 *  - the window carries FLAG_SECURE (no screenshots, no recents snapshot);
 *  - the content composes ONLY while its token is held — the moment the app
 *    re-locks (background, a minute idle, the server refusing the token) it
 *    is gone from the UI tree and [onRelocked] leaves the screen;
 *  - every touch restarts the idle clock.
 * A normal chat passes straight through.
 */
@Composable
fun HiddenChatGuard(
    conversationId: String?,
    onRelocked: () -> Unit,
    requireReveal: Boolean = false,
    viewModel: HiddenGuardViewModel = hiltViewModel(),
    content: @Composable () -> Unit,
) {
    val snapshot by viewModel.snapshot.collectAsStateWithLifecycle()
    val lockedIds by viewModel.lockedIds.collectAsStateWithLifecycle()
    val verdict = guardVerdict(conversationId, requireReveal, lockedIds, snapshot)
    if (verdict == GuardVerdict.NotHidden) {
        content()
        return
    }
    val context = LocalContext.current
    DisposableEffect(context) {
        // Through the shared count, like the chat lock: a surface leaving as
        // another arrives never clears what the other still holds.
        val hold = (context as? Activity)?.window?.let(SecureWindow::hold)
        onDispose { hold?.release() }
    }
    val leave by rememberUpdatedState(onRelocked)
    LaunchedEffect(verdict) { if (verdict == GuardVerdict.Relock) leave() }
    if (verdict != GuardVerdict.Show) {
        // Nothing of the chat composes without its token.
        Box(Modifier.fillMaxSize().background(UsTheme.extended.bgCanvas).testTag("hidden-relocked"))
        return
    }
    val chat = remember(viewModel, conversationId) {
        HiddenChatContext(hidden = !requireReveal, openToken = viewModel::openToken, touch = viewModel::touch)
    }
    Box(
        modifier = Modifier
            .fillMaxSize()
            .pointerInput(Unit) {
                awaitPointerEventScope {
                    while (true) {
                        // Observed on the way down, never consumed.
                        awaitPointerEvent(PointerEventPass.Initial)
                        viewModel.touch()
                    }
                }
            },
    ) {
        CompositionLocalProvider(LocalHiddenChat provides chat) { content() }
    }
}
