package com.us.android.core.auth

/**
 * Persistent credential storage.
 *
 * An interface rather than a concrete class so the session machinery can be
 * unit-tested without a device: the production implementation is backed by
 * the Android Keystore, which has no JVM equivalent.
 *
 * Storage is split by sensitivity, deliberately:
 *
 *  - **Refresh token** → encrypted. It is the long-lived credential and the
 *    only one worth protecting at rest.
 *  - **Access token** → never persisted at all. It lives in memory in
 *    [SessionManager] and is re-minted by refresh on cold start.
 *  - **userId / sessionId** → plain. Not secrets, and reading them
 *    synchronously is what lets the nav graph resolve a session on the first
 *    frame instead of blocking on I/O (finding F5).
 */
interface TokenStore {
    var userId: String?
    var sessionId: String?

    /** Epoch millis at which the current access token expires. 0 if unknown. */
    var accessTokenExpiresAtMillis: Long

    /** Cheap presence check that does not decrypt. */
    fun hasRefreshToken(): Boolean

    fun readRefreshToken(): String?
    fun writeRefreshToken(token: String)
    fun clear()
}
