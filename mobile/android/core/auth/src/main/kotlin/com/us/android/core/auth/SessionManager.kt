package com.us.android.core.auth

import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.LogoutRequestDto
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
import retrofit2.HttpException
import java.time.Instant
import java.time.format.DateTimeParseException
import javax.inject.Inject
import javax.inject.Provider
import javax.inject.Singleton
import kotlin.concurrent.Volatile

/**
 * Owns "who is signed in" for the whole app.
 *
 * Four properties matter more than anything else here:
 *
 * 1. **The initial state is computed synchronously, without I/O beyond a
 *    SharedPreferences read.** The nav graph observes a real value on the
 *    first frame and never awaits session restore. That is what closes
 *    finding F5 — the Flutter router blocks every navigation on a 3-second
 *    `sessionReady` await ([router.dart:128]).
 *
 * 2. **Concurrent refreshes collapse into one.** A feed screen firing eight
 *    parallel requests that all 401 must produce a single `/v1/auth/refresh`,
 *    not eight. Enforced by [inFlight]; and a 401 that arrives after a
 *    refresh already finished reuses its token instead of refreshing again
 *    ([refreshIfStale], L4).
 *
 * 3. **Refresh never runs on the client it serves** (H2). It goes through
 *    [SessionTokenApi] on the `@SessionClient`, so the calls blocked in the
 *    authenticator cannot starve it of a connection slot.
 *
 * 4. **A session is a generation** (L5). Every new session and every sign-out
 *    bumps [generation]; a refresh adopts its result, or clears the session
 *    on a rejection, only if the generation it started in is still current.
 *    A refresh in flight across a sign-out cannot sign the person back in,
 *    and an old refresh's 401 cannot wipe a newer sign-in.
 *
 * [AuthApi] arrives as a [Provider] to break the Dagger cycle: Retrofit needs
 * the OkHttp client, whose Authenticator needs this class. It is used only
 * when no [sessionApi] is given (hand-built instances in tests).
 */
@Singleton
class SessionManager @Inject constructor(
    private val tokenStore: TokenStore,
    private val authApi: Provider<AuthApi>,
    private val cookieStore: CsrfCookieStore,
    private val telemetry: Telemetry,
    @ApplicationScope private val scope: CoroutineScope,
    private val sessionApi: Provider<SessionTokenApi> = Provider { AuthApiSessionTokens(authApi.get()) },
) : TokenProvider, TokenRefresher {

    /**
     * In memory only, never persisted. On cold start it is null; the first
     * authenticated call mints one before it goes out ([shouldRefreshBeforeRequest]).
     */
    @Volatile
    private var accessToken: String? = null

    /** Guards [generation] together with every write of the session it names. */
    private val sessionLock = Any()

    @Volatile
    private var generation = 0L

    private val _state = MutableStateFlow(initialState())
    val state: StateFlow<SessionState> = _state.asStateFlow()

    private class InFlight(val generation: Long, val deferred: Deferred<String?>)

    private val refreshMutex = Mutex()
    private var inFlight: InFlight? = null

    /** When the current access token was adopted, on [elapsedClock], and how long it is good for. */
    @Volatile
    private var tokenAdoptedAtMillis = 0L

    @Volatile
    private var tokenLifetimeMillis = ASSUMED_LIFETIME_MILLIS

    /** After a transient refresh failure, requests stop refreshing up front for a while. */
    @Volatile
    private var proactiveBlockedUntilMillis = 0L

    /** Monotonic milliseconds. A test seam; production never changes it. */
    internal var elapsedClock: () -> Long = { System.nanoTime() / NANOS_PER_MILLI }

    /** Wall-clock milliseconds, for the server's absolute `expires_at`. A test seam. */
    internal var wallClock: () -> Long = System::currentTimeMillis

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

    /** Refreshes now, joining a refresh already in flight for this session. */
    override suspend fun refresh(): String? = runRefresh(onlyIfStale = false, failedToken = null)

    /**
     * Refreshes only if [failedToken] is still current (L4). When another
     * token is already current — a refresh finished while this caller waited,
     * or the request left before a cold start's first token existed — that
     * token is returned with no network call.
     */
    override suspend fun refreshIfStale(failedToken: String?): String? =
        runRefresh(onlyIfStale = true, failedToken = failedToken)

    override fun shouldRefreshBeforeRequest(): Boolean = shouldProactivelyRefresh()

    private suspend fun runRefresh(onlyIfStale: Boolean, failedToken: String?): String? {
        val flight = refreshMutex.withLock {
            val current = generation
            // Join only a refresh of THIS session. One started before a
            // sign-out or a new sign-in is someone else's result.
            inFlight?.takeIf { it.generation == current }?.let { return@withLock it }
            if (onlyIfStale) {
                val token = accessToken
                if (token != null && token != failedToken) return token
            }
            InFlight(current, scope.async { performRefresh(current) }).also { inFlight = it }
        }
        return try {
            flight.deferred.await()
        } finally {
            refreshMutex.withLock {
                if (inFlight === flight) inFlight = null
            }
        }
    }

    // Catching Throwable is the point: this is an error boundary. Any failure
    // of refresh — IO, malformed body, an unexpected runtime error — must end
    // in a cleanly cleared session rather than an exception escaping into
    // OkHttp's Authenticator. CancellationException is rethrown first so
    // structured concurrency still works.
    @Suppress("TooGenericExceptionCaught", "SwallowedException")
    private suspend fun performRefresh(startedIn: Long): String? {
        val refreshToken = tokenStore.readRefreshToken() ?: run {
            clearSessionIfStill(startedIn)
            return null
        }
        return try {
            val envelope = sessionApi.get().refresh(RefreshRequestDto(refreshToken))
            val data = envelope.data
            if (data == null || data.tokens.accessToken.isBlank()) {
                clearSessionIfStill(startedIn)
                null
            } else if (adoptIfStill(startedIn, data)) {
                proactiveBlockedUntilMillis = 0L
                data.tokens.accessToken
            } else {
                // Signed out, or signed in afresh, while this was in flight:
                // the result belongs to a session that no longer exists.
                telemetry.recordError(
                    event = "auth.refresh.discarded",
                    cause = IllegalStateException("session changed during refresh"),
                    attributes = emptyMap(),
                )
                null
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Throwable) {
            // Only a DEFINITIVE rejection of the refresh token ends the
            // session. A 429, a 5xx, or no network says nothing about the
            // credential — it is the server or the connection that is
            // unavailable — and signing the user out on those turns every
            // outage into a forced re-login. The request that triggered the
            // refresh simply fails; the next one refreshes again.
            //
            // The cause is not rethrown — an exception escaping into OkHttp's
            // Authenticator would surface as an opaque crash on whichever
            // request happened to 401 first. It is REPORTED instead, so a
            // silent logout is visible in telemetry rather than being
            // something users complain about and nobody can reproduce.
            val rejected = e.isDefinitiveRejection()
            telemetry.recordError(
                event = if (rejected) "auth.refresh.failed" else "auth.refresh.transient",
                cause = e,
                attributes = mapOf("session.cleared" to rejected.toString()),
            )
            telemetry.recordOperation(
                Operation.AuthRefresh,
                StatusClass.ServerError,
                0,
            )
            if (rejected) {
                clearSessionIfStill(startedIn)
            } else {
                proactiveBlockedUntilMillis = elapsedClock() + PROACTIVE_COOLDOWN_MILLIS
            }
            null
        }
    }

    /**
     * True when the server has actually refused the credential: 401 or 403.
     * Everything else — 429, 5xx, malformed body, IO — is transient.
     */
    private fun Throwable.isDefinitiveRejection(): Boolean =
        this is HttpException && (code() == HTTP_UNAUTHORIZED || code() == HTTP_FORBIDDEN)

    /**
     * True when the next request should not go out with the current token:
     * a session exists but no access token has been minted in this process
     * (cold start), or the token is past 80% of its lifetime.
     *
     * The lifetime is measured on a monotonic clock from the moment the token
     * was adopted, not by comparing the server's absolute `expires_at` with
     * the device clock: a device clock that is off would otherwise refresh on
     * every request, or never. After a transient refresh failure this stays
     * false for [PROACTIVE_COOLDOWN_MILLIS], so being offline does not double
     * every request; the reactive 401 path is the guarantee either way.
     */
    fun shouldProactivelyRefresh(nowMillis: Long = elapsedClock()): Boolean {
        if (nowMillis < proactiveBlockedUntilMillis) return false
        if (accessToken == null) return tokenStore.hasRefreshToken()
        val elapsed = nowMillis - tokenAdoptedAtMillis
        return elapsed >= (tokenLifetimeMillis * PROACTIVE_REFRESH_RATIO).toLong()
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
     *
     * Every call starts a new [generation]: whatever a refresh still in flight
     * brings back belongs to the session before this one.
     */
    fun applyAuthResponse(dto: AuthResponseDto): SessionState = synchronized(sessionLock) {
        generation++
        val next = when {
            // The user id travels with the token: /2fa/verify binds it
            // required and checks it against the pending session.
            dto.requires2fa -> SessionState.PendingTwoFactor(
                pendingToken = dto.pendingToken,
                userId = dto.user?.id.orEmpty(),
                method = dto.twoFactorMethod.trim(),
                emailMasked = dto.emailMasked.trim(),
            )

            dto.requiresStepUp -> SessionState.PendingStepUp(
                methods = dto.stepUpMethods,
                pendingToken = dto.pendingToken,
            )

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
        next
    }

    /** Adopts a refresh result only if the session it was started for is still the current one. */
    private fun adoptIfStill(startedIn: Long, dto: AuthResponseDto): Boolean = synchronized(sessionLock) {
        if (generation != startedIn) return false
        adoptTokens(dto)
        true
    }

    /** Caller holds [sessionLock]. */
    private fun adoptTokens(dto: AuthResponseDto) {
        accessToken = dto.tokens.accessToken
        tokenAdoptedAtMillis = elapsedClock()
        val expiresAt = dto.tokens.expiresAt.toEpochMillisOrZero()
        tokenLifetimeMillis = lifetimeOf(expiresAt)
        if (dto.tokens.refreshToken.isNotBlank()) {
            tokenStore.writeRefreshToken(dto.tokens.refreshToken)
        }
        tokenStore.accessTokenExpiresAtMillis = expiresAt
        dto.user?.id?.takeIf { it.isNotBlank() }?.let { tokenStore.userId = it }
        dto.sessionId.takeIf { it.isNotBlank() }?.let { tokenStore.sessionId = it }

        val current = _state.value
        if (current !is SessionState.Authenticated) {
            val userId = dto.user?.id ?: tokenStore.userId.orEmpty()
            _state.value = SessionState.Authenticated(userId, dto.sessionId)
        }
    }

    /**
     * The server sends only the expiry instant. Its distance from the device
     * clock bounds the lifetime from above (a short-lived dev token), but a
     * device clock that is off can make it nonsense, so it never exceeds the
     * assumed 15 minutes and never drops below a floor.
     */
    private fun lifetimeOf(expiresAtMillis: Long): Long {
        if (expiresAtMillis <= 0L) return ASSUMED_LIFETIME_MILLIS
        val remaining = expiresAtMillis - wallClock()
        return if (remaining < MIN_LIFETIME_MILLIS) {
            ASSUMED_LIFETIME_MILLIS
        } else {
            minOf(remaining, ASSUMED_LIFETIME_MILLIS)
        }
    }

    /**
     * Revokes this device's session server-side, best effort: `POST
     * /v1/auth/logout` with the refresh token in the body (L8). Called by
     * sign-out before [clearSession]. A failure is swallowed — the person is
     * signed out locally whatever the server says.
     */
    @Suppress("TooGenericExceptionCaught", "SwallowedException")
    suspend fun revokeServerSession() {
        val refreshToken = tokenStore.readRefreshToken() ?: return
        try {
            sessionApi.get().logout(LogoutRequestDto(refreshToken))
        } catch (e: CancellationException) {
            throw e
        } catch (_: Throwable) {
            // Offline or a 5xx: the token stays valid server-side until it
            // expires, which is the same trade every sign-out has offline.
        }
    }

    /** Called on explicit sign-out and whenever a refresh proves unrecoverable. */
    fun clearSession() = synchronized(sessionLock) { clearSessionLocked() }

    private fun clearSessionIfStill(startedIn: Long) = synchronized(sessionLock) {
        if (generation == startedIn) clearSessionLocked()
    }

    private fun clearSessionLocked() {
        generation++
        accessToken = null
        tokenStore.clear()
        cookieStore.clear()
        _state.value = SessionState.Unauthenticated
    }

    /** Test seam — lets a test assert what the interceptor will attach. */
    internal fun setAccessTokenForTest(token: String?) {
        accessToken = token
        tokenAdoptedAtMillis = elapsedClock()
    }

    private companion object {
        const val HTTP_UNAUTHORIZED = 401
        const val HTTP_FORBIDDEN = 403
        const val NANOS_PER_MILLI = 1_000_000L

        /**
         * The server does not advertise the token lifetime, only its expiry
         * instant, so proactive refresh assumes at most a 15-minute window
         * (the production access TTL). Worst case this refreshes slightly
         * early, which is harmless; the reactive 401 path is the guarantee.
         */
        const val ASSUMED_LIFETIME_MILLIS = 15 * 60 * 1000L
        const val MIN_LIFETIME_MILLIS = 60 * 1000L
        const val PROACTIVE_REFRESH_RATIO = 0.8
        const val PROACTIVE_COOLDOWN_MILLIS = 15 * 1000L
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
