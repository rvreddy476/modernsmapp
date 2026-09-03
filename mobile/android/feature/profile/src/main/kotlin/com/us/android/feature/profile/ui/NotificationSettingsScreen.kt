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
import com.us.android.core.profile.data.NotificationCategory
import com.us.android.core.profile.data.NotificationSettings
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsDualSwitchHeader
import com.us.android.core.ui.UsSettingsDualSwitchRow
import com.us.android.core.ui.UsSettingsLinkRow
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
        topBar = { UsTopBar("Push notifications", onBack = onBack) },
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
        InteractionsSection(value, state, vm)
        MoreSection(value, state, vm)
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
        UsSettingsSwitchRow(
            "Push notifications",
            value.pushEnabled,
            { vm.toggleGlobal(push = it) },
        )
        UsSettingsSwitchRow(
            "Email notifications",
            value.emailEnabled,
            { vm.toggleGlobal(email = it) },
        )
        UsSettingsSwitchRow(
            "Quiet hours",
            value.quietHoursEnabled,
            { vm.toggleGlobal(quiet = it) },
            description = "Suppresses non-urgent delivery during the selected local window.",
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

/**
 * The TikTok-style "Interactions" block: one row per primary category, each
 * with its own In-app and Push switches side by side.
 */
@Composable
private fun InteractionsSection(
    value: NotificationSettings,
    state: NotificationSettingsUiState.Editing,
    vm: NotificationSettingsViewModel,
) {
    UsSettingsSection("Interactions") {
        UsSettingsDualSwitchHeader()
        NotificationCategory.primaries.forEach { category ->
            CategoryRow(category, value, state.saving, vm)
        }
    }
}

/** The remaining, lower-traffic categories, collapsed behind one link row. */
@Composable
private fun MoreSection(
    value: NotificationSettings,
    state: NotificationSettingsUiState.Editing,
    vm: NotificationSettingsViewModel,
) {
    UsSettingsSection("More") {
        UsSettingsLinkRow(
            title = if (state.moreExpanded) "Hide more categories" else "Show more categories",
            onClick = vm::toggleMore,
        )
        if (state.moreExpanded) {
            UsSettingsDualSwitchHeader()
            NotificationCategory.secondaries.forEach { category ->
                CategoryRow(category, value, state.saving, vm)
            }
        }
    }
}

@Composable
private fun CategoryRow(
    category: NotificationCategory,
    value: NotificationSettings,
    saving: Boolean,
    vm: NotificationSettingsViewModel,
) {
    val channels = value.channels(category)
    UsSettingsDualSwitchRow(
        title = category.label,
        inApp = channels.inApp,
        push = channels.push,
        onInAppChange = { vm.setChannel(category, inApp = it) },
        onPushChange = { vm.setChannel(category, push = it) },
        enabled = !saving,
        pushEnabled = !saving && value.pushEnabled,
    )
}

private fun timeError(value: String): String? =
    if (NotificationSettingsUiState.Editing.TIME.matches(value)) null else "Use HH:mm"

private val DIGEST_OPTIONS = listOf(
    UsSettingsOption("never", "Never"),
    UsSettingsOption("daily", "Daily"),
    UsSettingsOption("weekly", "Weekly"),
)
