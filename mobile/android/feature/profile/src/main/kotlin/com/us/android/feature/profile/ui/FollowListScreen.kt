package com.us.android.feature.profile.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Tab
import androidx.compose.material3.TabRow
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.data.MediaRepository
import com.us.android.core.profile.data.FollowListPerson
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.components.SingletonComponent
import java.util.concurrent.ConcurrentHashMap

/**
 * A profile's Followers and Following, one tab each, opened on the count the
 * viewer tapped. The title is the account's @handle. Every row opens that
 * person's profile and carries the viewer's own relationship pill.
 */
@Composable
fun FollowListScreen(
    onBack: () -> Unit,
    onOpenProfile: (userId: String) -> Unit,
    viewModel: FollowListViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    UsScaffold(
        topBar = { UsTopBar(title = followListTitle(state.handle), onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            Column(modifier = Modifier.fillMaxSize()) {
                TabRow(
                    selectedTabIndex = FollowListTab.entries.indexOf(state.selected),
                    containerColor = UsTheme.extended.bgCanvas,
                    contentColor = UsTheme.extended.textPrimary,
                ) {
                    FollowListTab.entries.forEach { tab ->
                        Tab(
                            selected = tab == state.selected,
                            onClick = { viewModel.select(tab) },
                            text = { Text(tab.label, maxLines = 1) },
                            modifier = Modifier.testTag("follow_list_tab:${tab.wire}"),
                        )
                    }
                }
                // One list state per tab, so switching back keeps the place.
                key(state.selected) {
                    FollowListTabBody(
                        state = state,
                        tab = state.selected,
                        onOpenProfile = onOpenProfile,
                        onPillTap = viewModel::onPillTap,
                        onRetry = viewModel::retry,
                        onLoadMore = viewModel::loadMore,
                    )
                }
            }
            UsMessageHost(
                message = state.actionError?.let { UsMessage(it, UsMessageType.Error) },
                onDismiss = viewModel::dismissActionError,
            )
        }
    }

    state.confirm?.let { confirm ->
        FollowListConfirmSheet(
            confirm = confirm,
            onConfirm = viewModel::onConfirm,
            onDismiss = viewModel::onDismissConfirm,
        )
    }
}

internal fun followListTitle(handle: String): String = if (handle.isBlank()) "Connections" else "@$handle"

@Composable
@Suppress("LongParameterList") // the tab's state and its four doors out
private fun FollowListTabBody(
    state: FollowListUiState,
    tab: FollowListTab,
    onOpenProfile: (String) -> Unit,
    onPillTap: (String) -> Unit,
    onRetry: () -> Unit,
    onLoadMore: () -> Unit,
) {
    val page = state.page(tab)
    val listState = rememberLazyListState()
    val shouldLoadMore by remember(page) {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: return@derivedStateOf false
            page.hasMore && !page.loadingMore && !page.loadMoreFailed &&
                last >= listState.layoutInfo.totalItemsCount - LOAD_MORE_THRESHOLD
        }
    }
    LaunchedEffect(shouldLoadMore) {
        if (shouldLoadMore) onLoadMore()
    }

    when {
        page.loading && page.rows.isEmpty() -> UsLoadingState(label = "Loading ${tab.label.lowercase()}")
        page.error != null -> UsErrorState(message = page.error, onRetry = onRetry)
        page.isEmpty -> UsEmptyState(title = emptyTitle(tab, state.isOwnList), detail = emptyDetail(tab, state.isOwnList))
        else -> FollowListRows(
            state = state,
            tab = tab,
            page = page,
            listState = listState,
            onOpenProfile = onOpenProfile,
            onPillTap = onPillTap,
            onLoadMore = onLoadMore,
        )
    }
}

internal fun emptyTitle(tab: FollowListTab, own: Boolean): String = when (tab) {
    FollowListTab.FOLLOWERS -> if (own) "No followers yet" else "No followers to show"
    FollowListTab.FOLLOWING -> if (own) "You're not following anyone yet" else "Not following anyone yet"
}

internal fun emptyDetail(tab: FollowListTab, own: Boolean): String? = when {
    !own -> null
    tab == FollowListTab.FOLLOWERS -> "When people follow you, they'll be listed here."
    else -> "Accounts you follow will be listed here."
}

@Composable
@Suppress("LongParameterList")
private fun FollowListRows(
    state: FollowListUiState,
    tab: FollowListTab,
    page: FollowListPage,
    listState: LazyListState,
    onOpenProfile: (String) -> Unit,
    onPillTap: (String) -> Unit,
    onLoadMore: () -> Unit,
) {
    LazyColumn(state = listState, modifier = Modifier.fillMaxSize().testTag("follow_list:${tab.wire}")) {
        items(page.rows, key = { it.userId }) { person ->
            FollowListRowItem(
                person = person,
                pill = state.pillFor(tab, person),
                busy = person.userId in state.busyIds,
                onOpen = { onOpenProfile(person.userId) },
                onPillTap = { onPillTap(person.userId) },
            )
        }
        when {
            page.loadingMore -> item(key = "load-more") {
                Box(
                    modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l),
                    contentAlignment = Alignment.Center,
                ) {
                    CircularProgressIndicator()
                }
            }
            page.loadMoreFailed -> item(key = "load-more-failed") {
                Column(
                    modifier = Modifier.fillMaxWidth().padding(UsTheme.spacing.l),
                    horizontalAlignment = Alignment.CenterHorizontally,
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                ) {
                    Text(
                        text = "Couldn't load more.",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textMuted,
                    )
                    UsPillButton(text = "Try again", onClick = onLoadMore, filled = false)
                }
            }
        }
    }
}

@Composable
private fun FollowListRowItem(
    person: FollowListPerson,
    pill: FollowPill?,
    busy: Boolean,
    onOpen: () -> Unit,
    onPillTap: () -> Unit,
) {
    val name = person.displayName.ifBlank { person.username.ifBlank { "Someone" } }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onOpen)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("follow_list_row:${person.userId}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(
            name = name,
            size = UsAvatarSize.Chat,
            seed = person.userId,
            imageUrl = rememberProfileMediaUrl(person.avatarMediaId),
        )
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = name,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (person.username.isNotBlank()) {
                Text(
                    text = "@${person.username}",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (pill != null) {
            UsPillButton(
                text = pill.label,
                onClick = onPillTap,
                filled = pill.filled,
                busy = busy,
                modifier = Modifier.testTag("follow_list_pill:${person.userId}"),
            )
        }
    }
}

/** "Unfollow @name?" or "Cancel your request?", with the action and Cancel. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun FollowListConfirmSheet(confirm: FollowListConfirm, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    val handle = confirm.person.username.ifBlank { confirm.person.displayName.ifBlank { "them" } }
    val (title, detail, action) = when (confirm) {
        is FollowListConfirm.Unfollow -> Triple(
            "Unfollow @$handle?",
            "Their posts will stop showing in your feed. You can follow them again at any time.",
            "Unfollow",
        )
        is FollowListConfirm.CancelRequest -> Triple(
            "Cancel your request to follow @$handle?",
            "They won't be told you asked.",
            "Cancel request",
        )
    }
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgSheet,
    ) {
        Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.l)
                .testTag("follow_list_confirm"),
        ) {
            Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
            Text(detail, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            UsButton(
                text = action,
                onClick = onConfirm,
                modifier = Modifier.fillMaxWidth().testTag("follow_list_confirm_action"),
            )
            UsSecondaryButton(text = "Cancel", onClick = onDismiss, modifier = Modifier.fillMaxWidth())
        }
    }
}

/**
 * A row's picture: the avatar media id resolved through
 * `GET /v1/media/{id}/url` like every other surface, never a URL invented
 * from the id. Memoised for the session, so a scrolling list asks once per
 * picture.
 */
@Composable
internal fun rememberProfileMediaUrl(mediaId: String?): String? {
    val context = LocalContext.current
    val url by produceState(initialValue = mediaId?.let { ProfileMediaUrlMemo[it] }, mediaId) {
        val id = mediaId?.takeIf { it.isNotBlank() } ?: return@produceState
        ProfileMediaUrlMemo[id]?.let {
            value = it
            return@produceState
        }
        val repository = EntryPointAccessors
            .fromApplication(context.applicationContext, FollowListMediaEntryPoint::class.java)
            .mediaRepository()
        val resolved = (repository.delivery(id) as? AppResult.Success)?.data
            ?.takeIf { it.isReady }?.posterUrl
        if (resolved != null) {
            ProfileMediaUrlMemo[id] = resolved
            value = resolved
        }
    }
    return url
}

private object ProfileMediaUrlMemo {
    private val urls = ConcurrentHashMap<String, String>()
    operator fun get(id: String): String? = urls[id]
    operator fun set(id: String, url: String) {
        urls[id] = url
    }
}

@EntryPoint
@InstallIn(SingletonComponent::class)
internal interface FollowListMediaEntryPoint {
    fun mediaRepository(): MediaRepository
}

/** How many rows from the end to start fetching the next page. */
private const val LOAD_MORE_THRESHOLD = 4
