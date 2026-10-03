package com.us.android.feature.dating.safety

import android.app.Activity
import android.content.Context
import android.content.ContextWrapper
import android.view.Window
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.platform.LocalView
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.window.SecureWindow
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/*
 * Screen protection (mechanic M18).
 *
 * `GET /client-config` says whether Pulse's screens that show other people —
 * the deck, a profile, picks, who sparked you, the matches list and a match —
 * keep the window out of screenshots, screen recordings and the recents
 * snapshot (FLAG_SECURE). The chat is not one of them: the chat lock decides
 * that on its own.
 *
 * The config is read once per Dating session (each time Dating is opened) and
 * a failed read means off, tried again by the next protected screen. The flag
 * is held through the shared count in `:core:common` ([SecureWindow]), so
 * nested and overlapping screens — and the chat lock's own hold — are never
 * cleared by mistake: the flag goes only when the last holder lets go, and
 * only if the count was the one that set it.
 */

/** The client config's screen-protection switch, read once per Dating session. */
@Singleton
class ScreenProtectionConfig @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
) {
    private val lock = Mutex()

    /** The visit the cached answer belongs to; -1 before any successful read. */
    private var readFor = -1
    private var on = false

    /** True when the server asks for protection. Any failure reads as off, and is asked again next time. */
    suspend fun enabled(): Boolean = lock.withLock {
        val visit = session.visit
        if (readFor == visit) return@withLock on
        when (val result = repository.clientConfig()) {
            is DatingResult.Success -> {
                on = result.value.screenProtection
                readFor = visit
                on
            }
            is DatingResult.Failure -> false
        }
    }

    /** Sign-out: nothing read for one account is kept for the next. */
    suspend fun forget() = lock.withLock {
        readFor = -1
        on = false
    }
}

@HiltViewModel
class ScreenProtectionViewModel @Inject constructor(config: ScreenProtectionConfig) : ViewModel() {

    private val _enabled = MutableStateFlow(false)
    val enabled: StateFlow<Boolean> = _enabled.asStateFlow()

    init {
        viewModelScope.launch { _enabled.value = config.enabled() }
    }
}

/**
 * Keeps this window out of screenshots while the calling screen is shown and
 * the server asks for it. Place it once in each Pulse screen that shows other
 * people; nested or repeated placements are counted, never doubled up.
 */
@Composable
internal fun ProtectThisScreen(viewModel: ScreenProtectionViewModel = hiltViewModel()) {
    val enabled by viewModel.enabled.collectAsStateWithLifecycle()
    val view = LocalView.current
    DisposableEffect(enabled, view) {
        val hold = if (enabled) view.context.findWindow()?.let { SecureWindow.hold(it) } else null
        onDispose { hold?.release() }
    }
}

private tailrec fun Context.findWindow(): Window? = when (this) {
    is Activity -> window
    is ContextWrapper -> baseContext.findWindow()
    else -> null
}
