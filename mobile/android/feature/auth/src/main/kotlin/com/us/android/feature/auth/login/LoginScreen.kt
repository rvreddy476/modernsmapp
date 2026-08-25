package com.us.android.feature.auth.login

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
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
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme

@Composable
fun LoginRoute(
    onCreateAccount: () -> Unit,
    onNeedsVerification: (verificationToken: String, email: String) -> Unit,
    viewModel: LoginViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(state.needsVerificationToken) {
        state.needsVerificationToken?.let { token ->
            onNeedsVerification(token, state.identifier.trim())
        }
    }

    LoginScreen(
        state = state,
        onIdentifierChange = viewModel::onIdentifierChange,
        onPasswordChange = viewModel::onPasswordChange,
        onSubmit = viewModel::submit,
        onDismissMessage = viewModel::dismissMessage,
        onCreateAccount = onCreateAccount,
    )
}

@Composable
fun LoginScreen(
    state: LoginUiState,
    onIdentifierChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onDismissMessage: () -> Unit = {},
    onCreateAccount: () -> Unit = {},
) {
    UsScaffold {
        Box(modifier = Modifier.fillMaxSize()) {
            LoginBody(
                state = state,
                onIdentifierChange = onIdentifierChange,
                onPasswordChange = onPasswordChange,
                onSubmit = onSubmit,
                onCreateAccount = onCreateAccount,
            )
            UsMessageHost(message = state.message, onDismiss = onDismissMessage)
        }
    }
}

@Composable
private fun LoginBody(
    state: LoginUiState,
    onIdentifierChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onSubmit: () -> Unit,
    onCreateAccount: () -> Unit,
) {
    Column(
        modifier = Modifier.fillMaxSize(),
        verticalArrangement = Arrangement.Center,
    ) {
        Text(
            text = "US",
            style = MaterialTheme.typography.headlineMedium,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = "Unified Services",
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textMuted,
        )
        Spacer(Modifier.height(UsTheme.spacing.xxxxl))

        UsTextField(
            value = state.identifier,
            onValueChange = onIdentifierChange,
            label = "Email",
            placeholder = "you@example.com",
            errorText = state.identifierError,
            keyboardType = KeyboardType.Email,
            enabled = !state.isSubmitting,
        )
        Spacer(Modifier.height(UsTheme.spacing.xxl))
        UsTextField(
            value = state.password,
            onValueChange = onPasswordChange,
            label = "Password",
            errorText = state.passwordError,
            isPassword = true,
            enabled = !state.isSubmitting,
        )

        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        UsButton(
            text = "Sign in",
            onClick = onSubmit,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.canSubmit,
            loading = state.isSubmitting,
        )
        Spacer(Modifier.height(UsTheme.spacing.l))
        UsSecondaryButton(
            text = "Create an account",
            onClick = onCreateAccount,
            modifier = Modifier.fillMaxWidth(),
            enabled = !state.isSubmitting,
        )
    }
}

@Preview(name = "Login", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun LoginPreview() {
    UsTheme {
        LoginScreen(
            state = LoginUiState(identifier = "raghu@example.com", password = "hunter2"),
            onIdentifierChange = {},
            onPasswordChange = {},
            onSubmit = {},
        )
    }
}
