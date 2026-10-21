package com.us.android.feature.auth.login

import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.dto.AuthResponseDto
import com.us.android.core.auth.dto.AuthUserDto
import com.us.android.core.auth.oauth.GoogleSignIn
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.auth.FakeAuthApi
import com.us.android.feature.auth.FakeGoogleSignIn
import com.us.android.feature.auth.FakeTokenStore
import com.us.android.feature.auth.authRepository
import com.us.android.feature.auth.httpError
import com.us.android.feature.auth.sessionResponse
import com.us.android.feature.auth.testJson
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.JsonObject
import org.junit.Rule
import org.junit.Test

class LoginViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val api = FakeAuthApi()
    private val tokenStore = FakeTokenStore()

    private fun viewModel(
        scope: kotlinx.coroutines.CoroutineScope,
        google: FakeGoogleSignIn = FakeGoogleSignIn(),
    ) = LoginViewModel(authRepository(api, tokenStore, scope), google)

    private fun LoginViewModel.signIn() {
        onIdentifierChange("raghu@example.com")
        onPasswordChange("hunter2")
        submit()
    }

    @Test
    fun `requires_2fa routes to the two-step screen with the user id the verify route binds`() = runTest {
        api.loginResponse = AuthResponseDto(requires2fa = true, pendingToken = "pt-1", user = AuthUserDto(id = "u1"))
        val vm = viewModel(this)

        vm.signIn()

        assertThat(vm.state.value.navigation).isEqualTo(LoginNavigation.TwoFactor(userId = "u1", pendingToken = "pt-1"))
        assertThat(vm.state.value.message).isNull()
        assertThat(tokenStore.hasRefreshToken()).isFalse()

        vm.consumeNavigation()
        assertThat(vm.state.value.navigation).isNull()
    }

    @Test
    fun `requires_step_up routes to the unusual sign-in screen with the server's channels`() = runTest {
        api.loginResponse = AuthResponseDto(
            requiresStepUp = true,
            pendingToken = "pt-2",
            stepUpMethods = listOf("email_otp", "totp"),
        )
        val vm = viewModel(this)

        vm.signIn()

        assertThat(vm.state.value.navigation)
            .isEqualTo(LoginNavigation.StepUp(pendingToken = "pt-2", methods = listOf("email_otp", "totp")))
    }

    @Test
    fun `a host without the security screens still hears what the server asked for`() = runTest {
        val vm = viewModel(this)

        vm.showNavigationFallback(LoginNavigation.StepUp("pt", listOf("email_otp", "totp")))

        assertThat(vm.state.value.navigation).isNull()
        assertThat(vm.state.value.message?.text).contains("email or your authenticator app")
    }

    @Test
    fun `a full session routes nowhere - the shell flips on the session state`() = runTest {
        api.loginResponse = sessionResponse()
        val vm = viewModel(this)

        vm.signIn()

        assertThat(vm.state.value.navigation).isNull()
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
    }

    @Test
    fun `bad credentials stay a message`() = runTest {
        api.loginError = httpError(401, "AUTH_FAILED")
        val vm = viewModel(this)

        vm.signIn()

        assertThat(vm.state.value.navigation).isNull()
        assertThat(vm.state.value.message).isNotNull()
    }

    @Test
    fun `the Google button is hidden when the build has no client id`() = runTest {
        val vm = viewModel(this, FakeGoogleSignIn(isAvailable = false))

        assertThat(vm.state.value.googleSignInAvailable).isFalse()
        vm.signInWithGoogle()
        assertThat(vm.state.value.googleAuthUrl).isNull()
        assertThat(vm.state.value.isGoogleSubmitting).isFalse()
    }

    @Test
    fun `Google opens the consent URL and waits for the browser`() = runTest {
        val google = FakeGoogleSignIn()
        val vm = viewModel(this, google)

        vm.signInWithGoogle()

        assertThat(vm.state.value.googleAuthUrl).startsWith("https://accounts.example/auth")
        assertThat(vm.state.value.isGoogleSubmitting).isTrue()
        assertThat(vm.state.value.canSubmit).isFalse()
        vm.onGoogleAuthUrlOpened()
        assertThat(vm.state.value.googleAuthUrl).isNull()
        // Still waiting: a resume with nothing pending is a return from the browser.
        vm.cancelGoogleSignIn()
        assertThat(vm.state.value.isGoogleSubmitting).isFalse()
    }

    @Test
    fun `the browser's redirect is exchanged and a 200 signs in`() = runTest {
        api.oauthTokenResponse = testJson.encodeToJsonElement(AuthResponseDto.serializer(), sessionResponse()) as JsonObject
        val google = FakeGoogleSignIn()
        val vm = viewModel(this, google)
        vm.signInWithGoogle()
        vm.onGoogleAuthUrlOpened()

        google.offerRedirect("com.googleusercontent.apps.1234:/oauth2redirect?code=c&state=s1")

        assertThat(google.completed).containsExactly("com.googleusercontent.apps.1234:/oauth2redirect?code=c&state=s1")
        assertThat(api.oauthTokenRequests.single().first).isEqualTo("google")
        assertThat(api.oauthTokenRequests.single().second.token).isEqualTo("ya29.token")
        assertThat(tokenStore.readRefreshToken()).isEqualTo("rt")
        assertThat(vm.state.value.isGoogleSubmitting).isFalse()
        assertThat(vm.state.value.navigation).isNull()
    }

    @Test
    fun `a 202 pending signup routes to the phone step with the server's ticket`() = runTest {
        api.oauthTokenResponse = testJson.parseToJsonElement(
            """{"status":"pending_signup","pending_token":"op-1","provider":"google",""" +
                """"email":"raghu@example.com","name":"Raghu","next_step":"complete_signup"}""",
        ) as JsonObject
        val google = FakeGoogleSignIn()
        val vm = viewModel(this, google)
        vm.signInWithGoogle()

        google.offerRedirect("com.googleusercontent.apps.1234:/oauth2redirect?code=c&state=s1")

        assertThat(vm.state.value.navigation).isEqualTo(
            LoginNavigation.OAuthCompleteSignup(
                pendingToken = "op-1",
                provider = "google",
                email = "raghu@example.com",
                name = "Raghu",
            ),
        )
        assertThat(tokenStore.hasRefreshToken()).isFalse()
    }

    @Test
    fun `a Google 200 that requires 2fa goes to the two-step screen like a password sign-in`() = runTest {
        api.oauthTokenResponse = testJson.parseToJsonElement(
            """{"requires_2fa":true,"pending_token":"pt-9","user":{"id":"u1"}}""",
        ) as JsonObject
        val google = FakeGoogleSignIn()
        val vm = viewModel(this, google)
        vm.signInWithGoogle()

        google.offerRedirect("com.googleusercontent.apps.1234:/oauth2redirect?code=c&state=s1")

        assertThat(vm.state.value.navigation).isEqualTo(LoginNavigation.TwoFactor("u1", "pt-9"))
    }

    @Test
    fun `closing the consent page is silent, a refused exchange is a message`() = runTest {
        val google = FakeGoogleSignIn(
            completeResult = AppResult.Failure(AppError.AuthFailed(code = GoogleSignIn.CODE_CANCELLED)),
        )
        val vm = viewModel(this, google)
        vm.signInWithGoogle()
        google.offerRedirect("com.googleusercontent.apps.1234:/oauth2redirect?error=access_denied&state=s1")
        assertThat(vm.state.value.message).isNull()
        assertThat(vm.state.value.isGoogleSubmitting).isFalse()

        google.completeResult = AppResult.Failure(AppError.AuthFailed(code = GoogleSignIn.CODE_TOKEN_EXCHANGE))
        vm.signInWithGoogle()
        google.offerRedirect("com.googleusercontent.apps.1234:/oauth2redirect?code=c&state=s2")
        assertThat(vm.state.value.message?.text).contains("Google")
        assertThat(api.oauthTokenRequests).isEmpty()
    }

    @Test
    fun `the server refusing the Google token is a message, not a crash`() = runTest {
        api.oauthTokenError = httpError(401, "AUTH_FAILED", "OAuth authentication failed")
        val google = FakeGoogleSignIn()
        val vm = viewModel(this, google)
        vm.signInWithGoogle()

        google.offerRedirect("com.googleusercontent.apps.1234:/oauth2redirect?code=c&state=s1")

        assertThat(vm.state.value.message?.text).contains("couldn't sign you in with Google")
        assertThat(vm.state.value.isGoogleSubmitting).isFalse()
    }
}
