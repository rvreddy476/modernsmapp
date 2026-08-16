package com.us.android.navigation

import androidx.lifecycle.ViewModel
import com.us.android.core.auth.AuthRepository
import com.us.android.core.model.SessionState
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/**
 * Exposes the session to the navigation graph.
 *
 * Deliberately thin: it holds no state of its own and does no work in init.
 * [SessionManager][com.us.android.core.auth.SessionManager] already resolves
 * the initial value synchronously, so the graph has a real answer on the first
 * frame with nothing to await.
 */
@HiltViewModel
class MainViewModel @Inject constructor(
    authRepository: AuthRepository,
) : ViewModel() {
    val sessionState: StateFlow<SessionState> = authRepository.sessionState
}
