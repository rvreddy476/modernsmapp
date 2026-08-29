package com.us.android.core.auth

import com.us.android.core.auth.dto.LoginRequestDto
import com.us.android.core.auth.dto.RegisterRequestDto
import com.us.android.core.auth.dto.ResendVerificationRequestDto
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
) : SessionStateProvider {

    override val sessionState: StateFlow<SessionState> = sessionManager.state

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

    private companion object {
        const val VERIFICATION_TOKEN_KEY = "verification_token"
    }
}
