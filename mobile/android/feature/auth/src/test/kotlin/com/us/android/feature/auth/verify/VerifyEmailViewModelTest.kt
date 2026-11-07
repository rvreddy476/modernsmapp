package com.us.android.feature.auth.verify

import com.google.common.truth.Truth.assertThat
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.auth.FakeAuthApi
import com.us.android.feature.auth.FakeTokenStore
import com.us.android.feature.auth.authRepository
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * L9 (audit 2026-10-29): `VerifyEmailRoute` calls `start` from a
 * `LaunchedEffect`, which runs again after a rotation or a return to the
 * screen while this ViewModel lives on. The resend countdown must carry on
 * where it was, once a second — not restart at 60 and tick twice as fast.
 */
class VerifyEmailViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private fun TestScope.viewModel() = VerifyEmailViewModel(authRepository(FakeAuthApi(), FakeTokenStore(), this))

    private fun TestScope.advanceSeconds(seconds: Long) {
        advanceTimeBy(seconds * 1_000)
        runCurrent()
    }

    @Test
    fun `starting again for the same token neither resets nor doubles the cooldown`() = runTest {
        val vm = viewModel()
        vm.start("vt-1", "raghu@example.com")
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(60)

        advanceSeconds(10)
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(50)

        // Rotation: the effect runs again.
        vm.start("vt-1", "raghu@example.com")
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(50)

        advanceSeconds(5)
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(45)

        advanceSeconds(40)
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(5)
        assertThat(vm.state.value.canResend).isFalse()

        advanceSeconds(5)
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(0)
        assertThat(vm.state.value.canResend).isTrue()
    }

    @Test
    fun `a new verification token starts one fresh cooldown, not a second one`() = runTest {
        val vm = viewModel()
        vm.start("vt-1", "raghu@example.com")
        advanceSeconds(30)

        vm.start("vt-2", "raghu@example.com")
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(60)

        advanceSeconds(10)
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(50)
        advanceSeconds(50)
        assertThat(vm.state.value.resendCooldownSeconds).isEqualTo(0)
    }
}
