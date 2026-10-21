package com.us.android.feature.auth.twofactor

import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.auth.FakeAuthApi
import com.us.android.feature.auth.FakeTokenStore
import com.us.android.feature.auth.authRepository
import com.us.android.feature.auth.httpError
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

class TwoFactorViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val api = FakeAuthApi()
    private val tokenStore = FakeTokenStore()

    private fun viewModel(scope: kotlinx.coroutines.CoroutineScope) =
        TwoFactorViewModel(authRepository(api, tokenStore, scope)).apply { start("u1", "pt-1") }

    @Test
    fun `a correct code stores the session and reports signed in`() = runTest {
        val vm = viewModel(this)

        vm.onCodeChange("123456")
        vm.submit()

        assertThat(vm.state.value.signedIn).isTrue()
        assertThat(vm.state.value.isSubmitting).isFalse()
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
        assertThat(api.twoFactorRequests.single().userId).isEqualTo("u1")
        assertThat(api.twoFactorRequests.single().pendingToken).isEqualTo("pt-1")
        assertThat(api.twoFactorRequests.single().code).isEqualTo("123456")
    }

    @Test
    fun `a wrong code is an inline error, the field is cleared and nothing is stored`() = runTest {
        api.twoFactorError = httpError(401, "AUTH_FAILED", "2FA verification failed")
        val vm = viewModel(this)

        vm.onCodeChange("000000")
        vm.submit()

        val state = vm.state.value
        assertThat(state.codeError).contains("didn't work")
        assertThat(state.code).isEmpty()
        assertThat(state.signedIn).isFalse()
        assertThat(state.lockedOut).isFalse()
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `the login limiter's 429 locks the form`() = runTest {
        api.twoFactorError = httpError(429, "RATE_LIMITED")
        val vm = viewModel(this)

        vm.onCodeChange("111111")
        vm.submit()

        val state = vm.state.value
        assertThat(state.lockedOut).isTrue()
        assertThat(state.canSubmit).isFalse()
        assertThat(state.message?.type).isEqualTo(UsMessageType.Warning)
        assertThat(state.message?.text).contains("Too many attempts")
    }

    @Test
    fun `the six-digit field only takes digits and the recovery field takes a code`() = runTest {
        val vm = viewModel(this)

        vm.onCodeChange("12a34b5678")
        assertThat(vm.state.value.code).isEqualTo("123456")
        assertThat(vm.state.value.canSubmit).isTrue()

        vm.toggleRecoveryCode()
        assertThat(vm.state.value.code).isEmpty()
        vm.onCodeChange(" abcd-efgh-ijkl ")
        assertThat(vm.state.value.code).isEqualTo("abcd-efgh-ijkl")
        assertThat(vm.state.value.canSubmit).isTrue()

        vm.submit()
        assertThat(api.twoFactorRequests.single().code).isEqualTo("abcd-efgh-ijkl")
    }

    @Test
    fun `a short code cannot be submitted`() = runTest {
        val vm = viewModel(this)

        vm.onCodeChange("123")
        vm.submit()

        assertThat(api.twoFactorRequests).isEmpty()
        assertThat(vm.state.value.canSubmit).isFalse()
    }

    @Test
    fun `without a pending token the screen asks for a fresh sign-in`() = runTest {
        val vm = TwoFactorViewModel(authRepository(api, tokenStore, this)).apply { start("", "") }

        vm.onCodeChange("123456")
        vm.submit()

        assertThat(vm.state.value.message?.text).contains("sign in again")
        assertThat(api.twoFactorRequests).isEmpty()
    }
}
