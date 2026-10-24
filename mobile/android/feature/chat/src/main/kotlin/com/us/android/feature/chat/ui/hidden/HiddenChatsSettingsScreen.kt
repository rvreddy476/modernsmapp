package com.us.android.feature.chat.ui.hidden

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.hidden.CHAT_PASSWORD_MAX
import com.us.android.core.chat.hidden.HIDDEN_CHATS_COPY
import com.us.android.core.chat.hidden.SECRET_KEY_MAX
import com.us.android.core.chat.hidden.STRICT_MODE_WARNING
import com.us.android.core.common.window.SecureWindow
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsLinkRow
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSwitchRow

/**
 * Settings › Privacy › Hidden chats: set up, change, strict mode, and
 * "forgot" (reset after signing in again or a 2-step code).
 *
 * Every secret field is plain `remember` state: never `rememberSaveable`, so
 * no secret reaches a saved-state Bundle; the window is FLAG_SECURE while the
 * page is up, so none reaches a screenshot either.
 */
@Composable
@Suppress("LongMethod")
fun HiddenChatsSettingsScreen(
    onBack: () -> Unit,
    /** True when the chat that asked ("Hide chat" before setup) is hidden now. */
    onDone: (hidAChat: Boolean) -> Unit,
    viewModel: HiddenChatsSettingsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    androidx.compose.runtime.DisposableEffect(context) {
        val hold = (context as? android.app.Activity)?.window?.let(SecureWindow::hold)
        onDispose { hold?.release() }
    }
    LaunchedEffect(state.doneHiding) { if (state.doneHiding) onDone(true) }
    UsScaffold(
        topBar = { UsTopBar(title = "Hidden chats", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when {
            state.loading -> UsLoadingState(Modifier.padding(padding), "Loading hidden chats")
            state.loadFailed -> UsErrorState(
                state.error ?: "Hidden chats aren't available right now.",
                Modifier.padding(padding),
                onRetry = viewModel::load,
            )
            else -> Column(
                modifier = Modifier
                    .padding(padding)
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(horizontal = UsTheme.spacing.pageHorizontal)
                    .testTag("hidden-settings"),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                Text(
                    HIDDEN_CHATS_COPY,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                    modifier = Modifier.padding(top = UsTheme.spacing.m).testTag("hidden-copy"),
                )
                Messages(state)
                when {
                    state.resetStage == ResetStage.Verify -> VerifySection(state, viewModel)
                    state.resetStage == ResetStage.Secrets -> SecretsSection(
                        title = "New secret key and chat password",
                        submit = "Reset",
                        state = state,
                        strictDefault = state.settings?.strict == true,
                        onSubmit = viewModel::reset,
                        onCancel = viewModel::cancelReset,
                    )
                    state.settings?.configured == true -> ConfiguredSections(state, viewModel)
                    else -> SecretsSection(
                        title = "Set up hidden chats",
                        submit = if (viewModel.hidingAChat) "Save and hide" else "Save",
                        state = state,
                        strictDefault = false,
                        onSubmit = viewModel::setUp,
                        onCancel = null,
                    )
                }
            }
        }
    }
}

@Composable
private fun Messages(state: HiddenSettingsUiState) {
    state.error?.let {
        Text(it, color = MaterialTheme.colorScheme.error, modifier = Modifier.testTag("hidden-settings-error"))
    }
    state.notice?.let {
        Text(it, color = UsTheme.extended.statusSuccess, modifier = Modifier.testTag("hidden-settings-notice"))
    }
}

/** Setup and reset: the key, the password twice, strict mode. */
@Composable
@Suppress("LongParameterList")
private fun SecretsSection(
    title: String,
    submit: String,
    state: HiddenSettingsUiState,
    strictDefault: Boolean,
    onSubmit: (key: String, password: String, confirm: String, strict: Boolean) -> Unit,
    onCancel: (() -> Unit)?,
) {
    var key by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var strict by remember { mutableStateOf(strictDefault) }
    var warnStrict by remember { mutableStateOf(false) }
    UsSettingsSection(title) {
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsTextField(
                value = key,
                onValueChange = { key = it.take(SECRET_KEY_MAX) },
                label = "Secret key",
                placeholder = "4 to 12 letters or numbers",
                isPassword = true,
                errorText = state.problems.secretKey,
                modifier = Modifier.fillMaxWidth().testTag("hidden-setup-key"),
            )
            Text(
                "Type your secret key into chat search to show your hidden chats.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            UsTextField(
                value = password,
                onValueChange = { password = it.take(CHAT_PASSWORD_MAX) },
                label = "Chat password",
                placeholder = "6 to 64 characters, not your secret key",
                isPassword = true,
                errorText = state.problems.chatPassword,
                modifier = Modifier.fillMaxWidth().testTag("hidden-setup-password"),
            )
            UsTextField(
                value = confirm,
                onValueChange = { confirm = it.take(CHAT_PASSWORD_MAX) },
                label = "Confirm chat password",
                isPassword = true,
                errorText = state.problems.confirmPassword,
                modifier = Modifier.fillMaxWidth().testTag("hidden-setup-confirm"),
            )
            UsSettingsSwitchRow(
                title = "Strict mode",
                checked = strict,
                onCheckedChange = { on -> if (on) warnStrict = true else strict = false },
                description = "Resetting a forgotten secret deletes your hidden chats instead of keeping them.",
                modifier = Modifier.testTag("hidden-setup-strict"),
            )
            UsButton(
                text = submit,
                onClick = {
                    onSubmit(key, password, confirm, strict)
                    password = ""
                    confirm = ""
                },
                loading = state.busy,
                enabled = key.isNotEmpty() && password.isNotEmpty(),
                modifier = Modifier.fillMaxWidth().testTag("hidden-setup-submit"),
            )
            onCancel?.let { UsSecondaryButton(text = "Cancel", onClick = it, modifier = Modifier.fillMaxWidth()) }
        }
    }
    if (warnStrict) {
        StrictWarning(onConfirm = { strict = true; warnStrict = false }, onDismiss = { warnStrict = false })
    }
}

@Composable
private fun StrictWarning(onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("Turn on strict mode?") },
        text = { Text(STRICT_MODE_WARNING, modifier = Modifier.testTag("hidden-strict-warning")) },
        confirmButton = { TextButton(onClick = onConfirm) { Text("Turn on", color = UsTheme.extended.statusDanger) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

@Composable
@Suppress("LongMethod")
private fun ConfiguredSections(state: HiddenSettingsUiState, viewModel: HiddenChatsSettingsViewModel) {
    var current by remember { mutableStateOf("") }
    var newKey by remember { mutableStateOf("") }
    var newPassword by remember { mutableStateOf("") }
    var confirm by remember { mutableStateOf("") }
    var strictTarget by remember { mutableStateOf<Boolean?>(null) }
    UsSettingsSection("Change secret key or chat password") {
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsTextField(
                value = current,
                onValueChange = { current = it.take(CHAT_PASSWORD_MAX) },
                label = "Current chat password",
                isPassword = true,
                modifier = Modifier.fillMaxWidth().testTag("hidden-change-current"),
            )
            UsTextField(
                value = newKey,
                onValueChange = { newKey = it.take(SECRET_KEY_MAX) },
                label = "New secret key (optional)",
                isPassword = true,
                errorText = state.problems.secretKey,
                modifier = Modifier.fillMaxWidth().testTag("hidden-change-key"),
            )
            UsTextField(
                value = newPassword,
                onValueChange = { newPassword = it.take(CHAT_PASSWORD_MAX) },
                label = "New chat password (optional)",
                isPassword = true,
                errorText = state.problems.chatPassword,
                modifier = Modifier.fillMaxWidth().testTag("hidden-change-password"),
            )
            if (newPassword.isNotEmpty()) {
                UsTextField(
                    value = confirm,
                    onValueChange = { confirm = it.take(CHAT_PASSWORD_MAX) },
                    label = "Confirm new chat password",
                    isPassword = true,
                    errorText = state.problems.confirmPassword,
                    modifier = Modifier.fillMaxWidth().testTag("hidden-change-confirm"),
                )
            }
            UsButton(
                text = "Save changes",
                onClick = {
                    viewModel.change(current, newKey, newPassword, confirm)
                    current = ""
                    newKey = ""
                    newPassword = ""
                    confirm = ""
                },
                loading = state.busy,
                enabled = current.isNotEmpty() && (newKey.isNotEmpty() || newPassword.isNotEmpty()),
                modifier = Modifier.fillMaxWidth().testTag("hidden-change-submit"),
            )
        }
    }
    UsSettingsSection("Strict mode") {
        UsSettingsSwitchRow(
            title = "Strict mode",
            checked = state.settings?.strict == true,
            onCheckedChange = { strictTarget = it },
            description = "Resetting a forgotten secret deletes your hidden chats instead of keeping them.",
            enabled = !state.busy,
            modifier = Modifier.testTag("hidden-strict"),
        )
    }
    UsSettingsSection("Forgot your secret key or chat password?") {
        UsSettingsLinkRow(
            title = "Reset",
            description = if (state.settings?.strict == true) {
                "Strict mode is on: resetting deletes your hidden chats for you."
            } else {
                "Your hidden chats stay hidden with the new secrets."
            },
            onClick = viewModel::startReset,
            modifier = Modifier.testTag("hidden-reset"),
        )
    }
    strictTarget?.let { target ->
        StrictPasswordDialog(
            turningOn = target,
            onConfirm = { password ->
                viewModel.setStrict(password, target)
                strictTarget = null
            },
            onDismiss = { strictTarget = null },
        )
    }
}

/** Strict mode changes need the current chat password; turning it on shows the warning first. */
@Composable
private fun StrictPasswordDialog(turningOn: Boolean, onConfirm: (String) -> Unit, onDismiss: () -> Unit) {
    var password by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (turningOn) "Turn on strict mode?" else "Turn off strict mode?") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                if (turningOn) Text(STRICT_MODE_WARNING, modifier = Modifier.testTag("hidden-strict-warning"))
                UsTextField(
                    value = password,
                    onValueChange = { password = it.take(CHAT_PASSWORD_MAX) },
                    label = "Current chat password",
                    isPassword = true,
                    modifier = Modifier.fillMaxWidth().testTag("hidden-strict-password"),
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { onConfirm(password) }, enabled = password.isNotEmpty()) {
                Text(if (turningOn) "Turn on" else "Turn off", color = if (turningOn) UsTheme.extended.statusDanger else UsTheme.extended.textPrimary)
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text("Cancel") } },
    )
}

/** Forgot, step one: prove it's you — a 2-step code, or a sign-in in the last 5 minutes. */
@Composable
private fun VerifySection(state: HiddenSettingsUiState, viewModel: HiddenChatsSettingsViewModel) {
    var code by remember { mutableStateOf("") }
    UsSettingsSection("Confirm it's you") {
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            Text(
                "Enter a code from your authenticator app. Without two-step verification, sign out and sign " +
                    "in again, then reset within 5 minutes.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            UsTextField(
                value = code,
                onValueChange = { code = it.filter { ch -> !ch.isWhitespace() }.take(OTP_MAX) },
                label = "2-step code",
                keyboardType = KeyboardType.Number,
                modifier = Modifier.fillMaxWidth().testTag("hidden-reset-code"),
            )
            UsButton(
                text = "Confirm",
                onClick = {
                    viewModel.verifyCode(code)
                    code = ""
                },
                loading = state.busy,
                enabled = code.isNotEmpty(),
                modifier = Modifier.fillMaxWidth().testTag("hidden-reset-verify"),
            )
            UsSecondaryButton(
                text = "I signed in again just now",
                onClick = viewModel::useFreshSignIn,
                modifier = Modifier.fillMaxWidth().testTag("hidden-reset-fresh"),
            )
            TextButton(onClick = viewModel::cancelReset) { Text("Cancel") }
        }
    }
}

private const val OTP_MAX = 16
