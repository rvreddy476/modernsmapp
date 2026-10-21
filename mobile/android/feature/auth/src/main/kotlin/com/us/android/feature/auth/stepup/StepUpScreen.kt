package com.us.android.feature.auth.stepup

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
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.auth.AuthRepository
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.auth.ui.AuthHeader

@Composable
fun StepUpRoute(
    pendingToken: String,
    methods: List<String>,
    onBackToLogin: () -> Unit,
    viewModel: StepUpViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(pendingToken) {
        viewModel.start(pendingToken, methods)
    }
    // An expired pending session cannot be revived by any code: back to
    // sign-in, with the message host having said why.
    LaunchedEffect(state.expired) {
        if (state.expired && state.message == null) onBackToLogin()
    }

    StepUpScreen(
        state = state,
        onCodeChange = viewModel::onCodeChange,
        onSubmit = viewModel::submit,
        onSwitchMethod = viewModel::switchMethod,
        onDismissMessage = viewModel::dismissMessage,
        onBackToLogin = onBackToLogin,
    )
}

@Composable
@Suppress("LongParameterList")
fun StepUpScreen(
    state: StepUpUiState,
    onCodeChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onSwitchMethod: () -> Unit,
    onDismissMessage: () -> Unit = {},
    onBackToLogin: () -> Unit = {},
) {
    UsScaffold {
        Box(modifier = Modifier.fillMaxSize().imePadding()) {
            StepUpBody(
                state = state,
                onCodeChange = onCodeChange,
                onSubmit = onSubmit,
                onSwitchMethod = onSwitchMethod,
                onBackToLogin = onBackToLogin,
            )
            UsMessageHost(message = state.message, onDismiss = onDismissMessage)
        }
    }
}

@Composable
private fun StepUpBody(
    state: StepUpUiState,
    onCodeChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onSwitchMethod: () -> Unit,
    onBackToLogin: () -> Unit,
) {
    val inputsEnabled = !state.isSubmitting && !state.lockedOut && !state.signedIn && !state.expired
    val isEmail = state.method == AuthRepository.STEP_UP_METHOD_EMAIL
    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()),
        verticalArrangement = Arrangement.Center,
    ) {
        AuthHeader(
            icon = UsIcons.ShieldAlert,
            eyebrow = "Unfamiliar sign-in",
            title = "Confirm it's you",
            body = "This sign-in is from a new device or network. " + when {
                isEmail -> "Enter the 6-digit code we just emailed to your registered address."
                else -> "Enter the 6-digit code from your authenticator app."
            },
            tint = UsTheme.extended.statusWarning,
        )

        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        UsOtpField(
            value = state.code,
            onValueChange = onCodeChange,
            enabled = inputsEnabled,
            errorText = state.codeError,
            onFilled = { onSubmit() },
        )

        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        UsButton(
            text = "Verify",
            onClick = onSubmit,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.canSubmit,
            loading = state.isSubmitting,
        )

        if (state.canSwitchMethod) {
            Spacer(Modifier.height(UsTheme.spacing.s))
            TextButton(onClick = onSwitchMethod, modifier = Modifier.fillMaxWidth()) {
                Text(
                    text = if (isEmail) "Use authenticator app instead" else "Use email code instead",
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

        Spacer(Modifier.height(UsTheme.spacing.xxl))
        Text(
            text = "If this wasn't you, change your password and review recent activity in Settings › Account and security.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

@Preview(name = "Step-up — email", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun StepUpPreview() {
    UsTheme {
        StepUpScreen(
            state = StepUpUiState(
                methods = listOf("email_otp", "totp"),
                method = "email_otp",
                code = "123",
            ),
            onCodeChange = {},
            onSubmit = {},
            onSwitchMethod = {},
        )
    }
}
