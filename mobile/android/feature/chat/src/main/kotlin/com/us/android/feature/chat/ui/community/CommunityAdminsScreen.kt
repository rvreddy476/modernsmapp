package com.us.android.feature.chat.ui.community

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Snackbar
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.home.ChatSectionLabel

/** The owner's admin roster: search to add, × to remove. */
@Composable
fun CommunityAdminsScreen(
    onBack: () -> Unit,
    viewModel: CommunityAdminsViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar(title = "Admins", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            ChatFormField(
                value = state.query,
                onValueChange = viewModel::onQueryChange,
                label = "Add an admin",
                placeholder = "Search people by name or @username",
                counter = if (state.searching) "Searching…" else null,
                tag = "community_admins_search",
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
            )
            if (state.loading) {
                UsLoadingState(label = "Loading admins")
                return@Column
            }
            LazyColumn(modifier = Modifier.weight(1f).testTag("community_admins_list")) {
                if (state.results.isNotEmpty()) {
                    item(key = "results-label") { ChatSectionLabel("People") }
                    items(state.results, key = { "hit:${it.userId}" }) { hit ->
                        PersonRow(
                            userId = hit.userId,
                            name = hit.nameForDisplay,
                            subtitle = hit.username.takeIf { it.isNotBlank() }?.let { "@$it" },
                            avatarUrl = hit.avatarUrl,
                            pillText = "Make admin",
                            busy = hit.userId in state.busyUserIds,
                            onPill = { viewModel.add(hit.userId) },
                            tag = "community_admin_hit:${hit.userId}",
                        )
                    }
                }
                item(key = "admins-label") { ChatSectionLabel("Admins") }
                if (state.admins.isEmpty()) {
                    item(key = "empty") {
                        UsEmptyState(
                            title = "Only you post here",
                            detail = "Add admins so others can share updates and events.",
                            modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.xxxxl),
                        )
                    }
                }
                items(state.admins, key = { "admin:${it.userId}" }) { admin ->
                    PersonRow(
                        userId = admin.userId,
                        name = admin.displayName.ifBlank { "Admin" },
                        subtitle = "Admin",
                        avatarUrl = null,
                        onRemove = { viewModel.remove(admin.userId) },
                        tag = "community_admin:${admin.userId}",
                    )
                }
            }
            state.notice?.let { notice ->
                Snackbar(
                    modifier = Modifier.padding(UsTheme.spacing.m),
                    action = { TextButton(onClick = viewModel::dismissNotice) { Text("OK") } },
                ) { Text(notice) }
            }
        }
    }
}
