package com.us.android.feature.auth.verify

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme

@Composable
fun VerifyEmailRoute(
    verificationToken: String,
    email: String,
    onVerified: () -> Unit,
    onBackToLogin: () -> Unit,
    viewModel: VerifyEmailViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(verificationToken) {
        viewModel.start(verificationToken, email)
    }

    // Verification issues NO session, so success routes to sign-in rather
    // than into the app.
    LaunchedEffect(state.verified) {
        if (state.verified) onVerified()
    }

    VerifyEmailScreen(
        state = state,
        onCodeChange = viewModel::onCodeChange,
        onSubmit = viewModel::submit,
        onResend = viewModel::resend,
        onDismissMessage = viewModel::dismissMessage,
        onBackToLogin = onBackToLogin,
    )
}

@Composable
@Suppress("LongParameterList")
fun VerifyEmailScreen(
    state: VerifyEmailUiState,
    onCodeChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onResend: () -> Unit,
    onDismissMessage: () -> Unit = {},
    onBackToLogin: () -> Unit = {},
) {
    UsScaffold {
        // This screen discards the PaddingValues UsScaffold hands it, so the
        // scaffold's ime inset never reaches the content — the keyboard is
        // applied here instead. Nothing was consumed, so this is the full
        // inset, not a remainder.
        Box(modifier = Modifier.fillMaxSize().imePadding()) {
            VerifyBody(
                state = state,
                onCodeChange = onCodeChange,
                onSubmit = onSubmit,
                onResend = onResend,
                onBackToLogin = onBackToLogin,
            )
            UsMessageHost(message = state.message, onDismiss = onDismissMessage)
        }
    }
}

@Composable
private fun VerifyBody(
    state: VerifyEmailUiState,
    onCodeChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onResend: () -> Unit,
    onBackToLogin: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = "Check your email",
            style = MaterialTheme.typography.headlineMedium,
            color = UsTheme.extended.textPrimary,
        )
        Spacer(Modifier.height(UsTheme.spacing.m))
        Text(
            text = if (state.email.isNotBlank()) {
                "We sent a 6-digit code to ${state.email}. It expires in 5 minutes."
            } else {
                "Enter the 6-digit code we sent you. It expires in 5 minutes."
            },
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textMuted,
        )

        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        UsOtpField(
            value = state.code,
            onValueChange = onCodeChange,
            enabled = !state.isSubmitting,
            errorText = state.codeError,
            // Submitting on the last digit saves a deliberate tap; the button
            // stays for anyone who pastes or uses a keyboard.
            onFilled = { onSubmit() },
        )

        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        UsButton(
            text = "Verify email",
            onClick = onSubmit,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.canSubmit,
            loading = state.isSubmitting,
        )

        Spacer(Modifier.height(UsTheme.spacing.l))
        UsSecondaryButton(
            text = when {
                state.isResending -> "Sending…"
                state.resendCooldownSeconds > 0 ->
                    "Resend code in ${state.resendCooldownSeconds}s"
                else -> "Resend code"
            },
            onClick = onResend,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.canResend,
        )

        Spacer(Modifier.height(UsTheme.spacing.xxl))
        UsSecondaryButton(
            text = "Back to sign in",
            onClick = onBackToLogin,
            modifier = Modifier.fillMaxWidth(),
            enabled = !state.isSubmitting,
        )
    }
}

@Preview(name = "Verify — empty", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun VerifyPreview() {
    UsTheme {
        VerifyEmailScreen(
            state = VerifyEmailUiState(
                email = "raghu@example.com",
                resendCooldownSeconds = 47,
            ),
            onCodeChange = {},
            onSubmit = {},
            onResend = {},
        )
    }
}

@Preview(name = "Verify — bad code", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun VerifyErrorPreview() {
    UsTheme {
        VerifyEmailScreen(
            state = VerifyEmailUiState(
                email = "raghu@example.com",
                code = "",
                codeError = "That code didn't work. It may have expired — " +
                    "codes last 5 minutes. Request a new one if needed.",
            ),
            onCodeChange = {},
            onSubmit = {},
            onResend = {},
        )
    }
}
