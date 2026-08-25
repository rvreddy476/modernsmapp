package com.us.android.core.common.session

/**
 * Work that must happen while the user is still signed in, during sign-out.
 *
 * ## WHY THIS INVERSION EXISTS
 *
 * Some cleanup needs an authenticated call: unregistering this device's push
 * token, for instance, is a `DELETE` the server will reject once the session is
 * gone. That means it cannot be done by observing the session becoming
 * `Unauthenticated` — by then the token is cleared and the call fails.
 *
 * It has to run inside sign-out, before the session is torn down. The naive way
 * is for `:core:auth` to call `:core:notifications` directly, which makes the
 * authentication module depend on push, and then on whatever the next feature
 * needs to clean up.
 *
 * So the dependency is inverted: `:core:auth` depends on this interface, which
 * lives in `:core:common` (everything already depends on it), and each module
 * with sign-out work contributes an implementation via Dagger `@IntoSet`. Auth
 * knows there is cleanup to run; it never learns what.
 *
 * ## FAILURE IS NOT FATAL
 *
 * A task that throws must not prevent sign-out. A user who taps "log out" ends
 * up logged out even if the network is down — the same rule the logout call
 * itself already follows. Implementations should be idempotent, because a
 * failed teardown will not be retried.
 */
fun interface SessionTeardownTask {
    /**
     * Runs while the session is still valid.
     *
     * Must not throw for an expected failure. Anything thrown is swallowed by
     * the caller so that sign-out completes.
     */
    suspend fun onSignOut()
}
