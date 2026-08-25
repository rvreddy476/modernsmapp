package com.us.android.feature.chat.ui

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
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Snackbar
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState

/**
 * Group info and administration (directive §3.4): roster with roles, rename,
 * promote/demote/remove per the role ladder, explicit ownership transfer,
 * leave. Controls the viewer's role does not permit are absent, not disabled
 * — an admin does not see owner-only actions at all.
 */
@Composable
fun GroupInfoScreen(
    onLeft: () -> Unit,
    onBack: () -> Unit,
    viewModel: GroupInfoViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(state.left) {
        if (state.left) onLeft()
    }

    UsScaffold(
        topBar = { UsTopBar(title = "Group info", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding).fillMaxSize()) {
            if (state.loading) {
                UsLoadingState(label = "Loading group")
                return@Column
            }

            GroupHeader(state = state, viewModel = viewModel)

            Text(
                "${state.memberCount} members · groups hold up to 1,024",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal),
            )
            Spacer(Modifier.height(UsTheme.spacing.s))

            LazyColumn(modifier = Modifier.weight(1f)) {
                items(state.members, key = { it.userId }) { member ->
                    MemberRow(
                        member = member,
                        state = state,
                        viewModel = viewModel,
                    )
                }
            }

            UsSecondaryButton(
                text = "Leave group",
                onClick = viewModel::leave,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(UsTheme.spacing.pageHorizontal)
                    .testTag("group-leave"),
            )

            state.notice?.let { notice ->
                Snackbar(
                    modifier = Modifier.padding(UsTheme.spacing.m),
                    action = {
                        TextButton(onClick = viewModel::dismissNotice) { Text("OK") }
                    },
                ) { Text(notice) }
            }
        }
    }
}

@Composable
private fun GroupHeader(state: GroupInfoUiState, viewModel: GroupInfoViewModel) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.pageHorizontal),
    ) {
        if (state.renaming) {
            UsTextField(
                value = state.renameDraft,
                onValueChange = viewModel::onRenameDraft,
                label = "Group name",
                singleLine = true,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("group-rename-field"),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                TextButton(onClick = viewModel::cancelRename) { Text("Cancel") }
                TextButton(
                    onClick = viewModel::confirmRename,
                    modifier = Modifier.testTag("group-rename-save"),
                ) { Text("Save") }
            }
        } else {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    state.title.ifBlank { "Group" },
                    style = MaterialTheme.typography.titleLarge,
                    fontWeight = FontWeight.Bold,
                    color = UsTheme.extended.textPrimary,
                    modifier = Modifier.weight(1f),
                )
                if (state.isAdmin) {
                    TextButton(
                        onClick = viewModel::startRename,
                        modifier = Modifier.testTag("group-rename"),
                    ) { Text("Rename") }
                }
            }
        }
    }
}

@Composable
private fun MemberRow(
    member: ConversationMember,
    state: GroupInfoUiState,
    viewModel: GroupInfoViewModel,
) {
    var menuOpen by remember { mutableStateOf(false) }
    val busy = member.userId in state.busyUserIds

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(
                horizontal = UsTheme.spacing.pageHorizontal,
                vertical = UsTheme.spacing.s,
            ),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(
            name = member.displayName.ifBlank { "Member" },
            size = UsAvatarSize.Small,
            seed = member.userId,
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                member.displayName.ifBlank { "Member" },
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                when (member.role) {
                    "owner" -> "Owner"
                    "admin" -> "Admin"
                    else -> "Member"
                },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }

        // The action menu renders only when the viewer's role can actually do
        // something to THIS member (role ladder, directive §3.4).
        val actions = memberActions(state, member)
        if (actions.isNotEmpty() && !busy) {
            TextButton(onClick = { menuOpen = true }) { Text("Manage") }
            DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                actions.forEach { action ->
                    DropdownMenuItem(
                        text = { Text(action.label) },
                        onClick = {
                            menuOpen = false
                            action.run(viewModel, member.userId)
                        },
                    )
                }
            }
        }
    }
}

private data class MemberAction(
    val label: String,
    val run: (GroupInfoViewModel, String) -> Unit,
)

private fun memberActions(state: GroupInfoUiState, member: ConversationMember): List<MemberAction> {
    if (member.role == "owner") return emptyList()
    val actions = mutableListOf<MemberAction>()
    if (state.isOwner) {
        if (member.role == "member") {
            actions += MemberAction("Make admin") { vm, id -> vm.promote(id) }
        } else {
            actions += MemberAction("Remove admin role") { vm, id -> vm.demote(id) }
        }
        actions += MemberAction("Transfer ownership") { vm, id -> vm.transferOwnership(id) }
        actions += MemberAction("Remove from group") { vm, id -> vm.removeMember(id) }
    } else if (state.isAdmin && member.role == "member") {
        actions += MemberAction("Remove from group") { vm, id -> vm.removeMember(id) }
    }
    return actions
}
