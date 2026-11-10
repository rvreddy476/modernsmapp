package com.us.android.feature.profile.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.AccountSecurityApi
import com.us.android.core.profile.data.AccountSummary
import com.us.android.core.profile.data.DeviceSecurityApi
import com.us.android.core.profile.data.SecuritySettingsRepository
import com.us.android.core.profile.data.TwoFactorMethod
import com.us.android.core.profile.data.TwoFactorPurpose
import com.us.android.core.profile.data.dto.AccountSessionDto
import com.us.android.core.profile.data.dto.AccountSummaryDto
import com.us.android.core.profile.data.dto.DisableTwoFactorRequest
import com.us.android.core.profile.data.dto.EmailChallengeCodeRequest
import com.us.android.core.profile.data.dto.EmailChallengeDto
import com.us.android.core.profile.data.dto.EmailTwoFactorStartRequest
import com.us.android.core.profile.data.dto.SecurityEventDto
import com.us.android.core.profile.data.dto.StatusDto
import com.us.android.core.profile.data.dto.TrustedDeviceDto
import com.us.android.core.profile.data.dto.TwoFactorStatusDto
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/**
 * Settings › Two-step verification by emailed code (founder, 10 Nov 2026),
 * against the real repository and ErrorMapper with only the HTTP layer
 * faked, so the status-and-code mapping is the production one.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class TwoFactorSettingsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }
    private val api = FakeAccountSecurityApi()
    private val vm = TwoFactorSettingsViewModel(SecuritySettingsRepository(api, FakeDeviceApi(), ErrorMapper(json)))

    private fun account(method: TwoFactorMethod, verified: Boolean = true) = AccountSummary(
        userId = "u1",
        email = "raghu@gmail.com",
        phone = "",
        emailVerified = verified,
        phoneVerified = false,
        twoFactorEnabled = method != TwoFactorMethod.None,
        accountType = "user",
        accountStatus = "active",
        ageVerification = "",
        lastLoginAt = "",
        createdAt = "",
        twoFactorMethod = method,
    )

    private fun TestScope.waitOut(seconds: Int) {
        advanceTimeBy(seconds * 1_000L)
        runCurrent()
    }

    private fun toCode(purpose: TwoFactorPurpose) {
        if (purpose == TwoFactorPurpose.Enable) vm.turnOn() else vm.turnOff()
        vm.submitPassword("hunter2")
    }

    // ── Turning it on ──────────────────────────────────────────────────

    @Test
    fun `turning on is password, then the emailed code, then on`() = runTest {
        vm.bind(account(TwoFactorMethod.None))

        vm.turnOn()
        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.Password(TwoFactorPurpose.Enable))

        vm.submitPassword("hunter2")
        assertThat(api.starts.single()).isEqualTo(EmailTwoFactorStartRequest("hunter2", "enable"))
        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.Code(TwoFactorPurpose.Enable, "r***@gmail.com"))
        assertThat(vm.state.value.resendInSeconds).isEqualTo(TwoFactorSettingsUiState.RESEND_COOLDOWN_SECONDS)

        vm.submitCode("123456")
        assertThat(api.enables.single()).isEqualTo(EmailChallengeCodeRequest("ch-1", "123456"))
        val state = vm.state.value
        assertThat(state.step).isEqualTo(TwoFactorStep.Idle)
        assertThat(state.method).isEqualTo(TwoFactorMethod.Email)
        assertThat(state.emailMasked).isEqualTo("r***@gmail.com")
        assertThat(state.done).isEqualTo(TwoFactorDone.TurnedOn)
        assertThat(state.resendInSeconds).isEqualTo(0)
    }

    @Test
    fun `an unverified email cannot start turning it on`() = runTest {
        vm.bind(account(TwoFactorMethod.None, verified = false))

        vm.turnOn()

        assertThat(vm.state.value.canTurnOn).isFalse()
        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.Idle)
    }

    @Test
    fun `a wrong password stays on the password step`() = runTest {
        api.startError = httpError(401, "INVALID_CREDENTIALS")
        vm.bind(account(TwoFactorMethod.None))

        toCode(TwoFactorPurpose.Enable)

        assertThat(vm.state.value.problem).isEqualTo(TwoFactorProblem.WrongPassword)
        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.Password(TwoFactorPurpose.Enable))
        assertThat(vm.state.value.busy).isFalse()
    }

    @Test
    fun `a wrong code stays on the code step and an expired one asks for a new code`() = runTest {
        vm.bind(account(TwoFactorMethod.None))
        toCode(TwoFactorPurpose.Enable)

        api.enableError = httpError(400, "INVALID_CODE")
        vm.submitCode("000000")
        assertThat(vm.state.value.problem).isEqualTo(TwoFactorProblem.WrongCode)
        assertThat(vm.state.value.step).isInstanceOf(TwoFactorStep.Code::class.java)
        assertThat(vm.state.value.method).isEqualTo(TwoFactorMethod.None)

        api.enableError = httpError(400, "INVALID_CHALLENGE")
        vm.submitCode("111111")
        assertThat(vm.state.value.problem).isEqualTo(TwoFactorProblem.CodeExpired)
    }

    @Test
    fun `a short code is never sent`() = runTest {
        vm.bind(account(TwoFactorMethod.None))
        toCode(TwoFactorPurpose.Enable)

        vm.submitCode("123")

        assertThat(api.enables).isEmpty()
    }

    // ── Resend ─────────────────────────────────────────────────────────

    @Test
    fun `Resend waits out the cooldown, then starts again with the same password and uses the new challenge`() =
        runTest {
            vm.bind(account(TwoFactorMethod.None))
            toCode(TwoFactorPurpose.Enable)

            vm.resend()
            assertThat(api.starts).hasSize(1)

            waitOut(30)
            assertThat(vm.state.value.resendInSeconds).isEqualTo(30)
            waitOut(30)
            assertThat(vm.state.value.canResend).isTrue()

            api.nextChallenge = "ch-2"
            vm.resend()
            assertThat(api.starts).hasSize(2)
            assertThat(api.starts.last()).isEqualTo(EmailTwoFactorStartRequest("hunter2", "enable"))
            assertThat(vm.state.value.done).isEqualTo(TwoFactorDone.CodeResent)
            assertThat(vm.state.value.resendInSeconds).isEqualTo(TwoFactorSettingsUiState.RESEND_COOLDOWN_SECONDS)

            vm.submitCode("123456")
            assertThat(api.enables.single().challengeToken).isEqualTo("ch-2")
        }

    @Test
    fun `a 429 on Resend shows the wait and holds Resend for its Retry-After`() = runTest {
        vm.bind(account(TwoFactorMethod.None))
        toCode(TwoFactorPurpose.Enable)
        waitOut(60)

        api.startError = rateLimited("90")
        vm.resend()

        assertThat(vm.state.value.problem).isEqualTo(TwoFactorProblem.TooManyTries(90))
        assertThat(vm.state.value.resendInSeconds).isEqualTo(90)
        assertThat(vm.state.value.step).isInstanceOf(TwoFactorStep.Code::class.java)
    }

    // ── Turning it off ─────────────────────────────────────────────────

    @Test
    fun `turning off an email account sends the disable challenge and code to 2fa disable`() = runTest {
        vm.bind(account(TwoFactorMethod.Email))

        toCode(TwoFactorPurpose.Disable)
        assertThat(api.starts.single()).isEqualTo(EmailTwoFactorStartRequest("hunter2", "disable"))

        vm.submitCode("654321")
        assertThat(api.emailDisables.single()).isEqualTo(EmailChallengeCodeRequest("ch-1", "654321"))
        assertThat(api.totpDisables).isEmpty()
        assertThat(vm.state.value.method).isEqualTo(TwoFactorMethod.None)
        assertThat(vm.state.value.done).isEqualTo(TwoFactorDone.TurnedOff)
    }

    @Test
    fun `a legacy authenticator account turns off with password and app code, never an email`() = runTest {
        vm.bind(account(TwoFactorMethod.Authenticator))

        vm.turnOff()
        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.AuthenticatorOff)
        vm.disableAuthenticator("hunter2", "123456")

        assertThat(api.starts).isEmpty()
        assertThat(api.totpDisables.single()).isEqualTo(DisableTwoFactorRequest("hunter2", "123456"))
        assertThat(vm.state.value.method).isEqualTo(TwoFactorMethod.None)
    }

    @Test
    fun `an authenticator refusal stays on its step`() = runTest {
        api.totpDisableError = httpError(400, "2FA_DISABLE_FAILED")
        vm.bind(account(TwoFactorMethod.Authenticator))

        vm.turnOff()
        vm.disableAuthenticator("hunter2", "000000")

        assertThat(vm.state.value.problem).isEqualTo(TwoFactorProblem.AuthenticatorRejected)
        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.AuthenticatorOff)
        assertThat(vm.state.value.method).isEqualTo(TwoFactorMethod.Authenticator)
    }

    @Test
    fun `an unknown method is shown as on but offers no flow`() = runTest {
        vm.bind(account(TwoFactorMethod.Other))

        vm.turnOff()
        vm.turnOn()

        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.Idle)
    }

    // ── State that changed elsewhere ───────────────────────────────────

    @Test
    fun `a method mismatch drops the flow and reloads the account`() = runTest {
        api.startError = httpError(409, "TWO_FACTOR_METHOD_MISMATCH")
        api.account =
            AccountSummaryDto(userId = "u1", twoFactorEnabled = true, twoFactorMethod = "totp", emailVerified = true)
        vm.bind(account(TwoFactorMethod.Email))

        toCode(TwoFactorPurpose.Disable)

        assertThat(vm.state.value.problem).isEqualTo(TwoFactorProblem.StateChanged)
        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.Idle)
        assertThat(vm.state.value.method).isEqualTo(TwoFactorMethod.Authenticator)
    }

    @Test
    fun `a reload mid-flow does not reset the step`() = runTest {
        vm.bind(account(TwoFactorMethod.None))
        toCode(TwoFactorPurpose.Enable)

        vm.bind(account(TwoFactorMethod.Email))

        assertThat(vm.state.value.step).isInstanceOf(TwoFactorStep.Code::class.java)
        assertThat(vm.state.value.method).isEqualTo(TwoFactorMethod.None)
    }

    @Test
    fun `Cancel forgets the password, so Resend after Cancel sends nothing`() = runTest {
        vm.bind(account(TwoFactorMethod.None))
        toCode(TwoFactorPurpose.Enable)

        vm.cancel()
        waitOut(60)
        vm.resend()

        assertThat(vm.state.value.step).isEqualTo(TwoFactorStep.Idle)
        assertThat(api.starts).hasSize(1)
    }

    // ── Error mapping ──────────────────────────────────────────────────

    @Test
    fun `every server refusal has its own problem`() {
        val mapper = ErrorMapper(json)
        fun of(status: Int, code: String) = twoFactorProblemOf(mapper.map(httpError(status, code)))

        assertThat(of(401, "INVALID_CREDENTIALS")).isEqualTo(TwoFactorProblem.WrongPassword)
        assertThat(of(400, "INVALID_CODE")).isEqualTo(TwoFactorProblem.WrongCode)
        assertThat(of(400, "INVALID_CHALLENGE")).isEqualTo(TwoFactorProblem.CodeExpired)
        assertThat(of(409, "EMAIL_NOT_VERIFIED")).isEqualTo(TwoFactorProblem.EmailNotVerified)
        assertThat(of(409, "TWO_FACTOR_ALREADY_ENABLED")).isEqualTo(TwoFactorProblem.StateChanged)
        assertThat(of(409, "TWO_FACTOR_NOT_ENABLED")).isEqualTo(TwoFactorProblem.StateChanged)
        assertThat(of(409, "TWO_FACTOR_METHOD_MISMATCH")).isEqualTo(TwoFactorProblem.StateChanged)
        assertThat(of(503, "EMAIL_UNAVAILABLE")).isEqualTo(TwoFactorProblem.EmailUnavailable)
        assertThat(of(400, "2FA_DISABLE_FAILED")).isEqualTo(TwoFactorProblem.AuthenticatorRejected)
        assertThat(of(500, "INTERNAL")).isEqualTo(TwoFactorProblem.Failed)
        assertThat(twoFactorProblemOf(mapper.map(rateLimited("30")))).isEqualTo(TwoFactorProblem.TooManyTries(30))
        assertThat(twoFactorProblemOf(AppError.NoNetwork())).isEqualTo(TwoFactorProblem.Offline)
    }

    @Test
    fun `the status line masks the account's own address like the server does`() {
        assertThat(maskEmail("raghu@gmail.com")).isEqualTo("r***@gmail.com")
        assertThat(maskEmail("")).isEmpty()
        assertThat(maskEmail("@nobody")).isEmpty()
    }

    // ── Fakes ──────────────────────────────────────────────────────────

    private class FakeAccountSecurityApi : AccountSecurityApi {
        var account = AccountSummaryDto(userId = "u1")
        var nextChallenge = "ch-1"
        var startError: Throwable? = null
        var enableError: Throwable? = null
        var totpDisableError: Throwable? = null
        val starts = mutableListOf<EmailTwoFactorStartRequest>()
        val enables = mutableListOf<EmailChallengeCodeRequest>()
        val emailDisables = mutableListOf<EmailChallengeCodeRequest>()
        val totpDisables = mutableListOf<DisableTwoFactorRequest>()

        override suspend fun account() = ApiEnvelope(account)
        override suspend fun securityEvents() = ApiEnvelope(emptyList<SecurityEventDto>())
        override suspend fun acknowledgeEvent(id: String) = ApiEnvelope(StatusDto("ok"))

        override suspend fun startEmailTwoFactor(body: EmailTwoFactorStartRequest): ApiEnvelope<EmailChallengeDto> {
            starts += body
            startError?.let { throw it }
            return ApiEnvelope(EmailChallengeDto(nextChallenge, "2026-11-10T10:05:00Z", "r***@gmail.com"))
        }

        override suspend fun enableEmailTwoFactor(body: EmailChallengeCodeRequest): ApiEnvelope<TwoFactorStatusDto> {
            enables += body
            enableError?.let { throw it }
            return ApiEnvelope(TwoFactorStatusDto(enabled = true, method = "email"))
        }

        override suspend fun disableEmailTwoFactor(body: EmailChallengeCodeRequest): ApiEnvelope<TwoFactorStatusDto> {
            emailDisables += body
            return ApiEnvelope(TwoFactorStatusDto(enabled = false))
        }

        override suspend fun disableTwoFactor(body: DisableTwoFactorRequest): ApiEnvelope<TwoFactorStatusDto> {
            totpDisables += body
            totpDisableError?.let { throw it }
            return ApiEnvelope(TwoFactorStatusDto(enabled = false))
        }
    }

    private class FakeDeviceApi : DeviceSecurityApi {
        override suspend fun sessions() = ApiEnvelope(emptyList<AccountSessionDto>())
        override suspend fun revokeSession(id: String) = ApiEnvelope(StatusDto("ok"))
        override suspend fun logoutAll() = ApiEnvelope(StatusDto("ok"))
        override suspend fun trustedDevices() = ApiEnvelope(emptyList<TrustedDeviceDto>())
        override suspend fun removeTrustedDevice(id: String) = ApiEnvelope(StatusDto("ok"))
    }

    private companion object {
        fun httpError(status: Int, code: String): HttpException {
            val body = """{"error":{"code":"$code","message":"boom"}}"""
                .toResponseBody("application/json".toMediaType())
            return HttpException(Response.error<Any>(status, body))
        }

        fun rateLimited(retryAfter: String): HttpException {
            val body = """{"error":{"code":"RATE_LIMITED","message":"wait"}}"""
                .toResponseBody("application/json".toMediaType())
            val raw = okhttp3.Response.Builder()
                .code(429)
                .message("Too Many Requests")
                .protocol(Protocol.HTTP_1_1)
                .request(Request.Builder().url("http://localhost/v1/auth/2fa/email/start").build())
                .header("Retry-After", retryAfter)
                .build()
            return HttpException(Response.error<Any>(body, raw))
        }
    }
}
