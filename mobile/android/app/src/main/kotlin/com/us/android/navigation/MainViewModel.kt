package com.us.android.navigation

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.model.SessionState
import com.us.android.push.PushDestination
import com.us.android.push.PushDestinations
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Exposes the session to the navigation graph, and binds per-viewer caches to it.
 *
 * Deliberately thin: it holds no state of its own and does no work in init.
 * [SessionManager][com.us.android.core.auth.SessionManager] already resolves
 * the initial value synchronously, so the graph has a real answer on the first
 * frame with nothing to await.
 */
@HiltViewModel
class MainViewModel @Inject constructor(
    authRepository: AuthRepository,
    engagementStore: EngagementStore,
    private val pushDestinations: PushDestinations,
    callSessionManager: com.us.android.core.call.CallSessionManager,
) : ViewModel() {
    val sessionState: StateFlow<SessionState> = authRepository.sessionState

    /** The un-navigated notification tap, if any. Consumed by the nav host. */
    val pushDestination: StateFlow<PushDestination?> = pushDestinations.pending

    /** The device's one call — the nav host fronts the surface on Incoming. */
    val callState: StateFlow<com.us.android.core.call.CallState> = callSessionManager.state

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
            authRepository.sessionState.collect { state ->
                engagementStore.setViewer((state as? SessionState.Authenticated)?.userId)
            }
        }
    }
}
