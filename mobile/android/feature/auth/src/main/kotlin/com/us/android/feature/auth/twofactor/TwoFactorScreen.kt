package com.us.android.feature.auth.twofactor

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
import androidx.compose.runtime.remember
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.auth.R
import com.us.android.feature.auth.ui.AuthHeader

@Composable
fun TwoFactorRoute(
    userId: String,
    pendingToken: String,
    onBackToLogin: () -> Unit,
    /** The login response's `two_factor_method`; empty (an older server) reads as an authenticator app. */
    method: String = "",
    emailMasked: String = "",
    viewModel: TwoFactorViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(userId, pendingToken, method) {
        viewModel.start(userId, pendingToken, method, emailMasked)
    }

    TwoFactorScreen(
        state = state,
        onCodeChange = viewModel::onCodeChange,
        onSubmit = viewModel::submit,
        onToggleRecoveryCode = viewModel::toggleRecoveryCode,
        onDismissMessage = viewModel::dismissMessage,
        onBackToLogin = onBackToLogin,
        onResend = viewModel::resend,
    )
}

@Composable
@Suppress("LongParameterList")
fun TwoFactorScreen(
    state: TwoFactorUiState,
    onCodeChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onToggleRecoveryCode: () -> Unit,
    onDismissMessage: () -> Unit = {},
    onBackToLogin: () -> Unit = {},
    onResend: () -> Unit = {},
) {
    // An emailed-code notice that is not pinned under the field goes to the
    // message host, resolved from resources here.
    val notice = state.emailNotice?.takeUnless { it.inline }
    val noticeText = notice?.let { stringResource(it.text) }
    // Remembered: UsMessage stamps an id at construction, and a fresh one on
    // every recomposition would restart the host's timer.
    val noticeMessage = remember(notice, noticeText) {
        if (notice != null && noticeText != null) UsMessage(noticeText, notice.type) else null
    }
    val shown = state.message ?: noticeMessage
    UsScaffold {
        // Same inset handling as VerifyEmailScreen: the scaffold's padding is
        // not applied, so the keyboard inset is taken here.
        Box(modifier = Modifier.fillMaxSize().imePadding()) {
            TwoFactorBody(
                state = state,
                onCodeChange = onCodeChange,
                onSubmit = onSubmit,
                onToggleRecoveryCode = onToggleRecoveryCode,
                onBackToLogin = onBackToLogin,
                onResend = onResend,
            )
            UsMessageHost(message = shown, onDismiss = onDismissMessage)
        }
    }
}

@Composable
@Suppress("LongParameterList")
private fun TwoFactorBody(
    state: TwoFactorUiState,
    onCodeChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onToggleRecoveryCode: () -> Unit,
    onBackToLogin: () -> Unit,
    onResend: () -> Unit,
) {
    val inputsEnabled = !state.isSubmitting && !state.lockedOut && !state.signedIn
    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.Center,
    ) {
        AuthHeader(
            icon = UsIcons.ShieldCheck,
            eyebrow = "Verification required",
            title = "Two-step verification",
            body = when {
                state.isEmailCode && state.emailMasked.isNotBlank() ->
                    stringResource(R.string.two_factor_email_body, state.emailMasked)
                state.isEmailCode -> stringResource(R.string.two_factor_email_body_no_address)
                state.useRecoveryCode ->
                    "Enter one of the recovery codes you saved when you turned on two-step verification."
                else -> "Enter the 6-digit code from your authenticator app."
            },
        )

        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        CodeEntry(state, inputsEnabled, onCodeChange, onSubmit)

        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        UsButton(
            text = "Verify",
            onClick = onSubmit,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.canSubmit,
            loading = state.isSubmitting,
        )

        Spacer(Modifier.height(UsTheme.spacing.s))
        if (state.isEmailCode) {
            ResendButton(state, onResend)
        } else {
            TextButton(onClick = onToggleRecoveryCode, enabled = inputsEnabled, modifier = Modifier.fillMaxWidth()) {
                Text(
                    text = if (state.useRecoveryCode) "Use authenticator code instead" else "Use a recovery code",
                    style = MaterialTheme.typography.labelLarge,
                )
            }
        }

        Spacer(Modifier.height(UsTheme.spacing.xl))
        UsSecondaryButton(
            text = "Back to sign in",
            onClick = onBackToLogin,
            modifier = Modifier.fillMaxWidth(),
            enabled = !state.isSubmitting,
        )
    }
}

@Composable
private fun CodeEntry(
    state: TwoFactorUiState,
    inputsEnabled: Boolean,
    onCodeChange: (String) -> Unit,
    onSubmit: () -> Unit,
) {
    if (state.useRecoveryCode) {
        UsTextField(
            value = state.code,
            onValueChange = onCodeChange,
            label = "Recovery code",
            placeholder = "xxxx-xxxx-xxxx",
            errorText = state.codeError,
            enabled = inputsEnabled,
            keyboardType = KeyboardType.Ascii,
        )
    } else {
        val inlineNotice = state.emailNotice?.takeIf { it.inline }?.let { stringResource(it.text) }
        UsOtpField(
            value = state.code,
            onValueChange = onCodeChange,
            enabled = inputsEnabled,
            errorText = state.codeError ?: inlineNotice,
            onFilled = { onSubmit() },
        )
    }
}

/** Emailed codes only: a countdown while the server's 60-second cooldown runs, then Resend. */
@Composable
private fun ResendButton(state: TwoFactorUiState, onResend: () -> Unit) {
    TextButton(onClick = onResend, enabled = state.canResend, modifier = Modifier.fillMaxWidth()) {
        Text(
            text = if (state.resendInSeconds > 0) {
                stringResource(R.string.two_factor_email_resend_in, state.resendInSeconds)
            } else {
                stringResource(R.string.two_factor_email_resend)
            },
            style = MaterialTheme.typography.labelLarge,
        )
    }
}

@Preview(name = "Two-step — code", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun TwoFactorPreview() {
    UsTheme {
        TwoFactorScreen(
            state = TwoFactorUiState(code = "12"),
            onCodeChange = {},
            onSubmit = {},
            onToggleRecoveryCode = {},
        )
    }
}

@Preview(name = "Two-step — emailed code", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun TwoFactorEmailPreview() {
    UsTheme {
        TwoFactorScreen(
            state = TwoFactorUiState(isEmailCode = true, emailMasked = "r***@gmail.com", resendInSeconds = 42),
            onCodeChange = {},
            onSubmit = {},
            onToggleRecoveryCode = {},
        )
    }
}

@Preview(name = "Two-step — recovery, locked", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun TwoFactorLockedPreview() {
    UsTheme {
        TwoFactorScreen(
            state = TwoFactorUiState(useRecoveryCode = true, lockedOut = true),
            onCodeChange = {},
            onSubmit = {},
            onToggleRecoveryCode = {},
        )
    }
}
