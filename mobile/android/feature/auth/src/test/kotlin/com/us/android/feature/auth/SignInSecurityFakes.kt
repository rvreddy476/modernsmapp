package com.us.android.feature.auth

import com.us.android.core.auth.AuthApi
import com.us.android.core.auth.AuthRepository
import com.us.android.core.auth.SessionManager
import com.us.android.core.auth.TokenStore
import com.us.android.core.auth.dto.AccountDeactivatedDto
import com.us.android.core.auth.dto.AccountDeletionScheduledDto
import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.ForgotPasswordRequestDto
import com.us.android.core.auth.dto.LoginRequestDto
import com.us.android.core.auth.dto.MessageDto
import com.us.android.core.auth.dto.OAuthCompleteSignupRequestDto
import com.us.android.core.auth.dto.OAuthTokenRequestDto
import com.us.android.core.auth.dto.OAuthVerifySignupRequestDto
import com.us.android.core.auth.dto.PartnerPhoneChallengeDto
import com.us.android.core.auth.dto.PartnerPhoneRequestDto
import com.us.android.core.auth.dto.PartnerPhoneVerifyDto
import com.us.android.core.auth.dto.PasswordRequestDto
import com.us.android.core.auth.dto.RefreshRequestDto
import com.us.android.core.auth.dto.RegisterRequestDto
import com.us.android.core.auth.dto.ResendVerificationRequestDto
import com.us.android.core.auth.dto.ResetPasswordRequestDto
import com.us.android.core.auth.dto.StepUpVerifyRequestDto
import com.us.android.core.auth.dto.Verify2FARequestDto
import com.us.android.core.auth.dto.VerifyEmailRequestDto
import com.us.android.core.auth.oauth.GoogleSignIn
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.telemetry.NoOpTelemetry
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.HttpException
import retrofit2.Response
import javax.inject.Provider

/**
 * The sign-in security ViewModels run against the REAL `AuthRepository`,
 * `SessionManager` and `ErrorMapper`, with only the HTTP layer faked — the
 * same arrangement as `AccountControlViewModelTest`, and for the same
 * reason: a fake that throws a genuine in-process [HttpException] exercises
 * the real status-and-code mapping with no real suspension point, so the
 * ViewModel's `viewModelScope.launch` runs to completion under the test
 * dispatcher.
 */
internal val testJson = Json { ignoreUnknownKeys = true }

/** A session-issuing body, as every verify route returns it. */
internal fun sessionResponse(userId: String = "u1", sessionId: String = "s1") = AuthResponseDto(
    tokens = com.us.android.core.auth.dto.TokenPairDto(
        accessToken = "at",
        refreshToken = "rt",
        expiresAt = "2026-12-01T00:00:00Z",
    ),
    user = com.us.android.core.auth.dto.AuthUserDto(id = userId),
    sessionId = sessionId,
)

internal fun httpError(code: Int, errorCode: String, message: String = "boom"): HttpException {
    val body = """{"error":{"code":"$errorCode","message":"$message"}}"""
        .toResponseBody("application/json".toMediaType())
    return HttpException(Response.error<Any>(code, body))
}

internal class FakeTokenStore : TokenStore {
    override var userId: String? = null
    override var sessionId: String? = null
    override var accessTokenExpiresAtMillis: Long = 0
    private var refresh: String? = null
    var clearCount = 0
    override fun hasRefreshToken() = refresh != null
    override fun readRefreshToken() = refresh
    override fun writeRefreshToken(token: String) { refresh = token }
    override fun clear() {
        clearCount++
        refresh = null
        userId = null
        sessionId = null
    }

    fun signedIn(): FakeTokenStore = apply {
        userId = "u1"
        sessionId = "s1"
        refresh = "rt"
    }
}

/**
 * Each route answers from its slot: a throwable is thrown, a value returned.
 * Every request is recorded so a test can assert what went on the wire.
 */
@Suppress("TooManyFunctions")
internal class FakeAuthApi : AuthApi {
    var loginResponse: AuthResponseDto = AuthResponseDto()
    var loginError: Throwable? = null
    var twoFactorResponse: AuthResponseDto = sessionResponse()
    var twoFactorError: Throwable? = null
    var stepUpResponse: AuthResponseDto = sessionResponse()
    var stepUpError: Throwable? = null
    var forgotError: Throwable? = null
    var resetError: Throwable? = null
    var oauthTokenResponse: JsonObject = JsonObject(emptyMap())
    var oauthTokenError: Throwable? = null
    var completeSignupError: Throwable? = null
    var verifySignupResponse: AuthResponseDto = sessionResponse()
    var verifySignupError: Throwable? = null

    val twoFactorRequests = mutableListOf<Verify2FARequestDto>()
    val stepUpEmailRequests = mutableListOf<StepUpVerifyRequestDto>()
    val stepUpTotpRequests = mutableListOf<StepUpVerifyRequestDto>()
    val forgotRequests = mutableListOf<ForgotPasswordRequestDto>()
    val resetRequests = mutableListOf<ResetPasswordRequestDto>()
    val oauthTokenRequests = mutableListOf<Pair<String, OAuthTokenRequestDto>>()
    val completeSignupRequests = mutableListOf<OAuthCompleteSignupRequestDto>()
    val verifySignupRequests = mutableListOf<OAuthVerifySignupRequestDto>()

    override suspend fun login(body: LoginRequestDto): ApiEnvelope<AuthResponseDto> {
        loginError?.let { throw it }
        return ApiEnvelope(loginResponse)
    }

    override suspend fun verifyTwoFactor(body: Verify2FARequestDto): ApiEnvelope<AuthResponseDto> {
        twoFactorRequests += body
        twoFactorError?.let { throw it }
        return ApiEnvelope(twoFactorResponse)
    }

    override suspend fun verifyStepUpEmail(body: StepUpVerifyRequestDto): ApiEnvelope<AuthResponseDto> {
        stepUpEmailRequests += body
        stepUpError?.let { throw it }
        return ApiEnvelope(stepUpResponse)
    }

    override suspend fun verifyStepUpTwoFactor(body: StepUpVerifyRequestDto): ApiEnvelope<AuthResponseDto> {
        stepUpTotpRequests += body
        stepUpError?.let { throw it }
        return ApiEnvelope(stepUpResponse)
    }

    override suspend fun forgotPassword(body: ForgotPasswordRequestDto): ApiEnvelope<MessageDto> {
        forgotRequests += body
        forgotError?.let { throw it }
        return ApiEnvelope(MessageDto("If the account exists, a reset code has been sent"))
    }

    override suspend fun resetPassword(body: ResetPasswordRequestDto): ApiEnvelope<MessageDto> {
        resetRequests += body
        resetError?.let { throw it }
        return ApiEnvelope(MessageDto("Password reset successfully"))
    }

    override suspend fun oauthToken(provider: String, body: OAuthTokenRequestDto): ApiEnvelope<JsonObject> {
        oauthTokenRequests += provider to body
        oauthTokenError?.let { throw it }
        return ApiEnvelope(oauthTokenResponse)
    }

    override suspend fun oauthCompleteSignup(body: OAuthCompleteSignupRequestDto): ApiEnvelope<MessageDto> {
        completeSignupRequests += body
        completeSignupError?.let { throw it }
        return ApiEnvelope(MessageDto("otp sent"))
    }

    override suspend fun oauthVerifySignup(body: OAuthVerifySignupRequestDto): ApiEnvelope<AuthResponseDto> {
        verifySignupRequests += body
        verifySignupError?.let { throw it }
        return ApiEnvelope(verifySignupResponse)
    }

    // Not exercised by the sign-in security tests.
    override suspend fun requestPartnerPhone(body: PartnerPhoneRequestDto): ApiEnvelope<PartnerPhoneChallengeDto> =
        error("not used")
    override suspend fun verifyPartnerPhone(body: PartnerPhoneVerifyDto): ApiEnvelope<AuthResponseDto> = error("not used")
    override suspend fun register(idempotencyKey: String, body: RegisterRequestDto): ApiEnvelope<AuthResponseDto> =
        error("not used")
    override suspend fun refresh(body: RefreshRequestDto): ApiEnvelope<AuthResponseDto> = error("not used")
    override suspend fun verifyEmail(body: VerifyEmailRequestDto): ApiEnvelope<MessageDto> = error("not used")
    override suspend fun resendVerification(body: ResendVerificationRequestDto): ApiEnvelope<MessageDto> =
        error("not used")
    override suspend fun logout(): ApiEnvelope<Unit> = ApiEnvelope(Unit)
    override suspend fun deactivateAccount(body: PasswordRequestDto): ApiEnvelope<AccountDeactivatedDto> =
        error("not used")
    override suspend fun deleteAccount(body: PasswordRequestDto): ApiEnvelope<AccountDeletionScheduledDto> =
        error("not used")
}

/** A scripted [GoogleSignIn]: the test decides availability and what the exchange yields. */
internal class FakeGoogleSignIn(
    override val isAvailable: Boolean = true,
    var completeResult: AppResult<String> = AppResult.Success("ya29.token"),
) : GoogleSignIn {
    private val _pending = MutableStateFlow<String?>(null)
    override val pendingRedirect: StateFlow<String?> = _pending
    var beginCount = 0
    val completed = mutableListOf<String>()

    override fun begin(): String {
        beginCount++
        return "https://accounts.example/auth?state=s$beginCount"
    }

    override fun offerRedirect(uri: String?): Boolean {
        if (uri == null || !uri.startsWith("com.googleusercontent.apps.")) return false
        _pending.value = uri
        return true
    }

    override suspend fun complete(redirect: String): AppResult<String> {
        _pending.value = null
        completed += redirect
        return completeResult
    }
}

internal fun authRepository(
    api: FakeAuthApi,
    tokenStore: FakeTokenStore,
    scope: CoroutineScope,
): AuthRepository {
    val sessionManager = SessionManager(
        tokenStore = tokenStore,
        authApi = Provider { api },
        cookieStore = CsrfCookieStore(),
        telemetry = NoOpTelemetry,
        scope = scope,
    )
    return AuthRepository(api, sessionManager, ErrorMapper(testJson), emptySet(), testJson)
}
