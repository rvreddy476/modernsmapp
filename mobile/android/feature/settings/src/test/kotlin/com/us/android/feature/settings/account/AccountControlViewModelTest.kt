package com.us.android.feature.settings.account

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.AuthApi
import com.us.android.core.auth.AuthRepository
import com.us.android.core.auth.SessionManager
import com.us.android.core.auth.TokenStore
import com.us.android.core.auth.dto.AccountDeactivatedDto
import com.us.android.core.auth.dto.AccountDeletionScheduledDto
import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.LoginRequestDto
import com.us.android.core.auth.dto.MessageDto
import com.us.android.core.auth.dto.PasswordRequestDto
import com.us.android.core.auth.dto.RefreshRequestDto
import com.us.android.core.auth.dto.RegisterRequestDto
import com.us.android.core.auth.dto.ResendVerificationRequestDto
import com.us.android.core.auth.dto.VerifyEmailRequestDto
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.cookie.CsrfCookieStore
import com.us.android.core.telemetry.NoOpTelemetry
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import javax.inject.Provider

class AccountControlViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private class FakeTokenStore(
        userId: String? = "u1",
        refreshToken: String? = "rt",
    ) : TokenStore {
        override var userId: String? = userId
        override var sessionId: String? = "s1"
        override var accessTokenExpiresAtMillis: Long = 0
        private var refresh = refreshToken
        override fun hasRefreshToken() = refresh != null
        override fun readRefreshToken() = refresh
        override fun writeRefreshToken(token: String) { refresh = token }
        override fun clear() {
            refresh = null
            userId = null
            sessionId = null
        }
    }

    /**
     * A hand-written fake rather than MockWebServer: this test exercises the
     * VIEWMODEL's fire-and-forget `viewModelScope.launch`, which nothing here
     * awaits directly. A real OkHttp round trip is asynchronous on real
     * threads outside the test dispatcher's control, so the assertions would
     * race it. This fake throws a genuine [HttpException] built in-process
     * (no socket), so it still runs through the real [ErrorMapper] the same
     * way a live 401/409 would, but with no real suspension point — the
     * whole `launch` runs to completion synchronously under
     * `UnconfinedTestDispatcher`, the same way every other ViewModel test in
     * this codebase relies on a fake API.
     */
    private class FakeAuthApi : AuthApi {
        var deactivateError: HttpException? = null
        var deleteResult: ApiEnvelope<AccountDeletionScheduledDto>? = null
        var deleteError: HttpException? = null

        override suspend fun login(body: LoginRequestDto) = ApiEnvelope(AuthResponseDto())
        override suspend fun register(idempotencyKey: String, body: RegisterRequestDto) = ApiEnvelope(AuthResponseDto())
        override suspend fun refresh(body: RefreshRequestDto) = ApiEnvelope(AuthResponseDto())
        override suspend fun verifyEmail(body: VerifyEmailRequestDto) = ApiEnvelope(MessageDto())
        override suspend fun resendVerification(body: ResendVerificationRequestDto) = ApiEnvelope(MessageDto())
        override suspend fun logout() = ApiEnvelope(Unit)

        override suspend fun deactivateAccount(body: PasswordRequestDto): ApiEnvelope<AccountDeactivatedDto> {
            deactivateError?.let { throw it }
            return ApiEnvelope(AccountDeactivatedDto())
        }

        override suspend fun deleteAccount(body: PasswordRequestDto): ApiEnvelope<AccountDeletionScheduledDto> {
            deleteError?.let { throw it }
            return deleteResult ?: ApiEnvelope(AccountDeletionScheduledDto())
        }
    }

    private fun httpError(code: Int, errorCode: String): HttpException {
        val body = """{"error":{"code":"$errorCode","message":"boom"}}"""
            .toResponseBody("application/json".toMediaType())
        return HttpException(Response.error<Any>(code, body))
    }

    private fun buildViewModel(
        api: FakeAuthApi = FakeAuthApi(),
        tokenStore: FakeTokenStore = FakeTokenStore(),
        scope: kotlinx.coroutines.CoroutineScope,
    ): AccountControlViewModel {
        val sessionManager = SessionManager(
            tokenStore = tokenStore,
            authApi = Provider { api },
            cookieStore = CsrfCookieStore(),
            telemetry = NoOpTelemetry,
            scope = scope,
        )
        val authRepository = AuthRepository(api, sessionManager, ErrorMapper(json), emptySet())
        return AccountControlViewModel(authRepository)
    }

    @Test
    fun `a wrong password on deactivate shows an inline error and keeps the sheet open`() = runTest {
        val tokenStore = FakeTokenStore()
        val api = FakeAuthApi().apply { deactivateError = httpError(401, "INVALID_PASSWORD") }
        val viewModel = buildViewModel(api, tokenStore, this)

        viewModel.openSheet(AccountControlAction.DEACTIVATE)
        viewModel.setPassword("wrong")
        viewModel.confirm()

        val state = viewModel.state.value
        assertThat(state.error).isEqualTo("Incorrect password.")
        assertThat(state.signedOut).isFalse()
        assertThat(state.activeSheet).isEqualTo(AccountControlAction.DEACTIVATE)
        assertThat(tokenStore.hasRefreshToken()).isTrue()
    }

    @Test
    fun `a correct password on deactivate clears the session and signals signed out`() = runTest {
        val tokenStore = FakeTokenStore()
        val viewModel = buildViewModel(tokenStore = tokenStore, scope = this)

        viewModel.openSheet(AccountControlAction.DEACTIVATE)
        viewModel.setPassword("hunter2")
        viewModel.confirm()

        val state = viewModel.state.value
        assertThat(state.signedOut).isTrue()
        assertThat(state.error).isNull()
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `delete succeeds the same way deactivate does`() = runTest {
        val tokenStore = FakeTokenStore()
        val api = FakeAuthApi().apply {
            deleteResult = ApiEnvelope(AccountDeletionScheduledDto(scheduledPurgeDate = "2026-10-03T00:00:00Z"))
        }
        val viewModel = buildViewModel(api, tokenStore, this)

        viewModel.openSheet(AccountControlAction.DELETE)
        viewModel.setPassword("hunter2")
        viewModel.confirm()

        assertThat(viewModel.state.value.signedOut).isTrue()
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `a state conflict surfaces its own message and does not sign the user out`() = runTest {
        val tokenStore = FakeTokenStore()
        val api = FakeAuthApi().apply { deactivateError = httpError(409, "ACCOUNT_STATE_CONFLICT") }
        val viewModel = buildViewModel(api, tokenStore, this)

        viewModel.openSheet(AccountControlAction.DEACTIVATE)
        viewModel.setPassword("hunter2")
        viewModel.confirm()

        val state = viewModel.state.value
        assertThat(state.error).contains("already in that state")
        assertThat(state.signedOut).isFalse()
        assertThat(tokenStore.hasRefreshToken()).isTrue()
    }

    @Test
    fun `dismissing the sheet resets the password and error`() = runTest {
        val viewModel = buildViewModel(scope = this)
        viewModel.openSheet(AccountControlAction.DEACTIVATE)
        viewModel.setPassword("something")

        viewModel.dismissSheet()

        val state = viewModel.state.value
        assertThat(state.activeSheet).isNull()
        assertThat(state.password).isEmpty()
    }
}
