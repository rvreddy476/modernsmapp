package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.AccountSession
import com.us.android.core.profile.data.SecurityEvent
import com.us.android.core.profile.data.TrustedDevice
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsLinkRow
import com.us.android.core.ui.UsSettingsSection

@Composable
fun SecuritySettingsScreen(
    onBack: () -> Unit,
    onSignedOut: () -> Unit,
    viewModel: SecuritySettingsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect((state as? SecuritySettingsUiState.Content)?.signedOut) {
        if ((state as? SecuritySettingsUiState.Content)?.signedOut == true) onSignedOut()
    }
    UsScaffold(
        topBar = { UsTopBar("Account and security", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val current = state) {
            SecuritySettingsUiState.Loading -> UsLoadingState(
                Modifier.padding(padding),
                "Loading account security",
            )

            is SecuritySettingsUiState.Error -> UsErrorState(
                current.message,
                Modifier.padding(padding),
                onRetry = viewModel::load,
            )

            is SecuritySettingsUiState.Content -> SecurityContent(
                current,
                viewModel,
                Modifier.padding(padding),
            )
        }
    }
}

@Composable
private fun SecurityContent(
    state: SecuritySettingsUiState.Content,
    vm: SecuritySettingsViewModel,
    modifier: Modifier,
) {
    var code by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        AccountSection(state)
        TwoFactorSection(state, vm, code, { code = it }, password, { password = it })
        SessionSection(state, vm)
        TrustedDevicesSection(state, vm)
        SecurityEventsSection(state, vm)
        AccountDeletionNotice()
        state.message?.let { Text(it, color = MaterialTheme.colorScheme.primary) }
    }
}

@Composable
private fun AccountSection(state: SecuritySettingsUiState.Content) {
    UsSettingsSection("Account") {
        UsSettingsLinkRow(
            "Email",
            {},
            value = verified(state.account.email, state.account.emailVerified),
            enabled = false,
        )
        UsSettingsLinkRow(
            "Phone",
            {},
            value = verified(state.account.phone, state.account.phoneVerified),
            enabled = false,
        )
        UsSettingsLinkRow("Account status", {}, value = state.account.accountStatus, enabled = false)
        UsSettingsLinkRow("Age verification", {}, value = state.account.ageVerification, enabled = false)
    }
}

@Composable
private fun TwoFactorSection(
    state: SecuritySettingsUiState.Content,
    vm: SecuritySettingsViewModel,
    code: String,
    onCodeChange: (String) -> Unit,
    password: String,
    onPasswordChange: (String) -> Unit,
) {
    UsSettingsSection("Two-factor authentication") {
        if (!state.account.twoFactorEnabled) {
            UsButton(
                "Set up authenticator",
                vm::setupTwoFactor,
                Modifier.fillMaxWidth(),
                loading = state.busy,
            )
            state.twoFactorSetup?.let { setup ->
                Text("Authenticator secret", style = MaterialTheme.typography.labelLarge)
                Text(setup.secret, style = MaterialTheme.typography.bodyLarge)
                Text(
                    "Recovery codes — store these now. They are shown once.",
                    color = MaterialTheme.colorScheme.primary,
                )
                setup.recoveryCodes.forEach { Text(it) }
                AuthenticatorCodeField(code, onCodeChange)
                UsButton(
                    "Verify and enable",
                    { vm.verifyTwoFactor(code) },
                    Modifier.fillMaxWidth(),
                    enabled = code.length == AUTHENTICATOR_CODE_LENGTH,
                )
            }
        } else {
            Text("Authenticator protection is enabled.", color = UsTheme.extended.statusSuccess)
            UsTextField(
                value = password,
                onValueChange = onPasswordChange,
                label = "Current password",
                isPassword = true,
                keyboardType = KeyboardType.Password,
            )
            AuthenticatorCodeField(code, onCodeChange)
            UsSecondaryButton(
                "Disable two-factor",
                { vm.disableTwoFactor(password, code) },
                Modifier.fillMaxWidth(),
                enabled = password.isNotBlank() && code.length == AUTHENTICATOR_CODE_LENGTH,
            )
        }
    }
}

@Composable
private fun AuthenticatorCodeField(code: String, onCodeChange: (String) -> Unit) {
    UsTextField(
        code,
        { onCodeChange(it.filter(Char::isDigit).take(AUTHENTICATOR_CODE_LENGTH)) },
        "$AUTHENTICATOR_CODE_LENGTH-digit authenticator code",
    )
}

@Composable
private fun SessionSection(state: SecuritySettingsUiState.Content, vm: SecuritySettingsViewModel) {
    UsSettingsSection("Active sessions") {
        state.sessions.forEach { SessionRow(it, state.busy, vm::revokeSession) }
        UsSecondaryButton(
            "Log out on every device",
            vm::logoutEverywhere,
            Modifier.fillMaxWidth(),
            enabled = !state.busy,
        )
    }
}

@Composable
private fun TrustedDevicesSection(state: SecuritySettingsUiState.Content, vm: SecuritySettingsViewModel) {
    UsSettingsSection("Trusted devices") {
        if (state.trustedDevices.isEmpty()) Text("No trusted devices")
        state.trustedDevices.forEach { DeviceRow(it, state.busy, vm::removeTrustedDevice) }
    }
}

@Composable
private fun SecurityEventsSection(state: SecuritySettingsUiState.Content, vm: SecuritySettingsViewModel) {
    UsSettingsSection("Security activity") {
        if (state.events.isEmpty()) Text("No recent security alerts")
        state.events.forEach { EventRow(it, state.busy, vm::acknowledge) }
    }
}

@Composable
private fun AccountDeletionNotice() {
    UsSettingsSection("Your data and account") {
        Text(
            text = "Self-service account deletion is temporarily unavailable because the " +
                "cross-service erasure workflow is not complete. Contact privacy@atpost.com. " +
                "No data or session is changed by that request.",
            color = UsTheme.extended.textMuted,
        )
    }
}

@Composable
private fun SessionRow(value: AccountSession, busy: Boolean, revoke: (String) -> Unit) =
    UsSettingsLinkRow(
        value.platform.ifBlank { "Session" },
        { revoke(value.id) },
        description = listOf(value.ip, value.userAgent).filter(String::isNotBlank).joinToString(" · "),
        value = "Revoke",
        enabled = !busy,
    )

@Composable
private fun DeviceRow(value: TrustedDevice, busy: Boolean, remove: (String) -> Unit) =
    UsSettingsLinkRow(
        value.name.ifBlank { "Trusted device" },
        { remove(value.id) },
        description = value.lastUsedAt,
        value = "Remove",
        enabled = !busy,
    )

@Composable
private fun EventRow(value: SecurityEvent, busy: Boolean, acknowledge: (String) -> Unit) =
    UsSettingsLinkRow(
        value.type.replace('_', ' '),
        { acknowledge(value.id) },
        description = listOf(
            value.ip,
            value.countryCode,
            value.occurredAt,
        ).filter(String::isNotBlank).joinToString(" · "),
        value = if (value.acknowledged) "Reviewed" else "Review",
        enabled = !busy && !value.acknowledged,
    )

private fun verified(value: String, yes: Boolean) = when {
    value.isBlank() -> "Not set"
    yes -> "$value · verified"
    else -> "$value · unverified"
}

private const val AUTHENTICATOR_CODE_LENGTH = 6
