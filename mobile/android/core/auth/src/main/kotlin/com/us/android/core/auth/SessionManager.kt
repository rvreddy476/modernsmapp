package com.us.android.core.auth

import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.RefreshRequestDto
import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.model.SessionState
import com.us.android.core.network.TokenProvider
import com.us.android.core.network.TokenRefresher
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.telemetry.Operation
import com.us.android.core.telemetry.StatusClass
import com.us.android.core.telemetry.Telemetry
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.time.Instant
import java.time.format.DateTimeParseException
import javax.inject.Inject
import javax.inject.Provider
import javax.inject.Singleton
import kotlin.concurrent.Volatile

/**
 * Owns "who is signed in" for the whole app.
 *
 * Two properties matter more than anything else here:
 *
 * 1. **The initial state is computed synchronously, without I/O beyond a
 *    SharedPreferences read.** The nav graph observes a real value on the
 *    first frame and never awaits session restore. That is what closes
 *    finding F5 — the Flutter router blocks every navigation on a 3-second
 *    `sessionReady` await ([router.dart:128]).
 *
 * 2. **Concurrent refreshes collapse into one.** A feed screen firing eight
 *    parallel requests that all 401 must produce a single `/v1/auth/refresh`,
 *    not eight. Enforced by [inFlightRefresh].
 *
 * [AuthApi] arrives as a [Provider] to break the Dagger cycle: Retrofit needs
 * the OkHttp client, whose Authenticator needs this class.
 */
@Singleton
class SessionManager @Inject constructor(
    private val tokenStore: TokenStore,
    private val authApi: Provider<AuthApi>,
    private val cookieStore: CsrfCookieStore,
    private val telemetry: Telemetry,
    @ApplicationScope private val scope: CoroutineScope,
) : TokenProvider, TokenRefresher {

    /**
     * In memory only, never persisted. On cold start it is null and the first
     * authenticated call triggers a refresh to mint a new one.
     */
    @Volatile
    private var accessToken: String? = null

    private val _state = MutableStateFlow(initialState())
    val state: StateFlow<SessionState> = _state.asStateFlow()

    private val refreshMutex = Mutex()
    private var inFlightRefresh: Deferred<String?>? = null

    /**
     * Optimistic, and deliberately so: a stored refresh token plus a stored
     * user id means "there was a session", so the app opens signed in and
     * demotes to [SessionState.Unauthenticated] only if refresh actually
     * fails. The alternative — showing a login screen and then bouncing into
     * the app a second later — is worse for every user whose token is valid,
     * which is nearly all of them.
     */
    private fun initialState(): SessionState {
        val userId = tokenStore.userId
        return if (userId != null && tokenStore.hasRefreshToken()) {
            SessionState.Authenticated(
                userId = userId,
                sessionId = tokenStore.sessionId.orEmpty(),
            )
        } else {
            SessionState.Unauthenticated
        }
    }

    override fun currentAccessToken(): String? = accessToken

    // ── Refresh ─────────────────────────────────────────────────────────

    override suspend fun refresh(): String? {
        val deferred = refreshMutex.withLock {
            inFlightRefresh ?: scope.async { performRefresh() }.also { inFlightRefresh = it }
        }
        return try {
            deferred.await()
        } finally {
            refreshMutex.withLock {
                if (inFlightRefresh === deferred) inFlightRefresh = null
            }
        }
    }

    // Catching Throwable is the point: this is an error boundary. Any failure
    // of refresh — IO, malformed body, an unexpected runtime error — must end
    // in a cleanly cleared session rather than an exception escaping into
    // OkHttp's Authenticator. CancellationException is rethrown first so
    // structured concurrency still works.
    @Suppress("TooGenericExceptionCaught", "SwallowedException")
    private suspend fun performRefresh(): String? {
        val refreshToken = tokenStore.readRefreshToken() ?: run {
            clearSession()
            return null
        }
        return try {
            val envelope = authApi.get().refresh(RefreshRequestDto(refreshToken))
            val data = envelope.data
            if (data == null || data.tokens.accessToken.isBlank()) {
                clearSession()
                null
            } else {
                adoptTokens(data)
                data.tokens.accessToken
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Throwable) {
            // A failed refresh is terminal: the stored credential no longer
            // works, so drop it and let the nav graph react to the state
            // change. The network layer never navigates.
            //
            // The cause is not rethrown — an exception escaping into OkHttp's
            // Authenticator would surface as an opaque crash on whichever
            // request happened to 401 first. It is REPORTED instead, so a
            // silent logout is visible in telemetry rather than being
            // something users complain about and nobody can reproduce.
            telemetry.recordError(
                event = "auth.refresh.failed",
                cause = e,
                attributes = mapOf("session.cleared" to "true"),
            )
            telemetry.recordOperation(
                Operation.AuthRefresh,
                StatusClass.ServerError,
                0,
            )
            clearSession()
            null
        }
    }

    /**
     * True when the access token is past 80% of its lifetime.
     *
     * Proactive refresh on top of the reactive 401 path, because
     * `expires_at` is an absolute RFC3339 instant (not `expires_in`) and a
     * device clock that is behind would otherwise keep presenting a token the
     * server has already retired.
     */
    fun shouldProactivelyRefresh(nowMillis: Long = System.currentTimeMillis()): Boolean {
        if (accessToken == null) return tokenStore.hasRefreshToken()
        val expiresAt = tokenStore.accessTokenExpiresAtMillis
        if (expiresAt <= 0L) return false
        val issuedAt = expiresAt - ASSUMED_LIFETIME_MILLIS
        val elapsed = nowMillis - issuedAt
        return elapsed >= (ASSUMED_LIFETIME_MILLIS * PROACTIVE_REFRESH_RATIO).toLong()
    }

    // ── Session transitions ─────────────────────────────────────────────

    /**
     * Applies an [AuthResponseDto] and returns the resulting state.
     *
     * This is the single place the five server outcomes become one of five
     * [SessionState]s, and the order of the checks is the contract: the
     * pending cases are inspected BEFORE tokens, because `requires_2fa` and
     * `requires_step_up` come back as HTTP 200 with an empty token pair.
     * Treating 200 as "signed in" is the trap this method exists to avoid.
     */
    fun applyAuthResponse(dto: AuthResponseDto): SessionState {
        val next = when {
            dto.requires2fa -> SessionState.PendingTwoFactor(dto.pendingToken)

            dto.requiresStepUp -> SessionState.PendingStepUp(dto.stepUpMethods)

            dto.requiresVerification -> SessionState.PendingVerification(
                token = dto.verificationToken,
                expiresAtEpochSeconds = dto.verificationExpiresAt.toEpochSecondsOrZero(),
            )

            dto.tokens.accessToken.isNotBlank() -> {
                adoptTokens(dto)
                SessionState.Authenticated(
                    userId = dto.user?.id.orEmpty(),
                    sessionId = dto.sessionId,
                )
            }

            else -> SessionState.Unauthenticated
        }
        _state.value = next
        return next
    }

    private fun adoptTokens(dto: AuthResponseDto) {
        accessToken = dto.tokens.accessToken
        if (dto.tokens.refreshToken.isNotBlank()) {
            tokenStore.writeRefreshToken(dto.tokens.refreshToken)
        }
        tokenStore.accessTokenExpiresAtMillis = dto.tokens.expiresAt.toEpochMillisOrZero()
        dto.user?.id?.takeIf { it.isNotBlank() }?.let { tokenStore.userId = it }
        dto.sessionId.takeIf { it.isNotBlank() }?.let { tokenStore.sessionId = it }

        val current = _state.value
        if (current !is SessionState.Authenticated) {
            val userId = dto.user?.id ?: tokenStore.userId.orEmpty()
            _state.value = SessionState.Authenticated(userId, dto.sessionId)
        }
    }

    /** Called on explicit sign-out and whenever a refresh proves unrecoverable. */
    fun clearSession() {
        accessToken = null
        tokenStore.clear()
        cookieStore.clear()
        _state.value = SessionState.Unauthenticated
    }

    /** Test seam — lets a test assert what the interceptor will attach. */
    internal fun setAccessTokenForTest(token: String?) {
        accessToken = token
    }

    private companion object {
        /**
         * The server does not advertise the token lifetime, only its expiry
         * instant, so proactive refresh assumes a 15-minute window. Worst
         * case this refreshes slightly early, which is harmless; the reactive
         * 401 path is the real guarantee.
         */
        const val ASSUMED_LIFETIME_MILLIS = 15 * 60 * 1000L
        const val PROACTIVE_REFRESH_RATIO = 0.8
    }
}

private fun String?.toEpochMillisOrZero(): Long =
    this?.let { runCatching { Instant.parse(it).toEpochMilli() }.getOrNull() } ?: 0L

private fun String?.toEpochSecondsOrZero(): Long = this?.let {
    try {
        Instant.parse(it).epochSecond
    } catch (_: DateTimeParseException) {
        0L
    }
} ?: 0L
