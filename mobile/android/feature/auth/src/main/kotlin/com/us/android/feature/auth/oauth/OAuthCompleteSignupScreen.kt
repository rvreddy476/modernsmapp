package com.us.android.feature.auth.oauth

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
 * Finish signing up after Google. Success issues a session, so the shell
 * flips on the session state; nothing here navigates into the app.
 */
@Composable
fun OAuthCompleteSignupRoute(
    pendingToken: String,
    provider: String,
    email: String,
    onBackToLogin: () -> Unit,
    viewModel: OAuthCompleteSignupViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(pendingToken) {
        viewModel.start(pendingToken, provider, email)
    }

    OAuthCompleteSignupScreen(
        state = state,
        onPhoneChange = viewModel::onPhoneChange,
        onCodeChange = viewModel::onCodeChange,
        onSendCode = viewModel::sendCode,
        onVerify = viewModel::verify,
        onChangePhone = viewModel::changePhone,
        onDismissMessage = viewModel::dismissMessage,
        onBackToLogin = onBackToLogin,
    )
}

@Composable
@Suppress("LongParameterList")
fun OAuthCompleteSignupScreen(
    state: OAuthCompleteSignupUiState,
    onPhoneChange: (String) -> Unit,
    onCodeChange: (String) -> Unit,
    onSendCode: () -> Unit,
    onVerify: () -> Unit,
    onChangePhone: () -> Unit = {},
    onDismissMessage: () -> Unit = {},
    onBackToLogin: () -> Unit = {},
) {
    UsScaffold {
        Box(modifier = Modifier.fillMaxSize().imePadding()) {
            Column(
                modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()),
                verticalArrangement = Arrangement.Center,
            ) {
                AuthHeader(
                    icon = UsIcons.ShieldCheck,
                    eyebrow = "One more step",
                    title = "Finish signing up",
                    body = "${state.providerLabel} couldn't confirm that your email" +
                        (if (state.email.isNotBlank()) " (${state.email})" else "") +
                        " is verified. To keep your account safe, verify a phone number — we'll send a one-time code.",
                )
                Spacer(Modifier.height(UsTheme.spacing.xxxxl))
                when (state.step) {
                    OAuthSignupStep.Phone -> PhoneStep(state, onPhoneChange, onSendCode)
                    OAuthSignupStep.Code -> CodeStep(state, onCodeChange, onVerify, onChangePhone)
                }
                Spacer(Modifier.height(UsTheme.spacing.xl))
                UsSecondaryButton(
                    text = "Back to sign in",
                    onClick = onBackToLogin,
                    modifier = Modifier.fillMaxWidth(),
                    enabled = !state.isSubmitting,
                )
                Spacer(Modifier.height(UsTheme.spacing.xxl))
                Text(
                    text = "Your account uses ${state.providerLabel} for sign-in. The phone number is for " +
                        "security verification only — you can change it later from Settings.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
            UsMessageHost(message = state.message, onDismiss = onDismissMessage)
        }
    }
}

@Composable
private fun PhoneStep(
    state: OAuthCompleteSignupUiState,
    onPhoneChange: (String) -> Unit,
    onSendCode: () -> Unit,
) {
    UsTextField(
        value = state.phone,
        onValueChange = onPhoneChange,
        label = "Mobile number",
        placeholder = "10-digit number",
        errorText = state.phoneError,
        enabled = !state.isSubmitting && !state.lockedOut,
        keyboardType = KeyboardType.Phone,
    )
    Spacer(Modifier.height(UsTheme.spacing.xs))
    Text(
        text = "Country code +91. We'll text a 6-digit code.",
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
    )
    Spacer(Modifier.height(UsTheme.spacing.xxxxl))
    UsButton(
        text = "Send code",
        onClick = onSendCode,
        modifier = Modifier.fillMaxWidth(),
        enabled = state.canSendCode,
        loading = state.isSubmitting,
    )
}

@Composable
private fun CodeStep(
    state: OAuthCompleteSignupUiState,
    onCodeChange: (String) -> Unit,
    onVerify: () -> Unit,
    onChangePhone: () -> Unit,
) {
    val inputsEnabled = !state.isSubmitting && !state.lockedOut && !state.signedIn
    Text(
        text = "Enter the code sent to +91 •••••• ${state.phone.takeLast(PHONE_TAIL)}",
        style = MaterialTheme.typography.titleMedium,
        color = UsTheme.extended.textPrimary,
    )
    Spacer(Modifier.height(UsTheme.spacing.xl))
    UsOtpField(
        value = state.code,
        onValueChange = onCodeChange,
        enabled = inputsEnabled,
        errorText = state.codeError,
        onFilled = { onVerify() },
    )
    Spacer(Modifier.height(UsTheme.spacing.xxxxl))
    UsButton(
        text = "Verify and create account",
        onClick = onVerify,
        modifier = Modifier.fillMaxWidth(),
        enabled = state.canVerify,
        loading = state.isSubmitting,
    )
    Spacer(Modifier.height(UsTheme.spacing.s))
    TextButton(onClick = onChangePhone, enabled = !state.isSubmitting, modifier = Modifier.fillMaxWidth()) {
        Text("Use a different phone number", style = MaterialTheme.typography.labelLarge)
    }
}

private const val PHONE_TAIL = 4

@Preview(name = "OAuth signup — phone", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun PhonePreview() {
    UsTheme {
        OAuthCompleteSignupScreen(
            state = OAuthCompleteSignupUiState(providerLabel = "Google", email = "raghu@example.com", phone = "98765"),
            onPhoneChange = {},
            onCodeChange = {},
            onSendCode = {},
            onVerify = {},
        )
    }
}

@Preview(name = "OAuth signup — code", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun CodePreview() {
    UsTheme {
        OAuthCompleteSignupScreen(
            state = OAuthCompleteSignupUiState(
                step = OAuthSignupStep.Code,
                providerLabel = "Google",
                phone = "9876543210",
                code = "12",
            ),
            onPhoneChange = {},
            onCodeChange = {},
            onSendCode = {},
            onVerify = {},
        )
    }
}
