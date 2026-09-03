package com.us.android.feature.profile.ui

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
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.PrivacySettings
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsLinkRow
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSelectRow
import com.us.android.core.ui.UsSettingsSwitchRow

/**
 * Shows only controls that currently have an enforcing server-side consumer.
 * Stored future preferences (contact sync, last-seen and call/message filters)
 * stay out of the UI until their product surfaces can honour them.
 */
@Composable
fun PrivacySettingsScreen(
    onBack: () -> Unit,
    onDirectMessages: () -> Unit,
    viewModel: PrivacySettingsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar("Privacy and safety", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val current = state) {
            PrivacySettingsUiState.Loading ->
                UsLoadingState(Modifier.padding(padding), "Loading privacy settings")

            is PrivacySettingsUiState.Error ->
                UsErrorState(current.message, Modifier.padding(padding), onRetry = viewModel::load)

            is PrivacySettingsUiState.Editing ->
                PrivacySettingsForm(current, viewModel, onDirectMessages, Modifier.padding(padding))
        }
    }
}

@Composable
private fun PrivacySettingsForm(
    state: PrivacySettingsUiState.Editing,
    viewModel: PrivacySettingsViewModel,
    onDirectMessages: () -> Unit,
    modifier: Modifier,
) {
    val value = state.value
    val policyLocked = state.saving || value.under18Mode || value.strictPrivacyMode
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        if (value.under18Mode) {
            Text(
                text = "Minor-safety protections are enforced by the server and cannot be loosened.",
                color = MaterialTheme.colorScheme.primary,
            )
        }
        DiscoverabilitySection(state, viewModel)
        InteractionsSection(value, state.saving, viewModel, onDirectMessages)
        ContactPermissionSection(value, policyLocked, viewModel)
        ProfileVisibilitySection(value, policyLocked, viewModel)
        ProtectionSection(state, viewModel)
        state.message?.let { message ->
            Text(
                text = message,
                color = if (state.dirty) {
                    MaterialTheme.colorScheme.error
                } else {
                    UsTheme.extended.statusSuccess
                },
            )
        }
        UsButton(
            text = "Save privacy settings",
            onClick = viewModel::save,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.dirty,
            loading = state.saving,
        )
    }
}

@Composable
private fun DiscoverabilitySection(
    state: PrivacySettingsUiState.Editing,
    viewModel: PrivacySettingsViewModel,
) {
    var confirming by remember { mutableStateOf(false) }
    val value = state.value
    UsSettingsSection("Discoverability") {
        UsSettingsSwitchRow(
            title = "Private account",
            checked = value.isPrivateAccount,
            onCheckedChange = { turningOn ->
                if (turningOn) confirming = true else viewModel.setPrivateAccount(false)
            },
            description = "With a private account, only people you approve can follow you and see " +
                "your posts. Your existing followers are not affected.",
            enabled = !state.saving,
        )
    }
    if (confirming) {
        AlertDialog(
            onDismissRequest = { confirming = false },
            title = { Text("Make your account private?") },
            text = {
                Text(
                    "Only people you approve will be able to follow you and see your posts. " +
                        "Your existing followers keep their access.",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirming = false
                    viewModel.setPrivateAccount(true)
                }) { Text("Make private") }
            },
            dismissButton = {
                TextButton(onClick = { confirming = false }) { Text("Cancel") }
            },
        )
    }
}

@Composable
private fun InteractionsSection(
    value: PrivacySettings,
    saving: Boolean,
    viewModel: PrivacySettingsViewModel,
    onDirectMessages: () -> Unit,
) {
    UsSettingsSection("Interactions") {
        UsSettingsSelectRow(
            title = "Comments",
            selected = value.allowCommentsFrom,
            options = COMMENTS_OPTIONS,
            onSelected = { viewModel.select(PrivacyEnumField.COMMENTS, it) },
            description = "Who can comment on your posts",
            enabled = !saving,
        )
        UsSettingsLinkRow(
            title = "Direct messages",
            onClick = onDirectMessages,
            description = "Who can send you messages",
        )
    }
}

@Composable
private fun ContactPermissionSection(
    value: PrivacySettings,
    locked: Boolean,
    viewModel: PrivacySettingsViewModel,
) {
    // Messaging audience lives under Interactions → Direct messages; a second
    // control for the same setting here would only invite disagreement.
    UsSettingsSection("Contact permissions") {
        PrivacySelect("Who can add you to groups", value.whoCanAddToGroups, GROUP_ADD_OPTIONS, locked) {
            viewModel.select(PrivacyEnumField.GROUP_ADD, it)
        }
    }
    // Production chat pass (directive §3.2): every control below has a named
    // server-side consumer — pause gates creation and sends, the visibility
    // trio gates presence/receipt disclosure, typing gates the actor's own
    // indicator, and the preview toggle gates push text.
    UsSettingsSection("Chat") {
        PrivacySelect("Chat availability", value.chatAvailability, CHAT_AVAILABILITY_OPTIONS, false) {
            viewModel.select(PrivacyEnumField.CHAT_AVAILABILITY, it)
        }
        PrivacySelect("Who sees you online", value.whoCanSeeOnlineStatus, VISIBILITY_OPTIONS, locked) {
            viewModel.select(PrivacyEnumField.ONLINE, it)
        }
        PrivacySelect("Who sees your last seen", value.whoCanSeeLastSeen, VISIBILITY_OPTIONS, locked) {
            viewModel.select(PrivacyEnumField.LAST_SEEN, it)
        }
        PrivacySelect("Who sees your read receipts", value.whoCanSeeReadReceipts, VISIBILITY_OPTIONS, locked) {
            viewModel.select(PrivacyEnumField.READ_RECEIPTS, it)
        }
    }
}

@Composable
private fun ProfileVisibilitySection(
    value: PrivacySettings,
    locked: Boolean,
    viewModel: PrivacySettingsViewModel,
) {
    UsSettingsSection("Profile visibility") {
        PrivacySelect("Profile photo and cover", value.whoCanSeeProfilePhoto, VISIBILITY_OPTIONS, locked) {
            viewModel.select(PrivacyEnumField.PHOTO, it)
        }
    }
}

@Composable
private fun ProtectionSection(
    state: PrivacySettingsUiState.Editing,
    viewModel: PrivacySettingsViewModel,
) {
    val value = state.value
    UsSettingsSection("Protection") {
        UsSettingsSwitchRow(
            title = "Strict privacy mode",
            checked = value.strictPrivacyMode,
            onCheckedChange = { viewModel.toggle(PrivacyToggleField.STRICT, it) },
            description = "Clamps messaging and profile media now; future protections stay hidden until enforced.",
            enabled = !state.saving && !value.under18Mode,
        )
        UsSettingsSwitchRow(
            title = "Enable close-friends posts",
            checked = value.trustedCircleCloseFriendsPosts,
            onCheckedChange = { viewModel.toggle(PrivacyToggleField.TC_POSTS, it) },
            description = "Allow posts addressed to your trusted circle to reach those people.",
            enabled = !state.saving,
        )
        UsSettingsSwitchRow(
            title = "Send typing indicators",
            checked = value.sendTypingIndicators,
            onCheckedChange = { viewModel.toggle(PrivacyToggleField.TYPING, it) },
            description = "Show others when you're typing a message to them.",
            enabled = !state.saving,
        )
        UsSettingsSwitchRow(
            title = "Show message text in notifications",
            checked = value.showMessagePreview,
            onCheckedChange = { viewModel.toggle(PrivacyToggleField.MESSAGE_PREVIEW, it) },
            description = "Off shows \"New message\" instead of the message itself.",
            enabled = !state.saving,
        )
    }
}

@Composable
private fun PrivacySelect(
    title: String,
    value: String,
    options: List<UsSettingsOption>,
    locked: Boolean,
    onSelect: (String) -> Unit,
) {
    UsSettingsSelectRow(title, value, options, onSelect, enabled = !locked)
}

private fun options(vararg pairs: Pair<String, String>) =
    pairs.map { UsSettingsOption(it.first, it.second) }

private val VISIBILITY_OPTIONS = options(
    "no_one" to "No one",
    "connections_only" to "Connections",
    "everyone" to "Everyone",
)

private val GROUP_ADD_OPTIONS = options(
    "no_one" to "No one",
    "connections_only" to "Connections",
    "friends_of_friends_invites" to "Friends of friends, by invite",
    "everyone_with_approval" to "Anyone, with your approval",
)

private val CHAT_AVAILABILITY_OPTIONS = options(
    "enabled" to "On",
    "paused" to "Paused — no new messages in or out",
)

private val COMMENTS_OPTIONS = options(
    PrivacySettings.COMMENTS_EVERYONE to "Everyone",
    PrivacySettings.COMMENTS_FRIENDS to "Friends",
)
