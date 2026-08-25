package com.us.android.feature.chat.ui

import android.app.Activity
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.fragment.app.FragmentActivity
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.lock.chatLockSecureWindowFlags
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Wraps every chat surface: while the LOCAL chat lock is engaged, the wrapped
 * content is replaced entirely — nothing inside it composes, so no message
 * text can reach the screen, the semantics tree or a screenshot of the gate.
 *
 * While the lock is ENABLED (locked or not), the surface also carries
 * FLAG_SECURE (P0-7): with a delayed lock interval the app can sit unlocked
 * in the background, and without the flag the recents/task-switcher snapshot
 * retains message text for exactly the user who asked for a lock.
 */
@Composable
fun ChatLockGate(
    viewModel: ChatLockViewModel = hiltViewModel(),
    content: @Composable () -> Unit,
) {
    val locked by viewModel.locked.collectAsStateWithLifecycle()
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    DisposableEffect(state.enabled, context) {
        val window = (context as? Activity)?.window
        val flags = chatLockSecureWindowFlags(state.enabled)
        if (flags != 0) window?.addFlags(flags)
        onDispose { if (flags != 0) window?.clearFlags(flags) }
    }
    if (locked) {
        ChatLockScreen(viewModel = viewModel)
    } else {
        content()
    }
}

/**
 * The lock screen: biometric/device-credential first (Keystore-backed via
 * BiometricPrompt), PIN as the fallback. The PIN travels from this field to
 * the local verifier and nowhere else.
 */
@Composable
@Suppress("LongMethod")
fun ChatLockScreen(viewModel: ChatLockViewModel) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var pin by rememberSaveable { mutableStateOf("") }
    val context = LocalContext.current

    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.xl),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            "Chat is locked",
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
        )
        Text(
            "Your messages stay hidden until you unlock.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        Spacer(Modifier.height(UsTheme.spacing.xl))

        if (state.lockoutSeconds > 0) {
            Text(
                "Too many attempts. Try again in ${state.lockoutSeconds}s.",
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.testTag("lock-throttled"),
            )
            Spacer(Modifier.height(UsTheme.spacing.m))
        }

        UsTextField(
            value = pin,
            onValueChange = { pin = it.filter(Char::isDigit).take(MAX_PIN_LENGTH) },
            label = "PIN",
            placeholder = "Enter your chat PIN",
            singleLine = true,
            isPassword = true,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("lock-pin"),
        )
        state.error?.let {
            Text(
                it,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        Spacer(Modifier.height(UsTheme.spacing.m))
        UsButton(
            text = "Unlock",
            onClick = {
                viewModel.unlockWithPin(pin)
                pin = ""
            },
            enabled = pin.length >= MIN_PIN_LENGTH && state.lockoutSeconds == 0L,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("lock-unlock"),
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        if (state.biometricAvailable) {
            UsSecondaryButton(
                text = "Use fingerprint or face",
                onClick = {
                    (context as? FragmentActivity)?.let(viewModel::promptDeviceAuth)
                },
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("lock-biometric"),
            )
        }
        TextButton(
            onClick = viewModel::startForgotFlow,
            modifier = Modifier.testTag("lock-forgot"),
        ) { Text("Forgot PIN?") }

        if (state.confirmingReset) {
            Text(
                "There is no way to recover a forgotten chat PIN. Resetting removes " +
                    "the lock and clears the chat messages saved on THIS device — " +
                    "your conversations stay on the server and reload after reset.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                TextButton(onClick = viewModel::cancelForgotFlow) { Text("Keep the lock") }
                TextButton(
                    onClick = viewModel::confirmReset,
                    modifier = Modifier.testTag("lock-reset-confirm"),
                ) {
                    Text("Reset and clear", color = MaterialTheme.colorScheme.error)
                }
            }
        }
    }
}

/** Chat lock settings: enable/disable, auto-lock interval, biometric toggle. */
@Composable
@Suppress("LongMethod")
fun ChatLockSettingsScreen(
    onBack: () -> Unit,
    viewModel: ChatLockViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var newPin by rememberSaveable { mutableStateOf("") }
    var disablePin by rememberSaveable { mutableStateOf("") }

    UsScaffold(
        topBar = { UsTopBar(title = "Chat lock", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        ) {
            Spacer(Modifier.height(UsTheme.spacing.m))
            if (!state.enabled) {
                Text(
                    "Add a PIN that locks your chats on this phone. It is separate from " +
                        "your account password and never leaves the device.",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                )
                Spacer(Modifier.height(UsTheme.spacing.m))
                UsTextField(
                    value = newPin,
                    onValueChange = { newPin = it.filter(Char::isDigit).take(MAX_PIN_LENGTH) },
                    label = "New PIN (at least 6 digits)",
                    singleLine = true,
                    isPassword = true,
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("lock-settings-pin"),
                )
                Spacer(Modifier.height(UsTheme.spacing.m))
                UsButton(
                    text = "Turn on chat lock",
                    onClick = {
                        viewModel.enable(newPin)
                        newPin = ""
                    },
                    enabled = newPin.length >= MIN_PIN_LENGTH,
                    modifier = Modifier
                        .fillMaxWidth()
                        .testTag("lock-settings-enable"),
                )
            } else {
                Text(
                    "Auto-lock",
                    style = MaterialTheme.typography.titleSmall,
                    color = UsTheme.extended.textPrimary,
                )
                state.intervals.forEach { option ->
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        RadioButton(
                            selected = option == state.interval,
                            onClick = { viewModel.setInterval(option) },
                        )
                        Text(option, color = UsTheme.extended.textPrimary)
                    }
                }
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Switch(
                        checked = state.biometricEnabled,
                        onCheckedChange = viewModel::setBiometricEnabled,
                    )
                    Spacer(Modifier.height(UsTheme.spacing.s))
                    Text(
                        "  Allow fingerprint or face unlock",
                        color = UsTheme.extended.textPrimary,
                    )
                }
                Spacer(Modifier.height(UsTheme.spacing.l))
                Text(
                    "Turn off",
                    style = MaterialTheme.typography.titleSmall,
                    color = UsTheme.extended.textPrimary,
                )
                UsTextField(
                    value = disablePin,
                    onValueChange = { disablePin = it.filter(Char::isDigit).take(MAX_PIN_LENGTH) },
                    label = "Current PIN",
                    singleLine = true,
                    isPassword = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                state.error?.let {
                    Text(
                        it,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.error,
                    )
                }
                UsSecondaryButton(
                    text = "Turn off chat lock",
                    onClick = {
                        viewModel.disable(disablePin)
                        disablePin = ""
                    },
                    enabled = disablePin.length >= MIN_PIN_LENGTH,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(vertical = UsTheme.spacing.s)
                        .testTag("lock-settings-disable"),
                )
            }
        }
    }
}

private val MIN_PIN_LENGTH = com.us.android.core.chat.lock.ChatLockManager.MIN_PIN_LENGTH
private const val MAX_PIN_LENGTH = 12
