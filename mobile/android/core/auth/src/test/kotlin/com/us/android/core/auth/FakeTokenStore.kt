package com.us.android.core.auth

/** In-memory [TokenStore]; the production one needs the Android Keystore. */
class FakeTokenStore(
    override var userId: String? = null,
    override var sessionId: String? = null,
    override var accessTokenExpiresAtMillis: Long = 0L,
    private var refreshToken: String? = null,
) : TokenStore {

    var clearCount: Int = 0
        private set

    override fun hasRefreshToken(): Boolean = refreshToken != null

    override fun readRefreshToken(): String? = refreshToken

    override fun writeRefreshToken(token: String) {
        refreshToken = token
    }

    override fun clear() {
        clearCount++
        userId = null
        sessionId = null
        accessTokenExpiresAtMillis = 0L
        refreshToken = null
    }
}
