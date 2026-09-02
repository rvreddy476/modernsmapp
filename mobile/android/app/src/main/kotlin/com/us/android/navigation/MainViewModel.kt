package com.us.android.navigation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.call.CallSessionManager
import com.us.android.core.call.CallState
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ModulePreferencesRepository
import com.us.android.push.PushDestination
import com.us.android.push.PushDestinations
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Exposes the shell state to the navigation graph, and binds per-viewer
 * caches to the session.
 *
 * [SessionManager][com.us.android.core.auth.SessionManager] resolves the
 * initial session synchronously, so [shellState] has a real answer on the
 * first frame: signed out, or signed in and Loading until the module
 * preferences cache answers. Nothing here is awaited.
 *
 * This is also where the session drives the module preferences — refreshed
 * whenever a session becomes Authenticated, cleared on sign-out — because
 * `:core:auth` must not depend on `:core:profile` and the repository must
 * not depend on the session. The shell is the one place that sees both.
 */
@HiltViewModel
class MainViewModel @Inject constructor(
    sessionStateProvider: SessionStateProvider,
    engagementStore: EngagementStore,
    private val pushDestinations: PushDestinations,
    callSessionManager: CallSessionManager,
    modulePreferences: ModulePreferencesRepository,
) : ViewModel() {
    val sessionState: StateFlow<SessionState> = sessionStateProvider.sessionState

    val shellState: StateFlow<ShellState> = combine(sessionState, modulePreferences.state, ::deriveShellState)
        .stateIn(
            viewModelScope,
            SharingStarted.Eagerly,
            deriveShellState(sessionState.value, modulePreferences.state.value),
        )

    /** The un-navigated notification tap, if any. Consumed by the nav host. */
    val pushDestination: StateFlow<PushDestination?> = pushDestinations.pending

    /** The device's one call — the nav host fronts the surface on Incoming. */
    val callState: StateFlow<CallState> = callSessionManager.state

    fun consumePushDestination() = pushDestinations.consume()

    init {
        // Bind the shared engagement overlay to whoever is signed in.
        //
        // The store keys purely by post id, so without this a sign-out and
        // sign-in inside one process would leave the previous account's
        // private bookmarks, reactions and reposts on screen for the next
        // viewer — and worse, would use them to decide the direction of that
        // viewer's next tap. `setViewer` ignores a repeated id, so emitting
        // the same session again costs nothing.
        //
        // Everything except Authenticated collapses to null: a pending 2FA or
        // verification state is not a session, and must not keep the previous
        // viewer's overlay alive.
        viewModelScope.launch {
            sessionState.collect { state ->
                engagementStore.setViewer((state as? SessionState.Authenticated)?.userId)
            }
        }
        // Module preferences follow the session. `collectLatest` so a sign-out
        // cancels a refresh still on the wire rather than letting it land a
        // signed-out account's choices into the next viewer's state.
        viewModelScope.launch {
            sessionState.collectLatest { state ->
                when (state) {
                    is SessionState.Authenticated -> modulePreferences.refresh()
                    SessionState.Unauthenticated -> modulePreferences.clear()
                    else -> Unit
                }
            }
        }
    }
}
