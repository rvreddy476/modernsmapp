package com.us.android.feature.auth.twofactor

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.dto.TwoFactorResendDto
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.auth.FakeAuthApi
import com.us.android.feature.auth.FakeTokenStore
import com.us.android.feature.auth.authRepository
import com.us.android.feature.auth.httpError
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Protocol
import okhttp3.Request
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/**
 * The emailed two-step sign-in (founder, 10 Nov 2026): the login response
 * says `two_factor_method: "email"`, the code is already in the inbox, and
 * the screen offers Resend on the server's 60-second cooldown.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class TwoFactorEmailCodeTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val api = FakeAuthApi()
    private val tokenStore = FakeTokenStore()

    private fun emailed(scope: CoroutineScope) = TwoFactorViewModel(authRepository(api, tokenStore, scope))
        .apply { start("u1", "pt-1", method = "email", emailMasked = "r***@gmail.com") }

    private fun TestScope.waitOut(seconds: Int) {
        advanceTimeBy(seconds * 1_000L)
        runCurrent()
    }

    private fun rateLimited(retryAfter: String): HttpException {
        val body = """{"error":{"code":"RATE_LIMITED","message":"wait"}}"""
            .toResponseBody("application/json".toMediaType())
        val raw = okhttp3.Response.Builder()
            .code(429)
            .message("Too Many Requests")
            .protocol(Protocol.HTTP_1_1)
            .request(Request.Builder().url("http://localhost/v1/auth/2fa/resend").build())
            .header("Retry-After", retryAfter)
            .build()
        return HttpException(Response.error<Any>(body, raw))
    }

    @Test
    fun `an emailed code opens with the address, no recovery option and the resend timer running`() = runTest {
        val vm = emailed(this)

        val state = vm.state.value
        assertThat(state.isEmailCode).isTrue()
        assertThat(state.emailMasked).isEqualTo("r***@gmail.com")
        assertThat(state.resendInSeconds).isEqualTo(TwoFactorUiState.RESEND_COOLDOWN_SECONDS)
        assertThat(state.canResend).isFalse()

        vm.toggleRecoveryCode()
        assertThat(vm.state.value.useRecoveryCode).isFalse()
    }

    @Test
    fun `the timer counts down to Resend, and a second start for the same sign-in does not restart it`() = runTest {
        val vm = emailed(this)

        waitOut(20)
        assertThat(vm.state.value.resendInSeconds).isEqualTo(40)
        vm.start("u1", "pt-1", method = "email", emailMasked = "r***@gmail.com")
        assertThat(vm.state.value.resendInSeconds).isEqualTo(40)

        vm.resend()
        assertThat(api.resendRequests).isEmpty()

        waitOut(40)
        assertThat(vm.state.value.resendInSeconds).isEqualTo(0)
        assertThat(vm.state.value.canResend).isTrue()
    }

    @Test
    fun `Resend posts the pending token and restarts the timer from the server's cooldown`() = runTest {
        api.resendResponse = TwoFactorResendDto(sent = true, emailMasked = "r***@gmail.com", resendAfterSeconds = 45)
        val vm = emailed(this)
        waitOut(60)

        vm.onCodeChange("12")
        vm.resend()

        assertThat(api.resendRequests.single().pendingToken).isEqualTo("pt-1")
        val state = vm.state.value
        assertThat(state.emailNotice).isEqualTo(EmailCodeNotice.CodeSent)
        assertThat(state.resendInSeconds).isEqualTo(45)
        assertThat(state.code).isEmpty()
        assertThat(state.canResend).isFalse()
    }

    @Test
    fun `a resend with no cooldown in the body falls back to sixty seconds`() = runTest {
        api.resendResponse = TwoFactorResendDto(sent = true)
        val vm = emailed(this)
        waitOut(60)

        vm.resend()

        assertThat(vm.state.value.resendInSeconds).isEqualTo(TwoFactorUiState.RESEND_COOLDOWN_SECONDS)
        assertThat(vm.state.value.emailMasked).isEqualTo("r***@gmail.com")
    }

    @Test
    fun `a 429 on resend waits out its Retry-After`() = runTest {
        api.resendError = rateLimited("37")
        val vm = emailed(this)
        waitOut(60)

        vm.resend()

        assertThat(vm.state.value.emailNotice).isEqualTo(EmailCodeNotice.ResendTooSoon)
        assertThat(vm.state.value.resendInSeconds).isEqualTo(37)
        assertThat(vm.state.value.lockedOut).isFalse()
    }

    @Test
    fun `resend refusals map to their own notices`() = runTest {
        val vm = emailed(this)
        waitOut(60)

        api.resendError = httpError(503, "EMAIL_UNAVAILABLE")
        vm.resend()
        assertThat(vm.state.value.emailNotice).isEqualTo(EmailCodeNotice.EmailUnavailable)

        api.resendError = httpError(401, "AUTH_FAILED")
        vm.resend()
        assertThat(vm.state.value.emailNotice).isEqualTo(EmailCodeNotice.SignInExpired)
    }

    @Test
    fun `a method mismatch on resend falls back to the authenticator screen`() = runTest {
        api.resendError = httpError(409, "TWO_FACTOR_METHOD_MISMATCH")
        val vm = emailed(this)
        waitOut(60)

        vm.resend()

        assertThat(vm.state.value.isEmailCode).isFalse()
        assertThat(vm.state.value.canResend).isFalse()
    }

    @Test
    fun `a wrong emailed code is an inline notice that typing clears`() = runTest {
        api.twoFactorError = httpError(401, "AUTH_FAILED")
        val vm = emailed(this)

        vm.onCodeChange("000000")
        vm.submit()

        assertThat(vm.state.value.emailNotice).isEqualTo(EmailCodeNotice.WrongCode)
        assertThat(vm.state.value.codeError).isNull()
        assertThat(vm.state.value.code).isEmpty()

        vm.onCodeChange("1")
        assertThat(vm.state.value.emailNotice).isNull()
    }

    @Test
    fun `a correct emailed code goes to 2fa verify like an authenticator code`() = runTest {
        val vm = emailed(this)

        vm.onCodeChange("654321")
        vm.submit()

        assertThat(vm.state.value.signedIn).isTrue()
        assertThat(api.twoFactorRequests.single().code).isEqualTo("654321")
        assertThat(api.twoFactorRequests.single().pendingToken).isEqualTo("pt-1")
    }

    @Test
    fun `an unknown or missing method is an authenticator sign-in`() = runTest {
        for (method in listOf("", "totp", "sms")) {
            val vm = TwoFactorViewModel(authRepository(api, tokenStore, this))
            vm.start("u1", "pt-$method", method)

            assertThat(vm.state.value.isEmailCode).isFalse()
            assertThat(vm.state.value.resendInSeconds).isEqualTo(0)
            vm.resend()
            vm.toggleRecoveryCode()
            assertThat(vm.state.value.useRecoveryCode).isTrue()
        }
        assertThat(api.resendRequests).isEmpty()
    }
}
