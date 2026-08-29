package com.us.android.core.auth

import com.us.android.core.model.SessionState
import kotlinx.coroutines.flow.StateFlow

/**
 * Read-only view of the session state, for features that only need to know
 * WHO is signed in and must not see the full auth surface (login, logout,
 * token handling). Bound to [AuthRepository] in AuthModule; tests substitute
 * a plain fake.
 */
interface SessionStateProvider {
    val sessionState: StateFlow<SessionState>
}
