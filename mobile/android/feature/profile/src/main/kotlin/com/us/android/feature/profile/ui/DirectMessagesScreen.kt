package com.us.android.feature.profile.ui

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.DirectMessageAudience
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSelectRow

/** "Who can message you", the TikTok-style three-row picker. See [DirectMessageAudience]. */
@Composable
fun DirectMessagesScreen(
    onBack: () -> Unit,
    viewModel: DirectMessagesViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar("Direct messages", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val current = state) {
            DirectMessagesUiState.Loading -> UsLoadingState(Modifier.padding(padding), "Loading")
            is DirectMessagesUiState.Error ->
                UsErrorState(current.message, Modifier.padding(padding), onRetry = viewModel::load)
            is DirectMessagesUiState.Loaded ->
                DirectMessagesForm(current, viewModel, Modifier.padding(padding))
        }
    }
}

@Composable
private fun DirectMessagesForm(
    state: DirectMessagesUiState.Loaded,
    viewModel: DirectMessagesViewModel,
    modifier: Modifier,
) {
    val audience = state.audience
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
    ) {
        UsSettingsSection("Who can message you") {
            UsSettingsSelectRow(
                title = "Everyone",
                selected = audience.everyoneRequests.toWireValue(),
                options = REQUESTS_OPTIONS,
                onSelected = { viewModel.setEveryone(it.toBoolean()) },
                enabled = !state.saving,
            )
            UsSettingsSelectRow(
                title = "Followers",
                selected = audience.followersRequests.toWireValue(),
                options = REQUESTS_OPTIONS,
                onSelected = { viewModel.setFollowers(it.toBoolean()) },
                enabled = !state.saving,
            )
            UsSettingsSelectRow(
                title = "Friends",
                selected = audience.friendsDirect.toWireValue(),
                options = DIRECT_OPTIONS,
                onSelected = { viewModel.setFriends(it.toBoolean()) },
                enabled = !state.saving,
            )
        }
    }
}

/** Row-local `on`/`off` wire values — never sent to the server directly. */
private fun Boolean.toWireValue() = if (this) "on" else "off"
private fun String.toBoolean() = this == "on"

private val REQUESTS_OPTIONS = listOf(
    UsSettingsOption("on", "Requests"),
    UsSettingsOption("off", "Don't receive"),
)

private val DIRECT_OPTIONS = listOf(
    UsSettingsOption("on", "Direct"),
    UsSettingsOption("off", "Don't receive"),
)
