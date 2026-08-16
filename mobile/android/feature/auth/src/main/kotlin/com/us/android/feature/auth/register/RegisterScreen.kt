package com.us.android.feature.auth.register

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CheckboxDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.auth.RegistrationRules
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsChoice
import com.us.android.core.designsystem.component.UsChoiceRow
import com.us.android.core.designsystem.component.UsDatePickerField
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Labels are presentation; the stored tokens live on
 * [RegistrationRules.Gender] and must not drift when the wording changes.
 */
private val GENDER_OPTIONS = listOf(
    UsChoice(RegistrationRules.Gender.MALE, "Male"),
    UsChoice(RegistrationRules.Gender.FEMALE, "Female"),
    UsChoice(RegistrationRules.Gender.OTHER, "Others"),
)

@Composable
fun RegisterRoute(
    onNeedsVerification: (verificationToken: String, email: String) -> Unit,
    onBackToLogin: () -> Unit,
    viewModel: RegisterViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    // Registration issues no session; the account is pending until the emailed
    // code is confirmed. Hand the server-issued token straight to the verify
    // screen — it is the only handle on the new account.
    LaunchedEffect(state.verificationSent) {
        if (state.verificationSent) {
            onNeedsVerification(state.verificationToken, state.email)
        }
    }

    RegisterScreen(
        state = state,
        onEmailChange = viewModel::onEmailChange,
        onFirstNameChange = viewModel::onFirstNameChange,
        onLastNameChange = viewModel::onLastNameChange,
        onPasswordChange = viewModel::onPasswordChange,
        onDateOfBirthChange = viewModel::onDateOfBirthChange,
        onGenderChange = viewModel::onGenderChange,
        onTermsChange = viewModel::onTermsChange,
        onSubmit = viewModel::submit,
        onDismissMessage = viewModel::dismissMessage,
        onBackToLogin = onBackToLogin,
    )
}

@Composable
@Suppress("LongParameterList")
fun RegisterScreen(
    state: RegisterUiState,
    onEmailChange: (String) -> Unit,
    onFirstNameChange: (String) -> Unit,
    onLastNameChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onDateOfBirthChange: (String) -> Unit,
    onGenderChange: (RegistrationRules.Gender?) -> Unit,
    onTermsChange: (Boolean) -> Unit,
    onSubmit: () -> Unit,
    onDismissMessage: () -> Unit = {},
    onBackToLogin: () -> Unit = {},
) {
    UsScaffold {
        Box(modifier = Modifier.fillMaxSize()) {
            RegisterBody(
                state = state,
                onEmailChange = onEmailChange,
                onFirstNameChange = onFirstNameChange,
                onLastNameChange = onLastNameChange,
                onPasswordChange = onPasswordChange,
                onDateOfBirthChange = onDateOfBirthChange,
                onGenderChange = onGenderChange,
                onTermsChange = onTermsChange,
                onSubmit = onSubmit,
                onBackToLogin = onBackToLogin,
            )
            UsMessageHost(message = state.message, onDismiss = onDismissMessage)
        }
    }
}

@Composable
@Suppress("LongParameterList")
private fun RegisterBody(
    state: RegisterUiState,
    onEmailChange: (String) -> Unit,
    onFirstNameChange: (String) -> Unit,
    onLastNameChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onDateOfBirthChange: (String) -> Unit,
    onGenderChange: (RegistrationRules.Gender?) -> Unit,
    onTermsChange: (Boolean) -> Unit,
    onSubmit: () -> Unit,
    onBackToLogin: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(bottom = 32.dp),
    ) {
        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        Text(
            text = "Create account",
            style = MaterialTheme.typography.headlineMedium,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            text = "You'll verify your email before you can sign in.",
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textMuted,
        )
        Spacer(Modifier.height(UsTheme.spacing.xxxxl))

        RegisterFields(
            state = state,
            onEmailChange = onEmailChange,
            onFirstNameChange = onFirstNameChange,
            onLastNameChange = onLastNameChange,
            onPasswordChange = onPasswordChange,
            onDateOfBirthChange = onDateOfBirthChange,
            onGenderChange = onGenderChange,
        )

        Spacer(Modifier.height(UsTheme.spacing.xxl))
        TermsRow(
            accepted = state.acceptedTerms,
            error = state.termsError,
            enabled = !state.isSubmitting,
            onChange = onTermsChange,
        )

        Spacer(Modifier.height(UsTheme.spacing.xxxxl))
        UsButton(
            text = "Create account",
            onClick = onSubmit,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.canSubmit,
            loading = state.isSubmitting,
        )
        Spacer(Modifier.height(UsTheme.spacing.l))
        UsSecondaryButton(
            text = "I already have an account",
            onClick = onBackToLogin,
            modifier = Modifier.fillMaxWidth(),
            enabled = !state.isSubmitting,
        )
    }
}

@Composable
@Suppress("LongParameterList")
private fun RegisterFields(
    state: RegisterUiState,
    onEmailChange: (String) -> Unit,
    onFirstNameChange: (String) -> Unit,
    onLastNameChange: (String) -> Unit,
    onPasswordChange: (String) -> Unit,
    onDateOfBirthChange: (String) -> Unit,
    onGenderChange: (RegistrationRules.Gender?) -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl)) {
        UsTextField(
            value = state.email,
            onValueChange = onEmailChange,
            label = "Email",
            placeholder = "you@example.com",
            errorText = state.emailError,
            keyboardType = KeyboardType.Email,
            enabled = !state.isSubmitting,
        )

        // First and last name are SEPARATE inputs and stay separate on the
        // wire. The server validates and rejects each one independently, and
        // derives display_name itself as `firstName + " " + lastName`
        // (auth.go:424) — so there is no display-name field here and nothing
        // is concatenated on submit.
        UsTextField(
            value = state.firstName,
            onValueChange = onFirstNameChange,
            label = "First name",
            errorText = state.firstNameError,
            enabled = !state.isSubmitting,
        )
        UsTextField(
            value = state.lastName,
            onValueChange = onLastNameChange,
            label = "Last name",
            errorText = state.lastNameError,
            enabled = !state.isSubmitting,
        )

        UsTextField(
            value = state.password,
            onValueChange = onPasswordChange,
            label = "Password",
            errorText = state.passwordError,
            isPassword = true,
            enabled = !state.isSubmitting,
        )

        // The app's single date component. The calendar is bounded so an
        // underage date cannot be selected at all — the 18+ gate is enforced
        // by what is pickable, not by a message afterwards.
        UsDatePickerField(
            value = state.dateOfBirth,
            onValueChange = onDateOfBirthChange,
            label = "Date of birth",
            errorText = state.dateOfBirthError,
            enabled = !state.isSubmitting,
            maxDate = RegistrationRules.latestEligibleBirthDate(),
            initialDisplayedDate = RegistrationRules.latestEligibleBirthDate(),
        )

        UsChoiceRow(
            options = GENDER_OPTIONS,
            selected = state.gender,
            onSelect = onGenderChange,
            label = "Gender",
            enabled = !state.isSubmitting,
            errorText = state.genderError,
            // Required by product decision, so a chosen value cannot be
            // un-chosen — only swapped for another.
            allowDeselect = false,
        )
    }
}

@Composable
private fun TermsRow(
    accepted: Boolean,
    error: String?,
    enabled: Boolean,
    onChange: (Boolean) -> Unit,
) {
    Column {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Checkbox(
                checked = accepted,
                onCheckedChange = onChange,
                enabled = enabled,
                colors = CheckboxDefaults.colors(
                    checkedColor = MaterialTheme.colorScheme.primary,
                    uncheckedColor = UsTheme.extended.textMuted,
                ),
            )
            Text(
                text = "I accept the Terms of Service and Privacy Policy",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
        }
        if (error != null) {
            Text(
                text = error,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(start = 12.dp),
            )
        }
    }
}

@Preview(name = "Register", showBackground = true, backgroundColor = 0xFF000000, heightDp = 1200)
@Composable
private fun RegisterPreview() {
    UsTheme {
        RegisterScreen(
            state = RegisterUiState(
                email = "raghu@example.com",
                firstName = "Raghu",
                lastName = "Varan",
                password = "hunter2",
                dateOfBirth = "1990-04-12",
                gender = RegistrationRules.Gender.MALE,
                acceptedTerms = true,
            ),
            onEmailChange = {}, onFirstNameChange = {}, onLastNameChange = {},
            onPasswordChange = {}, onDateOfBirthChange = {}, onGenderChange = {},
            onTermsChange = {}, onSubmit = {},
        )
    }
}

@Preview(name = "Register — errors", showBackground = true, backgroundColor = 0xFF000000, heightDp = 1200)
@Composable
private fun RegisterErrorPreview() {
    UsTheme {
        RegisterScreen(
            state = RegisterUiState(
                email = "not-an-email",
                firstName = "Raghu2",
                lastName = "",
                dateOfBirth = "2015-01-01",
                emailError = "Enter a valid email address",
                firstNameError = "First name may contain letters, spaces, hyphens and apostrophes only",
                lastNameError = "Last name is required",
                dateOfBirthError = "You must be at least 18 to create an account",
                termsError = "You must accept the terms to continue",
            ),
            onEmailChange = {}, onFirstNameChange = {}, onLastNameChange = {},
            onPasswordChange = {}, onDateOfBirthChange = {}, onGenderChange = {},
            onTermsChange = {}, onSubmit = {},
        )
    }
}
