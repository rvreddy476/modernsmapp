package com.us.android.feature.memberships.creator

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.memberships.data.CreatorMember
import com.us.android.core.memberships.data.MembershipStatus
import com.us.android.core.memberships.data.rupees
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.memberships.mine.membershipDate

/**
 * The creator's members (paid memberships, 2026-10-23): who joined, on
 * which level and at what price, since when, and until when they have
 * access. Each row opens the member's profile. Reached from the Memberships
 * page and from a `membership_new_member` notification.
 */
@Composable
fun CreatorMembersScreen(
    onBack: () -> Unit,
    onOpenMember: (userId: String) -> Unit,
    viewModel: CreatorMembersViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(topBar = { UsTopBar(title = "Members", onBack = onBack) }) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding).testTag("creator_members")) {
            when {
                state.loading && state.members.isEmpty() -> UsLoadingState(label = "Loading members")
                state.loadError != null && state.members.isEmpty() ->
                    UsErrorState(message = state.loadError.orEmpty(), onRetry = viewModel::refresh)
                state.members.isEmpty() -> UsEmptyState(
                    title = "No members yet",
                    detail = "When someone joins one of your levels, they show here with when their month ends.",
                    modifier = Modifier.testTag("creator_members_empty"),
                )
                else -> MembersList(state = state, viewModel = viewModel, onOpenMember = onOpenMember)
            }
        }
    }
}

@Composable
private fun MembersList(state: CreatorMembersUiState, viewModel: CreatorMembersViewModel, onOpenMember: (String) -> Unit) {
    val list = rememberLazyListState()
    // The next page is asked for as the last rows come into view.
    LaunchedEffect(list) {
        snapshotFlow { list.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0 }
            .collect { last -> if (last >= list.layoutInfo.totalItemsCount - PREFETCH) viewModel.loadMore() }
    }
    LazyColumn(
        state = list,
        modifier = Modifier.fillMaxSize(),
        contentPadding = PaddingValues(vertical = UsTheme.spacing.l),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        items(state.members, key = { it.subscriptionId }) { member ->
            MemberCard(member = member, onClick = { onOpenMember(member.userId) })
        }
        item(key = "foot") {
            val moreError = state.moreError
            when {
                state.loadingMore -> Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator(color = UsTheme.extended.accentSolid)
                }
                moreError != null -> Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                    Text(moreError, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                    UsSecondaryButton(
                        text = "Try again",
                        onClick = viewModel::retryMore,
                        modifier = Modifier.fillMaxWidth().testTag("creator_members_retry"),
                    )
                }
            }
        }
    }
}

@Composable
private fun MemberCard(member: CreatorMember, onClick: () -> Unit) {
    UsSurfaceCard(onClick = onClick, modifier = Modifier.fillMaxWidth().testTag("creator_member:${member.subscriptionId}")) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsAvatar(
                name = member.nameForDisplay,
                seed = member.userId,
                size = UsAvatarSize.Medium,
                imageUrl = member.avatarUrl,
            )
            Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                Text(
                    text = member.nameForDisplay,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Text(
                    text = "${member.levelName.ifBlank { "Membership" }} · ${rupees(member.pricePaise)} a month",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textBody,
                )
                Text(
                    text = memberTimeLine(member),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (member.accessUntil != null) UsTheme.extended.textMuted else UsTheme.extended.statusWarning,
                )
            }
            Icon(imageVector = UsIcons.ChevronRight, contentDescription = null, tint = UsTheme.extended.textMuted)
        }
    }
}

/**
 * "Since 20 Oct 2026 · Active until 20 Nov 2026"; "… · Cancelled · access
 * until 1 Nov 2026" for one leaving at the month's end; the status alone
 * once access has ended.
 */
internal fun memberTimeLine(member: CreatorMember): String {
    val since = membershipDate(member.since).takeIf { it.isNotBlank() }?.let { "Since $it" }
    val until = member.accessUntil?.let { membershipDate(it) }?.takeIf { it.isNotBlank() }
    val status = member.status.label.substringBefore(" —")
    val access = when {
        until == null -> status
        member.status == MembershipStatus.Active -> "Active until $until"
        else -> "$status · access until $until"
    }
    return listOfNotNull(since, access).joinToString(" · ")
}

private const val PREFETCH = 4
