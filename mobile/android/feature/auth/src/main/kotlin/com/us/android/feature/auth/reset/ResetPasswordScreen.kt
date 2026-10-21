package com.us.android.feature.auth.reset

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.auth.PasswordRules
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.auth.ui.AuthHeader

/**
 * Forgot password (from sign-in) and change password (from Settings).
 *
 * [onDone] fires after "Sign in" on the Done step, once the view model has
 * ended any local session. From sign-in that is "back to sign in"; from
 * Settings the server has revoked every session and the repository has just
 * cleared this one, so the shell is already on its way to Login — the caller
 * only needs to leave this screen.
 */
@Composable
fun ResetPasswordRoute(
    identifier: String,
    fromSettings: Boolean,
    onDone: () -> Unit,
    onBack: () -> Unit,
    viewModel: ResetPasswordViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) {
        viewModel.start(identifier, fromSettings)
    }
    LaunchedEffect(state.sessionEnded) {
        if (state.sessionEnded) onDone()
    }

    ResetPasswordScreen(
        state = state,
        callbacks = ResetPasswordCallbacks(
            onIdentifierChange = viewModel::onIdentifierChange,
            onCodeChange = viewModel::onCodeChange,
            onNewPasswordChange = viewModel::onNewPasswordChange,
            onConfirmPasswordChange = viewModel::onConfirmPasswordChange,
            onRequestCode = viewModel::requestCode,
            onResend = viewModel::resend,
            onSubmitNewPassword = viewModel::submitNewPassword,
            onEditIdentifier = viewModel::editIdentifier,
            onDismissMessage = viewModel::dismissMessage,
            onDone = viewModel::finish,
            onBack = onBack,
        ),
    )
}

/** One bundle, so the screen and its previews do not carry eleven lambdas each. */
data class ResetPasswordCallbacks(
    val onIdentifierChange: (String) -> Unit = {},
    val onCodeChange: (String) -> Unit = {},
    val onNewPasswordChange: (String) -> Unit = {},
    val onConfirmPasswordChange: (String) -> Unit = {},
    val onRequestCode: () -> Unit = {},
    val onResend: () -> Unit = {},
    val onSubmitNewPassword: () -> Unit = {},
    val onEditIdentifier: () -> Unit = {},
    val onDismissMessage: () -> Unit = {},
    val onDone: () -> Unit = {},
    val onBack: () -> Unit = {},
)

@Composable
fun ResetPasswordScreen(
    state: ResetPasswordUiState,
    callbacks: ResetPasswordCallbacks,
) {
    UsScaffold {
        Box(modifier = Modifier.fillMaxSize().imePadding()) {
            Column(
                modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.Center,
            ) {
                when (state.step) {
                    ResetPasswordStep.Request -> RequestStep(state, callbacks)
                    ResetPasswordStep.Reset -> ResetStep(state, callbacks)
                    ResetPasswordStep.Done -> DoneStep(state, callbacks)
                }
            }
            UsMessageHost(message = state.message, onDismiss = callbacks.onDismissMessage)
        }
    }
}

@Composable
private fun RequestStep(state: ResetPasswordUiState, callbacks: ResetPasswordCallbacks) {
    AuthHeader(
        icon = UsIcons.Lock,
        eyebrow = if (state.fromSettings) "Change password" else "Forgot password",
        title = if (state.fromSettings) "Change your password" else "Reset your password",
        body = "Enter the email address on your account. If an account exists, we'll email a 6-digit code.",
    )
    Spacer(Modifier.height(UsTheme.spacing.xxxxl))
    UsTextField(
        value = state.identifier,
        onValueChange = callbacks.onIdentifierChange,
        label = "Email",
        placeholder = "you@example.com",
        errorText = state.identifierError,
        keyboardType = KeyboardType.Email,
        enabled = !state.isSubmitting && !state.fromSettings,
    )
    Spacer(Modifier.height(UsTheme.spacing.xxxxl))
    UsButton(
        text = "Send code",
        onClick = callbacks.onRequestCode,
        modifier = Modifier.fillMaxWidth(),
        enabled = state.canRequest,
        loading = state.isSubmitting,
    )
    Spacer(Modifier.height(UsTheme.spacing.l))
    UsSecondaryButton(
        text = if (state.fromSettings) "Cancel" else "Back to sign in",
        onClick = callbacks.onBack,
        modifier = Modifier.fillMaxWidth(),
        enabled = !state.isSubmitting,
    )
}

@Composable
private fun ResetStep(state: ResetPasswordUiState, callbacks: ResetPasswordCallbacks) {
    val inputsEnabled = !state.isSubmitting && !state.lockedOut
    AuthHeader(
        icon = UsIcons.Lock,
        eyebrow = "Check your email",
        title = "Enter the code and a new password",
        body = "If an account exists for ${state.identifier}, the 6-digit code is on its way. " +
            "Setting a new password signs you out everywhere.",
    )
    Spacer(Modifier.height(UsTheme.spacing.xxl))
    UsOtpField(
        value = state.code,
        onValueChange = callbacks.onCodeChange,
        enabled = inputsEnabled,
        errorText = state.codeError,
    )
    Spacer(Modifier.height(UsTheme.spacing.xxl))
    UsTextField(
        value = state.newPassword,
        onValueChange = callbacks.onNewPasswordChange,
        label = "New password",
        errorText = state.newPasswordError,
        isPassword = true,
        keyboardType = KeyboardType.Password,
        enabled = inputsEnabled,
    )
    Spacer(Modifier.height(UsTheme.spacing.xs))
    Text(
        text = PasswordRules.RULES_TEXT,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
    )
    Spacer(Modifier.height(UsTheme.spacing.xl))
    UsTextField(
        value = state.confirmPassword,
        onValueChange = callbacks.onConfirmPasswordChange,
        label = "Confirm new password",
        errorText = state.confirmPasswordError,
        isPassword = true,
        keyboardType = KeyboardType.Password,
        enabled = inputsEnabled,
    )
    Spacer(Modifier.height(UsTheme.spacing.xxxxl))
    UsButton(
        text = "Set new password",
        onClick = callbacks.onSubmitNewPassword,
        modifier = Modifier.fillMaxWidth(),
        enabled = state.canReset,
        loading = state.isSubmitting,
    )
    Spacer(Modifier.height(UsTheme.spacing.l))
    UsSecondaryButton(
        text = when {
            state.isResending -> "Sending…"
            state.resendCooldownSeconds > 0 -> "Resend code in ${state.resendCooldownSeconds}s"
            else -> "Resend code"
        },
        onClick = callbacks.onResend,
        modifier = Modifier.fillMaxWidth(),
        enabled = state.canResend,
    )
    if (!state.fromSettings) {
        Spacer(Modifier.height(UsTheme.spacing.s))
        TextButton(onClick = callbacks.onEditIdentifier, enabled = !state.isSubmitting, modifier = Modifier.fillMaxWidth()) {
            Text("Use a different email", style = MaterialTheme.typography.labelLarge)
        }
    }
    Spacer(Modifier.height(UsTheme.spacing.xl))
    UsSecondaryButton(
        text = if (state.fromSettings) "Cancel" else "Back to sign in",
        onClick = callbacks.onBack,
        modifier = Modifier.fillMaxWidth(),
        enabled = !state.isSubmitting,
    )
}

@Composable
private fun DoneStep(state: ResetPasswordUiState, callbacks: ResetPasswordCallbacks) {
    AuthHeader(
        icon = UsIcons.ShieldCheck,
        eyebrow = "Done",
        title = "Password updated",
        body = if (state.fromSettings) {
            "You've been signed out on every device, this one included. Sign in again with your new password."
        } else {
            "Every device has been signed out. Sign in with your new password."
        },
        tint = UsTheme.extended.statusSuccess,
    )
    Spacer(Modifier.height(UsTheme.spacing.xxxxl))
    UsButton(
        text = "Sign in",
        onClick = callbacks.onDone,
        modifier = Modifier.fillMaxWidth(),
        enabled = !state.finished,
        loading = state.finished && !state.sessionEnded,
    )
}

@Preview(name = "Reset — request", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun RequestPreview() {
    UsTheme {
        ResetPasswordScreen(
            state = ResetPasswordUiState(identifier = "raghu@example.com"),
            callbacks = ResetPasswordCallbacks(),
        )
    }
}

@Preview(name = "Reset — code and password", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun ResetPreview() {
    UsTheme {
        ResetPasswordScreen(
            state = ResetPasswordUiState(
                step = ResetPasswordStep.Reset,
                identifier = "raghu@example.com",
                code = "1234",
                newPassword = "short",
                newPasswordError = PasswordRules.text(PasswordRules.PasswordError.TooShort),
                resendCooldownSeconds = 41,
            ),
            callbacks = ResetPasswordCallbacks(),
        )
    }
}
