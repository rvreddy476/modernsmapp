package com.us.android.feature.auth.partner

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.AuthUserDto
import com.us.android.core.auth.dto.PartnerPhoneChallengeDto
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.auth.FakeAuthApi
import com.us.android.feature.auth.FakeTokenStore
import com.us.android.feature.auth.authRepository
import com.us.android.feature.auth.httpError
import com.us.android.feature.auth.login.LoginNavigation
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/** L7 (audit 2026-10-29): the phone-code sign-in's failure text and its second steps. */
class PartnerPhoneViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val api = FakeAuthApi()
    private val tokenStore = FakeTokenStore()

    private fun viewModel(scope: kotlinx.coroutines.CoroutineScope) =
        PartnerPhoneViewModel(authRepository(api, tokenStore, scope)).apply {
            edit {
                it.copy(
                    phone = "9876543210",
                    challenge = PartnerPhoneChallengeDto("challenge-1", "sms", 300, 30),
                    code = "123456",
                )
            }
        }

    @Test
    fun `a wrong code talks about the code, never an email and password`() = runTest {
        // partner_phone.go:71 — a wrong or expired code is 401 AUTH_FAILED.
        api.partnerVerifyError = httpError(401, "AUTH_FAILED", "Incorrect or expired code.")
        val vm = viewModel(this)

        vm.verify()

        val error = vm.state.value.error.orEmpty()
        assertThat(error).contains("code")
        assertThat(error).doesNotContain("email")
        assertThat(error).doesNotContain("password")
        assertThat(vm.state.value.busy).isFalse()
    }

    @Test
    fun `a code that needs 2fa next opens the two-step screen with what its verify binds`() = runTest {
        api.partnerVerifyResponse = AuthResponseDto(
            requires2fa = true,
            pendingToken = "pt-1",
            user = AuthUserDto(id = "u7"),
        )
        val vm = viewModel(this)

        vm.verify()

        assertThat(vm.state.value.pendingStep)
            .isEqualTo(LoginNavigation.TwoFactor(userId = "u7", pendingToken = "pt-1"))
        assertThat(vm.state.value.error).isNull()
        assertThat(tokenStore.hasRefreshToken()).isFalse()

        vm.leavePendingStep()
        assertThat(vm.state.value.pendingStep).isNull()
        assertThat(vm.state.value.challenge).isNull()
    }

    @Test
    fun `an unusual sign-in opens the step-up screen with the server's channels`() = runTest {
        api.partnerVerifyResponse = AuthResponseDto(
            requiresStepUp = true,
            pendingToken = "pt-2",
            stepUpMethods = listOf("email_otp"),
        )
        val vm = viewModel(this)

        vm.verify()

        assertThat(vm.state.value.pendingStep)
            .isEqualTo(LoginNavigation.StepUp(pendingToken = "pt-2", methods = listOf("email_otp")))
    }

    @Test
    fun `a right code with a session needs no second step`() = runTest {
        val vm = viewModel(this)

        vm.verify()

        assertThat(vm.state.value.pendingStep).isNull()
        assertThat(vm.state.value.error).isNull()
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
    }
}
