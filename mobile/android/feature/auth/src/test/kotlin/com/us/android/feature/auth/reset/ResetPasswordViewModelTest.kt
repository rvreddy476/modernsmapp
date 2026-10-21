package com.us.android.feature.auth.reset

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.PasswordRules
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.auth.FakeAuthApi
import com.us.android.feature.auth.FakeTokenStore
import com.us.android.feature.auth.authRepository
import com.us.android.feature.auth.httpError
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

class ResetPasswordViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val api = FakeAuthApi()
    private val tokenStore = FakeTokenStore()

    private fun viewModel(scope: kotlinx.coroutines.CoroutineScope) =
        ResetPasswordViewModel(authRepository(api, tokenStore, scope))

    private fun ResetPasswordViewModel.atResetStep(email: String = "raghu@example.com") = apply {
        start(email, fromSettings = false)
        requestCode()
        check(state.value.step == ResetPasswordStep.Reset)
    }

    @Test
    fun `requesting a code moves on with the if-an-account-exists copy, never sent`() = runTest {
        val vm = viewModel(this)
        vm.start("", fromSettings = false)
        vm.onIdentifierChange(" raghu@example.com ")

        vm.requestCode()

        assertThat(vm.state.value.step).isEqualTo(ResetPasswordStep.Reset)
        assertThat(vm.state.value.message?.text).startsWith("If an account exists for raghu@example.com")
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(60)
        assertThat(api.forgotRequests.single().identifier).isEqualTo("raghu@example.com")
    }

    @Test
    fun `recovery is email-only, so a phone number is refused before the round trip`() = runTest {
        val vm = viewModel(this)
        vm.start("", fromSettings = false)
        vm.onIdentifierChange("9876543210")

        vm.requestCode()

        assertThat(vm.state.value.identifierError).contains("email")
        assertThat(vm.state.value.step).isEqualTo(ResetPasswordStep.Request)
        assertThat(api.forgotRequests).isEmpty()
    }

    @Test
    fun `from Settings the address is locked and the code is requested on entry`() = runTest {
        val vm = viewModel(this)

        vm.start("raghu@example.com", fromSettings = true)

        assertThat(vm.state.value.fromSettings).isTrue()
        assertThat(vm.state.value.step).isEqualTo(ResetPasswordStep.Reset)
        assertThat(api.forgotRequests).hasSize(1)
        vm.editIdentifier()
        assertThat(vm.state.value.step).isEqualTo(ResetPasswordStep.Reset)
    }

    @Test
    fun `the client rules run before the server's`() = runTest {
        val vm = viewModel(this).atResetStep()
        vm.onCodeChange("123456")

        vm.onNewPasswordChange("weak")
        vm.onConfirmPasswordChange("weak")
        vm.submitNewPassword()
        assertThat(vm.state.value.newPasswordError).isEqualTo(PasswordRules.text(PasswordRules.PasswordError.TooShort))

        vm.onNewPasswordChange("Str0ng!pass")
        vm.onConfirmPasswordChange("Str0ng!pasz")
        vm.submitNewPassword()
        assertThat(vm.state.value.confirmPasswordError).isEqualTo("Those passwords don't match.")

        assertThat(api.resetRequests).isEmpty()
    }

    @Test
    fun `a good code and password reach Done with the secrets cleared from state`() = runTest {
        val vm = viewModel(this).atResetStep()
        vm.onCodeChange("123456")
        vm.onNewPasswordChange("Str0ng!pass")
        vm.onConfirmPasswordChange("Str0ng!pass")

        vm.submitNewPassword()

        assertThat(vm.state.value.step).isEqualTo(ResetPasswordStep.Done)
        assertThat(vm.state.value.newPassword).isEmpty()
        assertThat(vm.state.value.confirmPassword).isEmpty()
        assertThat(vm.state.value.code).isEmpty()
        val request = api.resetRequests.single()
        assertThat(request.identifier).isEqualTo("raghu@example.com")
        assertThat(request.code).isEqualTo("123456")
        assertThat(request.newPassword).isEqualTo("Str0ng!pass")
    }

    @Test
    fun `the server's WEAK_PASSWORD lands on the password field and keeps the code`() = runTest {
        api.resetError = httpError(422, "WEAK_PASSWORD", "password must contain an uppercase letter")
        val vm = viewModel(this).atResetStep()
        vm.onCodeChange("123456")
        vm.onNewPasswordChange("Str0ng!pass")
        vm.onConfirmPasswordChange("Str0ng!pass")

        vm.submitNewPassword()

        assertThat(vm.state.value.step).isEqualTo(ResetPasswordStep.Reset)
        assertThat(vm.state.value.newPasswordError).contains("too weak")
        assertThat(vm.state.value.code).isEqualTo("123456")
    }

    @Test
    fun `RESET_FAILED lands on the code, which is cleared`() = runTest {
        api.resetError = httpError(400, "RESET_FAILED", "Password reset failed")
        val vm = viewModel(this).atResetStep()
        vm.onCodeChange("123456")
        vm.onNewPasswordChange("Str0ng!pass")
        vm.onConfirmPasswordChange("Str0ng!pass")

        vm.submitNewPassword()

        assertThat(vm.state.value.codeError).contains("didn't work")
        assertThat(vm.state.value.code).isEmpty()
        assertThat(vm.state.value.step).isEqualTo(ResetPasswordStep.Reset)
    }

    @Test
    fun `from Settings, Sign in on the Done step ends the local session`() = runTest {
        tokenStore.signedIn()
        val vm = viewModel(this)
        vm.start("raghu@example.com", fromSettings = true)
        vm.onCodeChange("123456")
        vm.onNewPasswordChange("Str0ng!pass")
        vm.onConfirmPasswordChange("Str0ng!pass")
        vm.submitNewPassword()
        // The session survives the reset itself so the confirmation is seen.
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")

        vm.finish()
        vm.finish()

        assertThat(vm.state.value.sessionEnded).isTrue()
        assertThat(tokenStore.hasRefreshToken()).isFalse()
        assertThat(tokenStore.clearCount).isEqualTo(1)
    }

    @Test
    fun `from sign-in, finish leaves nothing to clear`() = runTest {
        val vm = viewModel(this).atResetStep()

        vm.finish()

        assertThat(vm.state.value.sessionEnded).isTrue()
        assertThat(tokenStore.clearCount).isEqualTo(0)
    }

    @Test
    fun `a 429 on the request locks the screen`() = runTest {
        api.forgotError = httpError(429, "RATE_LIMITED")
        val vm = viewModel(this)
        vm.start("raghu@example.com", fromSettings = false)

        vm.requestCode()

        assertThat(vm.state.value.lockedOut).isTrue()
        assertThat(vm.state.value.canRequest).isFalse()
        assertThat(vm.state.value.step).isEqualTo(ResetPasswordStep.Request)
    }
}
