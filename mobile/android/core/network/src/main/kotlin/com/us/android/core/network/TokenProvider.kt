package com.us.android.core.network

/**
 * Dependency inversion between `:core:network` and `:core:auth`.
 *
 * The network layer needs the current access token and a way to refresh it,
 * but `:core:auth` depends on `:core:network` (it uses Retrofit). Rather than
 * invert that — or create a cycle — the network layer declares what it needs
 * and `:core:auth` binds the implementations.
 */
interface TokenProvider {
    /** The in-memory access token, or null when there is no session. */
    fun currentAccessToken(): String?
}

interface TokenRefresher {
    /**
     * Attempts a single refresh against `POST /v1/auth/refresh`.
     *
     * Returns the new access token, or null if refresh failed. Implementations
     * MUST be safe to call concurrently and MUST collapse concurrent callers
     * into one network round trip — a feed screen firing eight parallel
     * requests that all 401 must produce one refresh, not eight.
     */
    suspend fun refresh(): String?
}
