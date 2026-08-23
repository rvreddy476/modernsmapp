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
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
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
                PrivacySettingsForm(current, viewModel, Modifier.padding(padding))
        }
    }
}

@Composable
private fun PrivacySettingsForm(
    state: PrivacySettingsUiState.Editing,
    viewModel: PrivacySettingsViewModel,
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
private fun ContactPermissionSection(
    value: com.us.android.core.profile.data.PrivacySettings,
    locked: Boolean,
    viewModel: PrivacySettingsViewModel,
) {
    UsSettingsSection("Contact permissions") {
        PrivacySelect("Who can message you", value.whoCanMessage, MESSAGE_OPTIONS, locked) {
            viewModel.select(PrivacyEnumField.MESSAGE, it)
        }
    }
}

@Composable
private fun ProfileVisibilitySection(
    value: com.us.android.core.profile.data.PrivacySettings,
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

private val MESSAGE_OPTIONS = options(
    "no_one" to "No one",
    "connections_only" to "Connections",
    "connections_and_mutual_followers" to "Connections + mutual followers",
    "followers_message_requests" to "Followers via requests",
    "everyone_message_requests" to "Everyone via requests",
)

private val VISIBILITY_OPTIONS = options(
    "no_one" to "No one",
    "connections_only" to "Connections",
    "everyone" to "Everyone",
)
