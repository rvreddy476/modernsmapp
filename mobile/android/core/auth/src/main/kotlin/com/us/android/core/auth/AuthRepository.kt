package com.us.android.core.auth

import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.ForgotPasswordRequestDto
import com.us.android.core.auth.dto.LoginRequestDto
import com.us.android.core.auth.dto.OAuthCompleteSignupRequestDto
import com.us.android.core.auth.dto.OAuthPendingSignupDto
import com.us.android.core.auth.dto.OAuthTokenRequestDto
import com.us.android.core.auth.dto.OAuthVerifySignupRequestDto
import com.us.android.core.auth.dto.PasswordRequestDto
import com.us.android.core.auth.dto.RegisterRequestDto
import com.us.android.core.auth.dto.ResendVerificationRequestDto
import com.us.android.core.auth.dto.ResetPasswordRequestDto
import com.us.android.core.auth.dto.StepUpVerifyRequestDto
import com.us.android.core.auth.dto.Verify2FARequestDto
import com.us.android.core.auth.dto.VerifyEmailRequestDto
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.common.session.SessionTeardownTask
import com.us.android.core.model.SessionState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.json.Json
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class AuthRepository @Inject constructor(
    private val authApi: AuthApi,
    private val sessionManager: SessionManager,
    private val errorMapper: ErrorMapper,
    /**
     * Cleanup that must run while the session is still valid — Slice D.
     *
     * A multibound set rather than a named dependency: this module must not
     * learn what needs tearing down. See [SessionTeardownTask].
     */
    private val teardownTasks: Set<@JvmSuppressWildcards SessionTeardownTask>,
    /**
     * The shared `Json` from NetworkModule, for the one endpoint whose two
     * success bodies share no type — see [signInWithOAuth].
     */
    private val json: Json,
) : SessionStateProvider {

    override val sessionState: StateFlow<SessionState> = sessionManager.state

    suspend fun requestPartnerPhone(body: com.us.android.core.auth.dto.PartnerPhoneRequestDto) =
        apiCall(errorMapper) { authApi.requestPartnerPhone(body) }

    suspend fun verifyPartnerPhone(challenge: String, code: String, deviceId: String): AppResult<SessionState> {
        return when (val result = apiCall(errorMapper) {
            authApi.verifyPartnerPhone(com.us.android.core.auth.dto.PartnerPhoneVerifyDto(challenge, code, deviceId))
        }) {
            is AppResult.Success -> AppResult.Success(sessionManager.applyAuthResponse(result.data))
            is AppResult.Failure -> AppResult.Failure(result.error)
        }
    }

    /**
     * Signs in, returning the resulting [SessionState] rather than a boolean.
     *
     * A successful HTTP 200 can still mean "not signed in" — see
     * [SessionManager.applyAuthResponse]. Returning the state forces every
     * caller to handle the pending outcomes instead of assuming success.
     */
    suspend fun login(identifier: String, password: String): AppResult<SessionState> {
        val result = apiCall(errorMapper) {
            authApi.login(LoginRequestDto(identifier = identifier, password = password))
        }
        return when (result) {
            is AppResult.Success -> AppResult.Success(
                sessionManager.applyAuthResponse(result.data),
            )
            is AppResult.Failure -> recoverPendingVerification(result.error)
        }
    }

    /**
     * Turns the one non-terminal 403 into a usable state.
     *
     * `EMAIL_NOT_VERIFIED` arrives as an error, but its `details` carry a
     * fresh `verification_token` that authorises finishing signup for this
     * one account. The password already matched, so only the account owner
     * can reach here. Dropping this response — as any client that discards
     * error bodies would — strands the user with no way to verify and no way
     * to resend.
     */
    private fun recoverPendingVerification(error: AppError): AppResult<SessionState> {
        if (error !is AppError.Forbidden) return AppResult.Failure(error)
        if (error.code != ErrorMapper.CODE_EMAIL_NOT_VERIFIED) return AppResult.Failure(error)

        val token = error.details[VERIFICATION_TOKEN_KEY].orEmpty()
        return AppResult.Success(
            SessionState.PendingVerification(token = token, expiresAtEpochSeconds = 0L),
        )
    }

    /**
     * Creates an account.
     *
     * [firstName] and [lastName] go to the server as separate fields. They are
     * NOT concatenated into a display name: the server validates and rejects
     * each independently, so joining them would lose the ability to mark the
     * offending input and would fail outright against `FIRST_NAME_REQUIRED` /
     * `LAST_NAME_REQUIRED`.
     *
     * On success this returns [SessionState.PendingVerification], never
     * [SessionState.Authenticated] — registration issues no session. The
     * account is PENDING until the emailed code is verified.
     */
    @Suppress("LongParameterList")
    suspend fun register(
        email: String,
        password: String,
        firstName: String,
        lastName: String,
        dateOfBirth: String,
        acceptedTerms: Boolean,
        gender: String? = null,
    ): AppResult<SessionState> {
        // One key per submit attempt, generated HERE rather than per HTTP
        // call, so a transport-level retry of this submit reuses it and the
        // server replays instead of creating a second account. A fresh tap of
        // "Create account" is a new intent and gets a new key.
        val idempotencyKey = UUID.randomUUID().toString()

        val result = apiCall(errorMapper) {
            authApi.register(
                idempotencyKey = idempotencyKey,
                body = RegisterRequestDto(
                    email = email.trim(),
                    password = password,
                    firstName = firstName.trim(),
                    lastName = lastName.trim(),
                    dob = dateOfBirth.trim(),
                    acceptedTerms = acceptedTerms,
                    termsVersion = RegistrationRules.TERMS_VERSION,
                    gender = gender,
                ),
            )
        }
        return when (result) {
            is AppResult.Success -> AppResult.Success(
                sessionManager.applyAuthResponse(result.data),
            )
            is AppResult.Failure -> AppResult.Failure(result.error)
        }
    }

    /**
     * Confirms the emailed code.
     *
     * Returns [Unit], not a session, because the server issues none: a
     * verified account still has to sign in. Callers must route to login on
     * success, never into the app.
     */
    suspend fun verifyEmail(verificationToken: String, code: String): AppResult<Unit> =
        apiCall(errorMapper) {
            authApi.verifyEmail(
                VerifyEmailRequestDto(
                    verificationToken = verificationToken,
                    code = code.trim(),
                ),
            )
        }.map { }

    suspend fun resendVerification(verificationToken: String): AppResult<Unit> =
        apiCall(errorMapper) {
            authApi.resendVerification(
                ResendVerificationRequestDto(verificationToken = verificationToken),
            )
        }.map { }

    // ── Sign-in security (web parity, founder 21 Oct 2026) ──────────────

    /**
     * Second step of a sign-in that came back [SessionState.PendingTwoFactor].
     *
     * [code] is a TOTP or a recovery code; the server tries both. Success is
     * applied through [SessionManager.applyAuthResponse] like a password
     * sign-in, so the shell flips to signed-in from the session state alone.
     */
    suspend fun verifyTwoFactor(
        userId: String,
        pendingToken: String,
        code: String,
    ): AppResult<SessionState> = apiCall(errorMapper) {
        authApi.verifyTwoFactor(
            Verify2FARequestDto(userId = userId, code = code.trim(), pendingToken = pendingToken),
        )
    }.map(sessionManager::applyAuthResponse)

    /**
     * Second step of an unusual sign-in ([SessionState.PendingStepUp]).
     * [method] is one of the server's `step_up_methods`; anything other than
     * `email_otp` goes down the TOTP route, which also accepts recovery codes.
     */
    suspend fun verifyStepUp(
        method: String,
        pendingToken: String,
        code: String,
    ): AppResult<SessionState> = apiCall(errorMapper) {
        val body = StepUpVerifyRequestDto(pendingToken = pendingToken, code = code.trim())
        if (method == STEP_UP_METHOD_EMAIL) {
            authApi.verifyStepUpEmail(body)
        } else {
            authApi.verifyStepUpTwoFactor(body)
        }
    }.map(sessionManager::applyAuthResponse)

    /** Asks for a reset code. Success says nothing about whether the account exists. */
    suspend fun forgotPassword(identifier: String): AppResult<Unit> =
        apiCall(errorMapper) {
            authApi.forgotPassword(ForgotPasswordRequestDto(identifier = identifier.trim()))
        }.map { }

    /**
     * Sets a new password with the emailed code.
     *
     * The server revokes EVERY session on success, this device's included.
     * A signed-in caller (Settings › Change password) must therefore follow
     * up with [endSessionAfterPasswordReset] — deliberately a second call,
     * so the screen can show "Password updated" before the shell flips to
     * Login (clearing the session re-roots the navigation graph, and a
     * confirmation cleared in the same breath is never seen). Nothing calls
     * the API in between, so the dead refresh token does no harm meanwhile.
     */
    suspend fun resetPassword(identifier: String, code: String, newPassword: String): AppResult<Unit> =
        apiCall(errorMapper) {
            authApi.resetPassword(
                ResetPasswordRequestDto(
                    identifier = identifier.trim(),
                    code = code.trim(),
                    newPassword = newPassword,
                ),
            )
        }.map { }

    /**
     * The local half of a password reset made while signed in: the server
     * has already revoked this session, so this is the same teardown-then-
     * clear that deactivation uses, with no logout call. Leaving the refresh
     * token in place would only produce a silent 401 on the next request and
     * an unexplained bounce to Login. A no-op when nobody is signed in.
     */
    suspend fun endSessionAfterPasswordReset() {
        if (sessionState.value.isAuthenticated) endRevokedSession()
    }

    /**
     * Signs in with a Google ACCESS token (not an ID token — see
     * [OAuthTokenRequestDto]). Three outcomes, by shape rather than by status:
     * a session (possibly `requires_2fa`, which [SessionManager] turns into
     * the pending state the two-step screen handles), or a pending signup
     * that needs a phone number first.
     */
    suspend fun signInWithOAuth(provider: String, accessToken: String): AppResult<OAuthSignInOutcome> =
        apiCall(errorMapper) { authApi.oauthToken(provider, OAuthTokenRequestDto(accessToken)) }
            .map { raw ->
                val pending = json.decodeFromJsonElement(OAuthPendingSignupDto.serializer(), raw)
                if (pending.isPendingSignup) {
                    OAuthSignInOutcome.PendingSignup(
                        pendingToken = pending.pendingToken,
                        provider = pending.provider.ifBlank { provider },
                        email = pending.email,
                        name = pending.name,
                    )
                } else {
                    val auth = json.decodeFromJsonElement(AuthResponseDto.serializer(), raw)
                    OAuthSignInOutcome.SignedIn(sessionManager.applyAuthResponse(auth))
                }
            }

    /** Attaches a phone to the pending OAuth signup; the server then sends (or fails to send) the code. */
    suspend fun completeOAuthSignup(pendingToken: String, phone: String): AppResult<Unit> =
        apiCall(errorMapper) {
            authApi.oauthCompleteSignup(OAuthCompleteSignupRequestDto(pendingToken, phone.trim()))
        }.map { }

    /** Confirms the phone code; the account is created and a session issued (201). */
    suspend fun verifyOAuthSignup(pendingToken: String, otp: String, deviceId: String): AppResult<SessionState> =
        apiCall(errorMapper) {
            authApi.oauthVerifySignup(
                OAuthVerifySignupRequestDto(pendingToken = pendingToken, otp = otp.trim(), deviceId = deviceId),
            )
        }.map(sessionManager::applyAuthResponse)

    /**
     * Signs out. The local session is cleared regardless of what the server
     * says: a user who taps "log out" must end up logged out even if the
     * device is offline or the endpoint 500s.
     */
    suspend fun logout(): AppResult<Unit> {
        // Teardown runs FIRST, while the access token is still valid — Slice D.
        //
        // Unregistering this device's push token is a `DELETE` the server
        // rejects once the session is gone, so it cannot be done by reacting to
        // the session becoming Unauthenticated. Ordering it here is the whole
        // reason SessionTeardownTask exists.
        //
        // Each task is isolated: a throwing task must not prevent sign-out. A
        // user who taps "log out" ends up logged out even if the network is
        // down, which is the same rule the logout call below already follows.
        for (task in teardownTasks) {
            try {
                task.onSignOut()
            } catch (e: CancellationException) {
                throw e
            } catch (_: Throwable) {
                // Deliberately swallowed. The device may keep receiving push
                // until the token rotates or the server prunes it — undesirable,
                // and still better than a sign-out that cannot complete.
            }
        }

        val result = try {
            apiCall(errorMapper) { authApi.logout() }
        } catch (e: CancellationException) {
            throw e
        } catch (_: Throwable) {
            AppResult.Success(Unit)
        }
        sessionManager.clearSession()
        return when (result) {
            is AppResult.Success -> AppResult.Success(Unit)
            is AppResult.Failure -> AppResult.Success(Unit)
        }
    }

    /**
     * Deactivates the account. On success the server has already revoked
     * every session, so the local one is cleared the same way sign-out clears
     * it; the nav graph then lands on Login. A failure (wrong password, state
     * conflict, offline) leaves the session intact — nothing changed.
     */
    suspend fun deactivateAccount(password: String): AppResult<Unit> {
        val result = apiCall(errorMapper) { authApi.deactivateAccount(PasswordRequestDto(password)) }
        if (result is AppResult.Success) endRevokedSession()
        return result.map { }
    }

    /** Schedules deletion; returns the purge date the server chose. Same session rule as deactivation. */
    suspend fun deleteAccount(password: String): AppResult<String> {
        val result = apiCall(errorMapper) { authApi.deleteAccount(PasswordRequestDto(password)) }
        if (result is AppResult.Success) endRevokedSession()
        return result.map { it.scheduledPurgeDate }
    }

    /**
     * The server has already revoked the session, so there is no logout call
     * to make — only the same local teardown sign-out does, best-effort, then
     * the clear. The push token DELETE may well be refused now; that is the
     * same trade sign-out makes when the network is down.
     */
    private suspend fun endRevokedSession() {
        for (task in teardownTasks) {
            try {
                task.onSignOut()
            } catch (e: CancellationException) {
                throw e
            } catch (_: Throwable) {
                // Deliberately swallowed — see logout().
            }
        }
        sessionManager.clearSession()
    }

    companion object {
        private const val VERIFICATION_TOKEN_KEY = "verification_token"

        /** The server's name for the emailed step-up channel (auth.go, `step_up_methods`). */
        const val STEP_UP_METHOD_EMAIL = "email_otp"

        /** The server's name for the authenticator-app channel. */
        const val STEP_UP_METHOD_TOTP = "totp"

        /** The only provider the Android sign-in button offers (A5: google, github, apple). */
        const val OAUTH_PROVIDER_GOOGLE = "google"
    }
}
