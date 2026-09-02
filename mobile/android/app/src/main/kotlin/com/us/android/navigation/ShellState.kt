package com.us.android.navigation

import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ModulePreferences
import com.us.android.core.profile.data.ModulePrefsState

/**
 * What the shell should be showing, from the session and the module choices
 * combined. The nav graph's start destination is a function of this and
 * nothing else.
 */
sealed interface ShellState {
    /** No session: the sign-in flow. */
    data object Unauthenticated : ShellState

    /**
     * Signed in, module choices not yet known. Only the splash renders this,
     * and only until the cache or the network answers — a relaunch with a
     * cached answer leaves it within a frame.
     */
    data object Loading : ShellState

    /** Signed in, never chose modules: the first-login picker, no bar. */
    data object NeedsOnboarding : ShellState

    /** Signed in with choices: the tabs. */
    data class Ready(val prefs: ModulePreferences) : ShellState
}

/**
 * The gate. Pure, so the matrix is a table test rather than a Robolectric run.
 *
 * [ModulePrefsState.Unavailable] becomes [ShellState.Ready] with defaults on
 * purpose: an outage on the preferences endpoint must not lock a returning
 * user behind a splash, nor push them through onboarding they already did.
 */
fun deriveShellState(session: SessionState, prefs: ModulePrefsState): ShellState {
    if (session !is SessionState.Authenticated) return ShellState.Unauthenticated
    return when (prefs) {
        ModulePrefsState.Unknown -> ShellState.Loading
        is ModulePrefsState.Loaded ->
            if (prefs.prefs.onboardingCompleted) ShellState.Ready(prefs.prefs) else ShellState.NeedsOnboarding
        ModulePrefsState.Unavailable -> ShellState.Ready(ModulePreferences.DEFAULT)
    }
}
