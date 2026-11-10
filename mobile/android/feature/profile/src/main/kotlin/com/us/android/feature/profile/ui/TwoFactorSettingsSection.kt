package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsOtpField
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.AccountSummary
import com.us.android.core.profile.data.TwoFactorMethod
import com.us.android.core.profile.data.TwoFactorPurpose
import com.us.android.core.ui.UsSettingsSection
import com.us.android.feature.profile.R

/**
 * Two-step verification by emailed code (founder, 10 Nov 2026). Off → Turn
 * on → password → the 6-digit code from the email → On. On → Turn off → the
 * same two steps. A legacy authenticator account turns it off with password
 * and app code. Secrets live in `remember` fields only, never saved state.
 */
@Composable
fun TwoFactorSettingsSection(
    account: AccountSummary,
    viewModel: TwoFactorSettingsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(account) { viewModel.bind(account) }
    UsSettingsSection(stringResource(R.string.two_factor_title)) {
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            when (val step = state.step) {
                TwoFactorStep.Idle -> IdleCard(state, viewModel)
                is TwoFactorStep.Password -> PasswordStep(state, step.purpose, viewModel)
                is TwoFactorStep.Code -> CodeStep(state, step, viewModel)
                TwoFactorStep.AuthenticatorOff -> AuthenticatorOffStep(state, viewModel)
            }
            state.problem?.let {
                Text(
                    problemText(it),
                    color = UsTheme.extended.statusDanger,
                    style = MaterialTheme.typography.bodySmall,
                    modifier = Modifier.testTag("two-factor-problem"),
                )
            }
            state.done?.let {
                Text(doneText(it), color = UsTheme.extended.statusSuccess, style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

@Composable
private fun IdleCard(state: TwoFactorSettingsUiState, vm: TwoFactorSettingsViewModel) {
    val address = state.emailMasked.ifBlank { maskEmail(state.accountEmail) }
    val status = when (state.method) {
        TwoFactorMethod.None -> stringResource(R.string.two_factor_off_status)
        TwoFactorMethod.Email -> if (address.isBlank()) {
            stringResource(R.string.two_factor_on_email_no_address)
        } else {
            stringResource(R.string.two_factor_on_email, address)
        }
        TwoFactorMethod.Authenticator -> stringResource(R.string.two_factor_on_authenticator)
        TwoFactorMethod.Other -> stringResource(R.string.two_factor_on_other)
    }
    Text(
        status,
        style = MaterialTheme.typography.bodyLarge,
        color = if (state.method == TwoFactorMethod.None) {
            UsTheme.extended.textMuted
        } else {
            UsTheme.extended.statusSuccess
        },
        modifier = Modifier.testTag("two-factor-status"),
    )
    when (state.method) {
        TwoFactorMethod.None -> {
            Text(
                stringResource(
                    if (state.emailVerified) R.string.two_factor_off_body else R.string.two_factor_needs_verified_email,
                ),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            UsButton(
                stringResource(R.string.two_factor_turn_on),
                vm::turnOn,
                Modifier.fillMaxWidth().testTag("two-factor-turn-on"),
                enabled = state.canTurnOn,
            )
        }
        TwoFactorMethod.Email, TwoFactorMethod.Authenticator -> UsSecondaryButton(
            stringResource(R.string.two_factor_turn_off),
            vm::turnOff,
            Modifier.fillMaxWidth().testTag("two-factor-turn-off"),
            enabled = state.canTurnOff,
        )
        // A method this build cannot run a flow for (sms, later): shown, not offered.
        TwoFactorMethod.Other -> Unit
    }
}

@Composable
private fun PasswordStep(state: TwoFactorSettingsUiState, purpose: TwoFactorPurpose, vm: TwoFactorSettingsViewModel) {
    var password by remember { mutableStateOf("") }
    Text(
        stringResource(
            if (purpose == TwoFactorPurpose.Enable) {
                R.string.two_factor_password_enable
            } else {
                R.string.two_factor_password_disable
            },
        ),
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
    )
    UsTextField(
        value = password,
        onValueChange = { password = it },
        label = stringResource(R.string.two_factor_password_label),
        isPassword = true,
        keyboardType = KeyboardType.Password,
        enabled = !state.busy,
        modifier = Modifier.testTag("two-factor-password"),
    )
    UsButton(
        stringResource(R.string.two_factor_continue),
        {
            vm.submitPassword(password)
            password = ""
        },
        Modifier.fillMaxWidth().testTag("two-factor-password-continue"),
        enabled = password.isNotEmpty() && !state.busy,
        loading = state.busy,
    )
    CancelButton(state, vm)
}

@Composable
private fun CodeStep(state: TwoFactorSettingsUiState, step: TwoFactorStep.Code, vm: TwoFactorSettingsViewModel) {
    var code by remember { mutableStateOf("") }
    Text(
        if (step.emailMasked.isBlank()) {
            stringResource(R.string.two_factor_code_sent_to_your_email)
        } else {
            stringResource(R.string.two_factor_code_sent_to, step.emailMasked)
        },
        style = MaterialTheme.typography.bodyMedium,
    )
    UsOtpField(
        value = code,
        onValueChange = { code = it.filter(Char::isDigit).take(TwoFactorSettingsUiState.CODE_LENGTH) },
        enabled = !state.busy,
        onFilled = {
            vm.submitCode(it)
            code = ""
        },
        modifier = Modifier.testTag("two-factor-code"),
    )
    UsButton(
        stringResource(
            if (step.purpose == TwoFactorPurpose.Enable) R.string.two_factor_turn_on else R.string.two_factor_turn_off,
        ),
        {
            vm.submitCode(code)
            code = ""
        },
        Modifier.fillMaxWidth().testTag("two-factor-code-submit"),
        enabled = code.length == TwoFactorSettingsUiState.CODE_LENGTH && !state.busy,
        loading = state.busy,
    )
    TextButton(onClick = vm::resend, enabled = state.canResend, modifier = Modifier.fillMaxWidth()) {
        Text(
            if (state.resendInSeconds > 0) {
                stringResource(R.string.two_factor_resend_in, state.resendInSeconds)
            } else {
                stringResource(R.string.two_factor_resend)
            },
            style = MaterialTheme.typography.labelLarge,
        )
    }
    CancelButton(state, vm)
}

@Composable
private fun AuthenticatorOffStep(state: TwoFactorSettingsUiState, vm: TwoFactorSettingsViewModel) {
    var password by remember { mutableStateOf("") }
    var code by remember { mutableStateOf("") }
    Text(
        stringResource(R.string.two_factor_authenticator_off_body),
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
    )
    UsTextField(
        value = password,
        onValueChange = { password = it },
        label = stringResource(R.string.two_factor_password_label),
        isPassword = true,
        keyboardType = KeyboardType.Password,
        enabled = !state.busy,
    )
    UsTextField(
        value = code,
        onValueChange = { code = it.filter(Char::isDigit).take(TwoFactorSettingsUiState.CODE_LENGTH) },
        label = stringResource(R.string.two_factor_authenticator_code_label),
        keyboardType = KeyboardType.Number,
        enabled = !state.busy,
    )
    UsSecondaryButton(
        stringResource(R.string.two_factor_turn_off),
        {
            vm.disableAuthenticator(password, code)
            code = ""
        },
        Modifier.fillMaxWidth(),
        enabled = password.isNotEmpty() && code.length == TwoFactorSettingsUiState.CODE_LENGTH && !state.busy,
    )
    CancelButton(state, vm)
}

@Composable
private fun CancelButton(state: TwoFactorSettingsUiState, vm: TwoFactorSettingsViewModel) {
    TextButton(onClick = vm::cancel, enabled = !state.busy, modifier = Modifier.fillMaxWidth()) {
        Text(stringResource(R.string.two_factor_cancel))
    }
}

@Composable
private fun problemText(problem: TwoFactorProblem): String = when (problem) {
    TwoFactorProblem.WrongPassword -> stringResource(R.string.two_factor_error_wrong_password)
    TwoFactorProblem.WrongCode -> stringResource(R.string.two_factor_error_wrong_code)
    TwoFactorProblem.CodeExpired -> stringResource(R.string.two_factor_error_code_expired)
    TwoFactorProblem.AuthenticatorRejected -> stringResource(R.string.two_factor_error_authenticator_rejected)
    TwoFactorProblem.EmailNotVerified -> stringResource(R.string.two_factor_error_email_not_verified)
    TwoFactorProblem.EmailUnavailable -> stringResource(R.string.two_factor_error_email_unavailable)
    TwoFactorProblem.StateChanged -> stringResource(R.string.two_factor_error_state_changed)
    TwoFactorProblem.Offline -> stringResource(R.string.two_factor_error_offline)
    TwoFactorProblem.Failed -> stringResource(R.string.two_factor_error_failed)
    is TwoFactorProblem.TooManyTries -> problem.retryAfterSeconds?.takeIf { it > 0 }?.let {
        stringResource(R.string.two_factor_error_too_many_wait, waitText(it))
    } ?: stringResource(R.string.two_factor_error_too_many)
}

@Composable
private fun waitText(seconds: Long): String =
    if (seconds < SECONDS_PER_MINUTE) {
        pluralStringResource(R.plurals.two_factor_seconds, seconds.toInt(), seconds.toInt())
    } else {
        val minutes = ((seconds + SECONDS_PER_MINUTE - 1) / SECONDS_PER_MINUTE).toInt()
        pluralStringResource(R.plurals.two_factor_minutes, minutes, minutes)
    }

@Composable
private fun doneText(done: TwoFactorDone): String = when (done) {
    TwoFactorDone.TurnedOn -> stringResource(R.string.two_factor_done_on)
    TwoFactorDone.TurnedOff -> stringResource(R.string.two_factor_done_off)
    TwoFactorDone.CodeResent -> stringResource(R.string.two_factor_done_resent)
}

/**
 * `raghu@gmail.com` → `r***@gmail.com`, the server's own masking, for the
 * status line before any code was sent this session. The address is the
 * signed-in person's own, already shown unmasked under Account.
 */
internal fun maskEmail(email: String): String {
    val at = email.indexOf('@')
    if (at <= 0) return ""
    return email.first() + "***" + email.substring(at)
}

private const val SECONDS_PER_MINUTE = 60L
