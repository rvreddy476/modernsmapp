package com.us.android.feature.auth.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.toRoute
import com.us.android.feature.auth.login.LoginNavigation
import com.us.android.feature.auth.oauth.OAuthCompleteSignupRoute
import com.us.android.feature.auth.reset.ResetPasswordRoute
import com.us.android.feature.auth.stepup.StepUpRoute
import com.us.android.feature.auth.twofactor.TwoFactorRoute
import kotlinx.serialization.Serializable

/*
 * The sign-in security graph (web parity, founder 21 Oct 2026).
 *
 * Login, Register and Verify email stay declared by each application module —
 * five apps register them with their own route objects. The four screens
 * below are new and shared, so they are declared ONCE here and any app adds
 * them with `signInSecurityScreens`; Momentum does, the partner apps may.
 *
 * Every argument is what the next request binds `required`. A pending token
 * is a short-lived, single-use credential for ONE attempt; carrying it in the
 * back stack is the same trade `VerifyEmailRoute` makes with its verification
 * token — the screen survives rotation and process death without a holder.
 */

/** The authenticator-code step of a sign-in that answered `requires_2fa`. */
@Serializable
data class TwoFactorCodeRoute(val userId: String, val pendingToken: String)

/**
 * The unusual-sign-in step. [methods] is the server's `step_up_methods`
 * joined with [StepUpVerifyRoute.METHODS_SEPARATOR] — a flat string, as the
 * web's `?methods=` query, rather than a list argument.
 */
@Serializable
data class StepUpVerifyRoute(val pendingToken: String, val methods: String) {
    val methodList: List<String> get() = methods.split(METHODS_SEPARATOR).filter { it.isNotBlank() }

    companion object {
        const val METHODS_SEPARATOR = ","
        fun of(pendingToken: String, methods: List<String>) =
            StepUpVerifyRoute(pendingToken, methods.joinToString(METHODS_SEPARATOR))
    }
}

/**
 * Forgot password (from sign-in, [fromSettings] false) and Change password
 * (from Settings › Account and security, [fromSettings] true, [identifier]
 * the account's email).
 */
@Serializable
data class PasswordResetRoute(val identifier: String = "", val fromSettings: Boolean = false)

/** The phone + code step after a Google sign-in that answered 202 `pending_signup`. */
@Serializable
data class OAuthSignupRoute(
    val pendingToken: String,
    val provider: String,
    val email: String = "",
    val name: String = "",
)

/**
 * Where the security screens go when they leave.
 *
 * Success never navigates: every screen that signs in stores the session and
 * the shell flips on the session state. [onBackToLogin] is the only exit
 * from the three sign-in steps; [onPasswordResetDone] is the Done step's
 * "Sign in", told whether the reset came from Settings — the session is
 * already cleared by then, so the host lands on Login the way sign-out does,
 * with the tab stack behind it gone.
 */
data class SignInSecurityDestinations(
    val onBackToLogin: () -> Unit,
    val onPasswordResetDone: (fromSettings: Boolean) -> Unit = { onBackToLogin() },
    val onPasswordResetCancelled: () -> Unit = onBackToLogin,
)

fun NavGraphBuilder.signInSecurityScreens(destinations: SignInSecurityDestinations) {
    composable<TwoFactorCodeRoute> { entry ->
        val route = entry.toRoute<TwoFactorCodeRoute>()
        TwoFactorRoute(
            userId = route.userId,
            pendingToken = route.pendingToken,
            onBackToLogin = destinations.onBackToLogin,
        )
    }
    composable<StepUpVerifyRoute> { entry ->
        val route = entry.toRoute<StepUpVerifyRoute>()
        StepUpRoute(
            pendingToken = route.pendingToken,
            methods = route.methodList,
            onBackToLogin = destinations.onBackToLogin,
        )
    }
    composable<PasswordResetRoute> { entry ->
        val route = entry.toRoute<PasswordResetRoute>()
        ResetPasswordRoute(
            identifier = route.identifier,
            fromSettings = route.fromSettings,
            onDone = { destinations.onPasswordResetDone(route.fromSettings) },
            onBack = destinations.onPasswordResetCancelled,
        )
    }
    composable<OAuthSignupRoute> { entry ->
        val route = entry.toRoute<OAuthSignupRoute>()
        OAuthCompleteSignupRoute(
            pendingToken = route.pendingToken,
            provider = route.provider,
            email = route.email,
            onBackToLogin = destinations.onBackToLogin,
        )
    }
}

/** The Login screen's pending outcome, as a push. One place maps the three. */
fun NavController.navigateToSignInStep(step: LoginNavigation) = when (step) {
    is LoginNavigation.TwoFactor -> navigate(TwoFactorCodeRoute(step.userId, step.pendingToken))
    is LoginNavigation.StepUp -> navigate(StepUpVerifyRoute.of(step.pendingToken, step.methods))
    is LoginNavigation.OAuthCompleteSignup ->
        navigate(OAuthSignupRoute(step.pendingToken, step.provider, step.email, step.name))
}

fun NavController.navigateToForgotPassword(email: String = "") =
    navigate(PasswordResetRoute(identifier = email, fromSettings = false))

fun NavController.navigateToChangePassword(email: String) =
    navigate(PasswordResetRoute(identifier = email, fromSettings = true))
