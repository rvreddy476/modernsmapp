package com.us.android.feature.auth.login

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsActivityWallpaper
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsWordmark
import com.us.android.core.designsystem.component.UsWordmarkSize
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Sign-in.
 *
 * [onNavigate] receives the pending second steps (two-step code, unusual
 * sign-in, Google signup completion). It is nullable so that a host app that
 * has not wired the security screens still compiles and still says what the
 * server asked for ([LoginViewModel.showNavigationFallback]) instead of
 * going quiet. Momentum wires it; the partner apps may add the same graph
 * with `signInSecurityScreens`.
 */
@Composable
fun LoginRoute(
    onCreateAccount: () -> Unit,
    onNeedsVerification: (verificationToken: String, email: String) -> Unit,
    onForgotPassword: ((email: String) -> Unit)? = null,
    onNavigate: ((LoginNavigation) -> Unit)? = null,
    viewModel: LoginViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val uriHandler = LocalUriHandler.current

    LaunchedEffect(state.needsVerificationToken) {
        state.needsVerificationToken?.let { token ->
            viewModel.consumeNavigation()
            onNeedsVerification(token, state.identifier.trim())
        }
    }

    LaunchedEffect(state.navigation) {
        val destination = state.navigation ?: return@LaunchedEffect
        if (onNavigate != null) {
            viewModel.consumeNavigation()
            onNavigate(destination)
        } else {
            viewModel.showNavigationFallback(destination)
        }
    }

    // The consent page opens in the system browser: Google refuses WebViews,
    // and the redirect comes back to MainActivity on the app's own scheme.
    LaunchedEffect(state.googleAuthUrl) {
        val url = state.googleAuthUrl ?: return@LaunchedEffect
        viewModel.onGoogleAuthUrlOpened()
        runCatching { uriHandler.openUri(url) }.onFailure { viewModel.cancelGoogleSignIn() }
    }
    // Back from the browser with nothing in hand: release the button.
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
        viewModel.cancelGoogleSignIn()
    }

    LoginScreen(
        state = state,
        onIdentifierChange = viewModel::onIdentifierChange,
        onPasswordChange = viewModel::onPasswordChange,
        onSubmit = viewModel::submit,
        onDismissMessage = viewModel::dismissMessage,
        onCreateAccount = onCreateAccount,
        onForgotPassword = onForgotPassword?.let { forgot -> { forgot(state.identifier.trim()) } },
        onGoogleSignIn = viewModel::signInWithGoogle,
    )
}

@Composable
@Suppress("LongParameterList")
fun LoginScreen(
    state: LoginUiState,
    onIdentifierChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onDismissMessage: () -> Unit = {},
    onCreateAccount: () -> Unit = {},
    /** Null hides the link: a host app without the reset flow. */
    onForgotPassword: (() -> Unit)? = null,
    onGoogleSignIn: () -> Unit = {},
) {
    UsScaffold(contentMaxWidth = LOGIN_MAX_WIDTH, backdrop = { UsActivityWallpaper(Modifier.fillMaxSize()) }) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            LoginBody(
                state = state,
                onIdentifierChange = onIdentifierChange,
                onPasswordChange = onPasswordChange,
                onSubmit = onSubmit,
                onCreateAccount = onCreateAccount,
                onForgotPassword = onForgotPassword,
                onGoogleSignIn = onGoogleSignIn,
            )
            UsMessageHost(message = state.message, onDismiss = onDismissMessage)
        }
    }
}

@Composable
@Suppress("LongParameterList", "LongMethod")
private fun LoginBody(
    state: LoginUiState,
    onIdentifierChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onCreateAccount: () -> Unit,
    onForgotPassword: (() -> Unit)?,
    onGoogleSignIn: () -> Unit,
) {
    val busy = state.isSubmitting || state.isGoogleSubmitting
    BoxWithConstraints(modifier = Modifier.fillMaxSize()) {
        Column(
            modifier = Modifier.fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .heightIn(min = maxHeight)
                .padding(vertical = UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.Center,
        ) {
            UsWordmark(size = UsWordmarkSize.Hero)
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            Text("Welcome back", style = MaterialTheme.typography.headlineMedium)
            Spacer(Modifier.height(UsTheme.spacing.m))
            Text(
                "One place for your everyday.",
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textSecondary,
            )
            Spacer(Modifier.height(UsTheme.spacing.xxxxl))

            UsTextField(
                value = state.identifier,
                onValueChange = onIdentifierChange,
                label = "Email",
                placeholder = "you@example.com",
                errorText = state.identifierError,
                keyboardType = KeyboardType.Email,
                enabled = !busy,
            )
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            UsTextField(
                value = state.password,
                onValueChange = onPasswordChange,
                label = "Password",
                errorText = state.passwordError,
                isPassword = true,
                enabled = !busy,
            )
            if (onForgotPassword != null) {
                Spacer(Modifier.height(UsTheme.spacing.xs))
                Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    TextButton(onClick = onForgotPassword, enabled = !busy) {
                        Text("Forgot password?", style = MaterialTheme.typography.labelLarge)
                    }
                }
            }

            Spacer(Modifier.height(UsTheme.spacing.xxxxl))
            UsButton(
                text = "Sign in",
                onClick = onSubmit,
                modifier = Modifier.fillMaxWidth(),
                enabled = state.canSubmit,
                loading = state.isSubmitting,
            )

            if (state.googleSignInAvailable) {
                Spacer(Modifier.height(UsTheme.spacing.xl))
                OrDivider()
                Spacer(Modifier.height(UsTheme.spacing.xl))
                // The web's "Google" button, without its brand-coloured
                // mark: the design system draws icons in theme tokens only.
                UsSecondaryButton(
                    text = if (state.isGoogleSubmitting) "Waiting for Google…" else "Continue with Google",
                    onClick = onGoogleSignIn,
                    modifier = Modifier.fillMaxWidth(),
                    enabled = !busy,
                )
            }

            Spacer(Modifier.height(UsTheme.spacing.l))
            UsSecondaryButton(
                text = "Create an account",
                onClick = onCreateAccount,
                modifier = Modifier.fillMaxWidth(),
                enabled = !busy,
            )
        }
    }
}

@Composable
private fun OrDivider() {
    Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        HorizontalDivider(modifier = Modifier.weight(1f), color = UsTheme.extended.borderSubtle)
        Text(
            text = "or continue with",
            modifier = Modifier.padding(horizontal = UsTheme.spacing.m),
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
        )
        HorizontalDivider(modifier = Modifier.weight(1f), color = UsTheme.extended.borderSubtle)
    }
}

@Preview(name = "Login", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun LoginPreview() {
    UsTheme {
        LoginScreen(
            state = LoginUiState(
                identifier = "raghu@example.com",
                password = "hunter2",
                googleSignInAvailable = true,
            ),
            onIdentifierChange = {},
            onPasswordChange = {},
            onSubmit = {},
            onForgotPassword = {},
        )
    }
}

private val LOGIN_MAX_WIDTH = 480.dp
