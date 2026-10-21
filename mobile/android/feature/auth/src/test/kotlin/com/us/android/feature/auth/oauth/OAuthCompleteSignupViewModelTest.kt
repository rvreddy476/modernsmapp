package com.us.android.feature.auth.oauth

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.auth.FakeAuthApi
import com.us.android.feature.auth.FakeTokenStore
import com.us.android.feature.auth.authRepository
import com.us.android.feature.auth.httpError
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

class OAuthCompleteSignupViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val api = FakeAuthApi()
    private val tokenStore = FakeTokenStore()

    private fun viewModel(scope: kotlinx.coroutines.CoroutineScope) =
        OAuthCompleteSignupViewModel(authRepository(api, tokenStore, scope)).apply {
            start("op-1", "google", "raghu@example.com")
        }

    @Test
    fun `the provider is named and the phone is validated before the round trip`() = runTest {
        val vm = viewModel(this)
        assertThat(vm.state.value.providerLabel).isEqualTo("Google")

        vm.onPhoneChange("12345")
        assertThat(vm.state.value.canSendCode).isFalse()
        vm.onPhoneChange("1234567890")
        vm.sendCode()

        assertThat(vm.state.value.phoneError).contains("valid 10-digit")
        assertThat(api.completeSignupRequests).isEmpty()
    }

    @Test
    fun `a good number is sent with the country code and moves to the code step`() = runTest {
        val vm = viewModel(this)

        vm.onPhoneChange("98765 43210")
        vm.sendCode()

        assertThat(api.completeSignupRequests.single().phone).isEqualTo("+919876543210")
        assertThat(api.completeSignupRequests.single().pendingToken).isEqualTo("op-1")
        assertThat(vm.state.value.step).isEqualTo(OAuthSignupStep.Code)
    }

    @Test
    fun `OAUTH_SIGNUP_FAILED lands on the phone field`() = runTest {
        api.completeSignupError = httpError(400, "OAUTH_SIGNUP_FAILED", "cannot complete signup with this phone")
        val vm = viewModel(this)

        vm.onPhoneChange("9876543210")
        vm.sendCode()

        assertThat(vm.state.value.step).isEqualTo(OAuthSignupStep.Phone)
        assertThat(vm.state.value.phoneError).contains("phone number")
    }

    @Test
    fun `the code creates the account and stores the session`() = runTest {
        val vm = viewModel(this)
        vm.onPhoneChange("9876543210")
        vm.sendCode()

        vm.onCodeChange("123456")
        vm.verify()

        assertThat(vm.state.value.signedIn).isTrue()
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
        val request = api.verifySignupRequests.single()
        assertThat(request.otp).isEqualTo("123456")
        assertThat(request.pendingToken).isEqualTo("op-1")
        assertThat(request.deviceId).isNotEmpty()
    }

    @Test
    fun `a wrong code is an inline error and the field is cleared`() = runTest {
        api.verifySignupError = httpError(401, "OAUTH_VERIFY_FAILED", "invalid otp")
        val vm = viewModel(this)
        vm.onPhoneChange("9876543210")
        vm.sendCode()

        vm.onCodeChange("000000")
        vm.verify()

        assertThat(vm.state.value.codeError).contains("didn't work")
        assertThat(vm.state.value.code).isEmpty()
        assertThat(vm.state.value.signedIn).isFalse()
    }

    @Test
    fun `changing the number returns to the phone step and keeps it`() = runTest {
        val vm = viewModel(this)
        vm.onPhoneChange("9876543210")
        vm.sendCode()

        vm.changePhone()

        assertThat(vm.state.value.step).isEqualTo(OAuthSignupStep.Phone)
        assertThat(vm.state.value.phone).isEqualTo("9876543210")
    }
}
