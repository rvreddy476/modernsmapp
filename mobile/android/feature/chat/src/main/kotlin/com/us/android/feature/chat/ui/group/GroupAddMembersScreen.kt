package com.us.android.feature.chat.ui.group

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.PersonHit
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.community.ChatFormField
import com.us.android.feature.chat.ui.community.PersonRow
import com.us.android.feature.chat.ui.home.ChatSectionLabel

/**
 * Add members: search people (connections up front, `v1/search/users` as
 * the pill is typed), tick as many as needed, Add. The outcome lines are
 * the server's — added, invited, skipped — one per person.
 */
@Composable
fun GroupAddMembersScreen(
    onDone: () -> Unit,
    onBack: () -> Unit,
    viewModel: GroupAddMembersViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar(title = "Add members", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            ChatFormField(
                value = state.query,
                onValueChange = viewModel::onQueryChange,
                label = "Who to add",
                placeholder = "Search by name or @username",
                counter = if (state.searching) "Searching…" else null,
                tag = "group_add_search",
                modifier = Modifier.padding(
                    horizontal = UsTheme.spacing.pageHorizontal,
                    vertical = UsTheme.spacing.m,
                ),
            )
            OutcomeLines(state.outcomes)
            CandidateList(
                state = state,
                onToggle = viewModel::toggle,
                modifier = Modifier.weight(1f),
            )
            AddButton(state = state, onAdd = viewModel::add, onDone = onDone)
            Text(
                text = "People whose settings ask for consent get an invite instead of being added directly.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier
                    .padding(horizontal = UsTheme.spacing.pageHorizontal)
                    .padding(bottom = UsTheme.spacing.l),
            )
        }
    }
}

/** What the server said about each person just added. */
@Composable
private fun OutcomeLines(outcomes: List<AddOutcomeLine>) {
    if (outcomes.isEmpty()) return
    Column(modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal)) {
        outcomes.forEach { line ->
            Text(
                text = "${line.name} — ${line.text}",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textBody,
                modifier = Modifier.padding(vertical = UsTheme.spacing.xs),
            )
        }
    }
}

@Composable
private fun CandidateList(
    state: GroupAddMembersUiState,
    onToggle: (PersonHit) -> Unit,
    modifier: Modifier = Modifier,
) {
    when {
        state.loadingConnections -> UsLoadingState(label = "Loading your connections", modifier = modifier)
        state.candidates.isEmpty() -> UsEmptyState(
            title = if (state.query.isBlank()) "No one to add yet" else "No one matches",
            detail = if (state.query.isBlank()) "Search for people by name or @username." else "Try another name.",
            modifier = modifier,
        )
        else -> LazyColumn(modifier = modifier.testTag("group_add_list")) {
            item(key = "label") { ChatSectionLabel("People") }
            items(state.candidates, key = { it.userId }) { hit ->
                val picked = hit.userId in state.selected
                PersonRow(
                    userId = hit.userId,
                    name = hit.nameForDisplay,
                    subtitle = hit.username.takeIf { it.isNotBlank() }?.let { "@$it" },
                    avatarUrl = hit.avatarUrl,
                    pillText = if (picked) "Selected" else "Select",
                    pillSelected = picked,
                    onPill = { onToggle(hit) },
                    onClick = { onToggle(hit) },
                    tag = "group_add_candidate:${hit.userId}",
                )
            }
        }
    }
}

/** "Add N" while people are ticked; "Done" once the last batch went through. */
@Composable
private fun AddButton(state: GroupAddMembersUiState, onAdd: () -> Unit, onDone: () -> Unit) {
    val buttonModifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.pageHorizontal)
    if (state.done && state.selected.isEmpty()) {
        UsButton(text = "Done", onClick = onDone, modifier = buttonModifier.testTag("group_add_done"))
    } else {
        UsButton(
            text = when {
                state.adding -> "Adding…"
                state.selected.isEmpty() -> "Add members"
                else -> "Add ${state.selected.size}"
            },
            onClick = onAdd,
            enabled = state.canAdd,
            loading = state.adding,
            modifier = buttonModifier.testTag("group_add_submit"),
        )
    }
}
