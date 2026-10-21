package com.us.android.feature.auth.stepup

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.AuthRepository
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.auth.FakeAuthApi
import com.us.android.feature.auth.FakeTokenStore
import com.us.android.feature.auth.authRepository
import com.us.android.feature.auth.httpError
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

class StepUpViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val api = FakeAuthApi()
    private val tokenStore = FakeTokenStore()

    private fun viewModel(
        scope: kotlinx.coroutines.CoroutineScope,
        methods: List<String> = listOf("email_otp", "totp"),
    ) = StepUpViewModel(authRepository(api, tokenStore, scope)).apply { start("pt-2", methods) }

    @Test
    fun `email is the default when both channels are offered, and the emailed code signs in`() = runTest {
        val vm = viewModel(this)
        assertThat(vm.state.value.method).isEqualTo(AuthRepository.STEP_UP_METHOD_EMAIL)
        assertThat(vm.state.value.canSwitchMethod).isTrue()

        vm.onCodeChange("654321")
        vm.submit()

        assertThat(vm.state.value.signedIn).isTrue()
        assertThat(api.stepUpEmailRequests.single().pendingToken).isEqualTo("pt-2")
        assertThat(api.stepUpTotpRequests).isEmpty()
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
    }

    @Test
    fun `switching to the authenticator uses the 2fa route`() = runTest {
        val vm = viewModel(this)

        vm.onCodeChange("12")
        vm.switchMethod()
        assertThat(vm.state.value.method).isEqualTo(AuthRepository.STEP_UP_METHOD_TOTP)
        assertThat(vm.state.value.code).isEmpty()

        vm.onCodeChange("654321")
        vm.submit()

        assertThat(api.stepUpTotpRequests.single().code).isEqualTo("654321")
        assertThat(api.stepUpEmailRequests).isEmpty()
    }

    @Test
    fun `one offered channel cannot be switched, and unknown channels are dropped`() = runTest {
        val vm = viewModel(this, methods = listOf("sms_otp", "totp"))

        assertThat(vm.state.value.methods).containsExactly("totp")
        assertThat(vm.state.value.method).isEqualTo("totp")
        assertThat(vm.state.value.canSwitchMethod).isFalse()
    }

    @Test
    fun `a wrong code is an inline error with the field cleared`() = runTest {
        api.stepUpError = httpError(401, "STEP_UP_CODE_INVALID", "Incorrect code. Please try again.")
        val vm = viewModel(this)

        vm.onCodeChange("000000")
        vm.submit()

        assertThat(vm.state.value.codeError).isEqualTo("That code isn't right. Please try again.")
        assertThat(vm.state.value.code).isEmpty()
        assertThat(vm.state.value.expired).isFalse()
    }

    @Test
    fun `an expired pending session sends the person back to sign in`() = runTest {
        api.stepUpError = httpError(401, "STEP_UP_EXPIRED")
        val vm = viewModel(this)

        vm.onCodeChange("000000")
        vm.submit()

        assertThat(vm.state.value.expired).isTrue()
        assertThat(vm.state.value.canSubmit).isFalse()
        assertThat(vm.state.value.message?.text).contains("sign in again")
    }

    @Test
    fun `a replayed code says so`() = runTest {
        api.stepUpError = httpError(401, "STEP_UP_CODE_REPLAYED")
        val vm = viewModel(this)

        vm.onCodeChange("000000")
        vm.submit()

        assertThat(vm.state.value.codeError).contains("already been used")
    }

    @Test
    fun `a 429 locks the form`() = runTest {
        api.stepUpError = httpError(429, "RATE_LIMITED")
        val vm = viewModel(this)

        vm.onCodeChange("000000")
        vm.submit()

        assertThat(vm.state.value.lockedOut).isTrue()
        assertThat(vm.state.value.canSubmit).isFalse()
    }
}
