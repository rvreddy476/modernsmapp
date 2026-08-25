package com.us.android.feature.chat.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Checkbox
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState

/**
 * Create a private group (directive §3.4, §5.5): title, eligible member
 * selection from the viewer's CONNECTIONS, confirmation with honest
 * per-target outcomes (added / invited / skipped by privacy).
 */
@Composable
fun GroupCreateScreen(
    onCreated: (conversationId: String, title: String) -> Unit,
    onBack: () -> Unit,
    viewModel: GroupCreateViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(state.created) {
        state.created?.let { onCreated(it.conversationId, it.title) }
    }

    UsScaffold(
        topBar = { UsTopBar(title = "New group", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
        ) {
            UsTextField(
                value = state.title,
                onValueChange = viewModel::onTitleChange,
                label = "Group name",
                placeholder = "What is this group about?",
                singleLine = true,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("group-title"),
            )

            Spacer(Modifier.height(UsTheme.spacing.m))
            Text(
                "Add people",
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                "You can add your connections. People whose settings ask for consent " +
                    "get an invite instead of being added directly.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            Spacer(Modifier.height(UsTheme.spacing.s))

            when {
                state.loadingCandidates -> UsLoadingState(label = "Loading your connections")
                state.candidates.isEmpty() -> UsEmptyState(
                    title = "No connections yet",
                    detail = "Groups start from your accepted connections.",
                )
                else -> LazyColumn(modifier = Modifier.weight(1f)) {
                    items(state.candidates, key = { it.userId }) { candidate ->
                        CandidateRow(
                            candidate = candidate,
                            selected = candidate.userId in state.selectedIds,
                            onToggle = { viewModel.toggle(candidate.userId) },
                        )
                    }
                }
            }

            state.error?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            UsButton(
                text = when {
                    state.creating -> "Creating…"
                    state.selectedIds.isEmpty() -> "Create group"
                    else -> "Create group (${state.selectedIds.size})"
                },
                onClick = viewModel::create,
                enabled = state.canCreate,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(vertical = UsTheme.spacing.m)
                    .testTag("group-create"),
            )
        }
    }
}

@Composable
private fun CandidateRow(
    candidate: GroupCandidate,
    selected: Boolean,
    onToggle: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onToggle)
            .padding(vertical = UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(name = candidate.displayName, size = UsAvatarSize.Small, seed = candidate.userId)
        Text(
            candidate.displayName,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        Checkbox(checked = selected, onCheckedChange = { onToggle() })
    }
}
