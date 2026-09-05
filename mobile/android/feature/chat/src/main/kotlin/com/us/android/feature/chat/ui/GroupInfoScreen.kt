package com.us.android.feature.chat.ui

import android.content.Intent
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.chat.data.CommunityRules
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.InviteLink
import com.us.android.core.chat.data.InviteLinkState
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.chat.ui.community.ChatFormField
import com.us.android.feature.chat.ui.community.PictureSlot
import com.us.android.feature.chat.ui.home.ChatActionPill
import com.us.android.feature.chat.ui.home.ChatSectionLabel
import com.us.android.feature.chat.ui.home.ChatTogglePill
import com.us.android.feature.chat.ui.home.rememberMediaUrl

/**
 * Group info and administration (directive §3.4, groups pass 2026-09-05):
 * photo, name, description, the invite link (owner/admin), "Add members",
 * the roster with roles and the role ladder's actions, and leave. Controls
 * the viewer's role does not permit are absent, not disabled — an admin
 * does not see owner-only actions at all.
 */
@Composable
fun GroupInfoScreen(
    onLeft: () -> Unit,
    onBack: () -> Unit,
    onAddMembers: (conversationId: String) -> Unit,
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
            LazyColumn(modifier = Modifier.weight(1f).testTag("group_info_list")) {
                item(key = "header") { GroupHeader(state = state, viewModel = viewModel) }
                item(key = "description") { DescriptionSection(state = state, viewModel = viewModel) }
                if (state.isAdmin) {
                    item(key = "invite") { InviteLinkSection(state = state, viewModel = viewModel) }
                }
                item(key = "members-label") {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier.fillMaxWidth().padding(end = UsTheme.spacing.pageHorizontal),
                    ) {
                        ChatSectionLabel("${state.memberCount} members", modifier = Modifier.weight(1f))
                        if (state.isAdmin) {
                            ChatActionPill(
                                text = "Add members",
                                icon = UsIcons.UserPlus,
                                onClick = { onAddMembers(viewModel.conversationId) },
                                tag = "group-add-members",
                            )
                        }
                    }
                }
                items(state.members, key = { it.userId }) { member ->
                    MemberRow(member = member, state = state, viewModel = viewModel)
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

/** The photo (tap to change, owner/admin), the name, Rename. */
@Composable
private fun GroupHeader(state: GroupInfoUiState, viewModel: GroupInfoViewModel) {
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        viewModel.changePhoto(uri)
    }
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.pageHorizontal),
    ) {
        if (state.isAdmin) {
            PictureSlot(
                imageUrl = state.avatarUrl,
                name = state.title.ifBlank { "Group" },
                onPick = { picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) },
                busy = state.uploadingPhoto,
            )
            Text(
                text = if (state.uploadingPhoto) "Uploading photo…" else "Change photo",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        } else {
            UsAvatar(
                name = state.title.ifBlank { "Group" },
                size = UsAvatarSize.Large,
                seed = viewModel.conversationId,
                imageUrl = state.avatarUrl
            )
        }
        if (state.renaming) {
            ChatFormField(
                value = state.renameDraft,
                onValueChange = viewModel::onRenameDraft,
                label = "Group name",
                modifier = Modifier.fillMaxWidth().testTag("group-rename-field"),
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                TextButton(onClick = viewModel::cancelRename) { Text("Cancel") }
                TextButton(
                    onClick = viewModel::confirmRename,
                    modifier = Modifier.testTag("group-rename-save"),
                ) { Text("Save") }
            }
        } else {
            Text(
                text = state.title.ifBlank { "Group" },
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                textAlign = TextAlign.Center,
            )
            if (state.isAdmin) {
                ChatTogglePill(
                    text = "Rename",
                    selected = false,
                    onClick = viewModel::startRename,
                    tag = "group-rename"
                )
            }
        }
    }
}

/** The description, or "Add a description" for an admin; an inline editor when editing. */
@Composable
private fun DescriptionSection(state: GroupInfoUiState, viewModel: GroupInfoViewModel) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        if (state.editingDescription) {
            ChatFormField(
                value = state.descriptionDraft,
                onValueChange = viewModel::onDescriptionDraft,
                label = "Description",
                placeholder = "What is this group for?",
                problem = state.descriptionProblem,
                counter = "${state.descriptionDraft.length}/${CommunityRules.GROUP_DESCRIPTION_MAX}",
                singleLine = false,
                minLines = DESCRIPTION_LINES,
                tag = "group-description-field",
            )
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                TextButton(onClick = viewModel::cancelDescription) { Text("Cancel") }
                TextButton(
                    onClick = viewModel::confirmDescription,
                    enabled = state.descriptionProblem == null,
                    modifier = Modifier.testTag("group-description-save"),
                ) { Text("Save") }
            }
        } else {
            Text(
                text = "Description",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textSecondary,
            )
            Text(
                text = state.description.ifBlank {
                    if (state.isAdmin) {
                        "Add a description so people know what this group is for."
                    } else {
                        "No description yet."
                    }
                },
                style = MaterialTheme.typography.bodyMedium,
                color = if (state.description.isBlank()) UsTheme.extended.textMuted else UsTheme.extended.textBody,
                modifier = Modifier.testTag("group-description"),
            )
            if (state.isAdmin) {
                ChatTogglePill(
                    text = if (state.description.isBlank()) "Add description" else "Edit description",
                    selected = false,
                    onClick = viewModel::startDescription,
                    tag = "group-description-edit",
                )
            }
        }
    }
}

/**
 * The invite link (owner/admin): the URL with Copy and Share, its uses and
 * expiry, Revoke; or "Create link" when none is live. A dead link is said.
 */
@Composable
private fun InviteLinkSection(state: GroupInfoUiState, viewModel: GroupInfoViewModel) {
    val shape = RoundedCornerShape(UsTheme.radii.panel)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .background(UsTheme.extended.bgRaised, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .padding(UsTheme.spacing.l)
            .testTag("group-invite-link"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Text(
            text = "Invite link",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textSecondary,
        )
        val link = state.inviteLink
        if (!state.inviteLinkLoaded) {
            Text("Checking…", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        } else if (link == null) {
            Text(
                text = "Anyone with the link can join this group.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
            ChatActionPill(
                text = if (state.inviteBusy) "Creating…" else "Create link",
                icon = UsIcons.Link,
                onClick = { if (!state.inviteBusy) viewModel.createInviteLink() },
                tag = "group-invite-create",
            )
        } else {
            Text(
                text = link.url.ifBlank { link.code },
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.testTag("group-invite-url"),
            )
            Text(
                text = inviteFacts(link, state.inviteLinkState),
                style = MaterialTheme.typography.bodySmall,
                color = if (state.inviteLinkState == InviteLinkState.Live) {
                    UsTheme.extended.textMuted
                } else {
                    MaterialTheme.colorScheme.error
                },
                modifier = Modifier.testTag("group-invite-facts"),
            )
            InviteLinkActions(
                link = link,
                groupTitle = state.title,
                busy = state.inviteBusy,
                onRevoke = viewModel::revokeInviteLink,
            )
        }
    }
}

/** Copy, Share (the system chooser) and Revoke for a live link. */
@Composable
private fun InviteLinkActions(link: InviteLink, groupTitle: String, busy: Boolean, onRevoke: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val text = link.url.ifBlank { link.code }
    Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        ChatTogglePill(
            text = "Copy",
            selected = false,
            onClick = { clipboard.setText(AnnotatedString(text)) },
            tag = "group-invite-copy",
        )
        ChatTogglePill(
            text = "Share",
            selected = false,
            onClick = {
                val send = Intent(Intent.ACTION_SEND).apply {
                    type = "text/plain"
                    putExtra(Intent.EXTRA_TEXT, "Join ${groupTitle.ifBlank { "my group" }}: $text")
                }
                context.startActivity(Intent.createChooser(send, "Share invite link"))
            },
            tag = "group-invite-share",
        )
        ChatTogglePill(
            text = if (busy) "…" else "Revoke",
            selected = false,
            onClick = { if (!busy) onRevoke() },
            tag = "group-invite-revoke",
        )
    }
}

/** "3 of 10 uses · expires in 6 days", or the reason the link is dead. */
private fun inviteFacts(link: InviteLink, state: InviteLinkState?): String {
    val uses = if (link.maxUses > 0) "${link.uses} of ${link.maxUses} uses" else "${link.uses} uses"
    val expiry = link.expiresAt?.let { formatRelativeTime(it) }?.takeIf { it.isNotBlank() }
    return when (state) {
        InviteLinkState.Expired -> "Expired · $uses"
        InviteLinkState.Exhausted -> "Used up · $uses"
        else -> if (expiry != null) "$uses · expires $expiry" else "$uses · never expires"
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
            imageUrl = rememberMediaUrl(member.avatarMediaId),
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

private const val DESCRIPTION_LINES = 3
private val HAIRLINE = 1.dp
