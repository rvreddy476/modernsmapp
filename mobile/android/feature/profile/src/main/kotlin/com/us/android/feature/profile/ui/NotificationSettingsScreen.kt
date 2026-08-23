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
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.NotificationSettings
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSelectRow
import com.us.android.core.ui.UsSettingsSwitchRow

@Composable
fun NotificationSettingsScreen(
    onBack: () -> Unit,
    viewModel: NotificationSettingsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar("Notifications", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val current = state) {
            NotificationSettingsUiState.Loading -> UsLoadingState(
                Modifier.padding(padding),
                "Loading notifications",
            )

            is NotificationSettingsUiState.Error -> UsErrorState(
                current.message,
                Modifier.padding(padding),
                onRetry = viewModel::load,
            )

            is NotificationSettingsUiState.Editing -> NotificationForm(
                current,
                viewModel,
                Modifier.padding(padding),
            )
        }
    }
}

@Composable
private fun NotificationForm(
    state: NotificationSettingsUiState.Editing,
    vm: NotificationSettingsViewModel,
    modifier: Modifier,
) {
    val value = state.value
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        DeliverySection(value, vm)
        SocialSection(value, vm)
        CommunitySection(value, vm)
        state.message?.let {
            Text(
                it,
                color = if (state.dirty) {
                    MaterialTheme.colorScheme.error
                } else {
                    UsTheme.extended.statusSuccess
                },
            )
        }
        UsButton(
            "Save notification settings",
            vm::save,
            Modifier.fillMaxWidth(),
            enabled = state.dirty && state.quietHoursValid,
            loading = state.saving,
        )
    }
}

@Composable
private fun DeliverySection(value: NotificationSettings, vm: NotificationSettingsViewModel) {
    UsSettingsSection("Delivery") {
        Toggle("Push notifications", value.pushEnabled, NotificationToggle.PUSH, vm)
        Toggle("Email notifications", value.emailEnabled, NotificationToggle.EMAIL, vm)
        Toggle(
            "Quiet hours",
            value.quietHoursEnabled,
            NotificationToggle.QUIET,
            vm,
            "Suppresses non-urgent delivery during the selected local window.",
        )
        if (value.quietHoursEnabled) QuietHoursFields(value, vm)
        UsSettingsSelectRow(
            "Email digest",
            value.emailDigest,
            DIGEST_OPTIONS,
            vm::digest,
            enabled = value.emailEnabled,
        )
    }
}

@Composable
private fun QuietHoursFields(
    value: NotificationSettings,
    vm: NotificationSettingsViewModel,
) {
    UsTextField(
        value.quietHoursStart,
        { vm.quietHours(start = it) },
        "Start (24-hour)",
        placeholder = "22:00",
        errorText = timeError(value.quietHoursStart),
    )
    UsTextField(
        value.quietHoursEnd,
        { vm.quietHours(end = it) },
        "End (24-hour)",
        placeholder = "07:00",
        errorText = timeError(value.quietHoursEnd),
    )
    UsTextField(
        value.quietHoursTimeZone,
        { vm.quietHours(timezone = it) },
        "Timezone",
        placeholder = "Asia/Kolkata",
        errorText = if (value.quietHoursTimeZone.isBlank()) "Timezone is required" else null,
    )
}

@Composable
private fun SocialSection(value: NotificationSettings, vm: NotificationSettingsViewModel) {
    UsSettingsSection("Social") {
        Toggle("Likes", value.pushLikes, NotificationToggle.LIKES, vm)
        Toggle("Super likes", value.pushSuperLikes, NotificationToggle.SUPER_LIKES, vm)
        Toggle("Comments", value.pushComments, NotificationToggle.COMMENTS, vm)
        Toggle("Replies", value.pushReplies, NotificationToggle.REPLIES, vm)
        Toggle("Mentions", value.pushMentions, NotificationToggle.MENTIONS, vm)
        Toggle("New followers", value.pushFollows, NotificationToggle.FOLLOWS, vm)
        Toggle("Connection requests", value.pushFriendRequests, NotificationToggle.FRIEND_REQUESTS, vm)
    }
}

@Composable
private fun CommunitySection(
    value: NotificationSettings,
    vm: NotificationSettingsViewModel,
) {
    UsSettingsSection("Groups, channels and communities") {
        Toggle("Group posts", value.pushGroupPosts, NotificationToggle.GROUP_POSTS, vm)
        Toggle("Group mentions", value.pushGroupMentions, NotificationToggle.GROUP_MENTIONS, vm)
        Toggle("Channel updates", value.pushChannelUpdates, NotificationToggle.CHANNEL_UPDATES, vm)
        Toggle("Urgent channel updates", value.pushChannelUrgent, NotificationToggle.CHANNEL_URGENT, vm)
        Toggle("Community posts", value.pushCommunityPosts, NotificationToggle.COMMUNITY_POSTS, vm)
        Toggle("Community mentions", value.pushCommunityMentions, NotificationToggle.COMMUNITY_MENTIONS, vm)
        Toggle("Event reminders", value.pushEventReminders, NotificationToggle.EVENTS, vm)
        Toggle(
            "Security and system",
            value.pushSystem,
            NotificationToggle.SYSTEM,
            vm,
            "We may still deliver essential security notices when ordinary push is off.",
        )
    }
}

@Composable
private fun Toggle(
    title: String,
    checked: Boolean,
    field: NotificationToggle,
    vm: NotificationSettingsViewModel,
    description: String? = null,
) =
    UsSettingsSwitchRow(title, checked, { vm.toggle(field, it) }, description = description)

private fun timeError(value: String): String? =
    if (NotificationSettingsUiState.Editing.TIME.matches(value)) null else "Use HH:mm"

private val DIGEST_OPTIONS = listOf(
    UsSettingsOption("never", "Never"),
    UsSettingsOption("daily", "Daily"),
    UsSettingsOption("weekly", "Weekly"),
)
