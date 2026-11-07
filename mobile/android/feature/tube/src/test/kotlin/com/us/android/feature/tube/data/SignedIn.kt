package com.us.android.feature.tube.data

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.model.SessionState
import kotlinx.coroutines.flow.MutableStateFlow

/** A session a test can sign in and out of; signed in as [userId] to begin with. */
class SignedIn(userId: String = "viewer-1") : SessionStateProvider {
    override val sessionState = MutableStateFlow<SessionState>(SessionState.Authenticated(userId, "session-$userId"))

    fun signInAs(userId: String) {
        sessionState.value = SessionState.Authenticated(userId, "session-$userId")
    }

    fun signOut() {
        sessionState.value = SessionState.Unauthenticated
    }
}
