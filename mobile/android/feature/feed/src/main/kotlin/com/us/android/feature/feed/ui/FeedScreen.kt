package com.us.android.feature.feed.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.tooling.preview.Preview
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.paging.LoadState
import androidx.paging.compose.LazyPagingItems
import androidx.paging.compose.collectAsLazyPagingItems
import com.us.android.core.common.error.AppError
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsBadgedIcon
import com.us.android.core.designsystem.component.UsHomeTopBar
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.engagement.data.repostCountOr
import com.us.android.core.engagement.data.repostedOr
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.ui.DEFAULT_MEDIA_ASPECT
import com.us.android.core.ui.EngagementFailureBar
import com.us.android.core.ui.PostActionState
import com.us.android.core.ui.PostCard
import com.us.android.core.ui.PostCardMediaPage
import com.us.android.core.ui.PostCardPoll
import com.us.android.core.ui.PostCardPollOption
import com.us.android.core.ui.PostCardState
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.rememberPostSharer
import com.us.android.feature.feed.data.AppErrorException
import com.us.android.feature.feed.ui.comments.CommentsSheet

@Composable
fun FeedScreen(
    onOpenPost: (postId: String) -> Unit,
    onOpenAuthor: (userId: String) -> Unit,
    onOpenMessages: () -> Unit,
    onOpenNotifications: () -> Unit,
    /** Momentum's header search glyph — :app decides it opens the Explore tab. */
    onOpenSearch: () -> Unit,
    viewModel: FeedViewModel = hiltViewModel(),
) {
    val items = viewModel.items.collectAsLazyPagingItems()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    val pollVotes by viewModel.pollVotes.collectAsStateWithLifecycle()
    val failures by viewModel.failures.collectAsStateWithLifecycle()
    var commentsFor by rememberSaveable { mutableStateOf<String?>(null) }

    // Two effects, deliberately. A refresh replaces the whole list with values
    // just fetched, so all of them are server authority. An append leaves the
    // earlier pages in the snapshot exactly as they were loaded — including a
    // `has_reacted=false` captured before the viewer liked the row — so those
    // rows must NOT be reprocessed. Reconciling the whole snapshot on append
    // is what made a confirmed like revert on scroll.
    LaunchedEffect(items.loadState.refresh) {
        if (items.loadState.refresh is LoadState.NotLoading && items.itemCount > 0) {
            viewModel.onRefreshHydrated(items.itemSnapshotList.items)
        }
    }
    LaunchedEffect(items.loadState.append, items.itemCount) {
        if (items.loadState.append is LoadState.NotLoading && items.itemCount > 0) {
            viewModel.onAppendHydrated(items.itemSnapshotList.items)
        }
    }

    UsScaffold(
        // Momentum's header: search, Messages, the bell. Every one of them
        // works — Search, New post and Messages were all rendered here once
        // with empty click handlers, and were removed on the rule that a
        // visible primary control which does nothing is worse than an absent
        // one. Each callback is a REQUIRED parameter so none can be re-added
        // inert. Create moved to the bottom bar's centre button.
        topBar = {
            UsHomeTopBar(
                onHomeClick = { },
                actions = {
                    IconButton(onClick = onOpenSearch) {
                        Icon(
                            imageVector = UsIcons.Search,
                            contentDescription = "Search",
                            tint = UsTheme.extended.textPrimary,
                        )
                    }
                    IconButton(onClick = onOpenMessages) {
                        Icon(
                            imageVector = UsIcons.Comment,
                            contentDescription = "Messages",
                            tint = UsTheme.extended.textPrimary,
                        )
                    }
                    NotificationsAction(onClick = onOpenNotifications)
                },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(modifier = Modifier.padding(padding)) {
            EngagementFailureBar(
                failures = failures,
                onRetry = viewModel::retryFailure,
                onDismiss = viewModel::dismissFailure,
            )
            FeedList(
                items = items,
                overlays = overlays,
                pollVotes = pollVotes,
                onOpenPost = onOpenPost,
                onOpenAuthor = onOpenAuthor,
                onOpenComments = { commentsFor = it },
                onReact = viewModel::onReact,
                onBookmark = viewModel::onBookmark,
                onRepost = viewModel::onRepost,
                onVotePoll = viewModel::onVotePoll,
                onExternalShared = viewModel::onExternalShared,
                posterUrl = viewModel::posterUrl,
                mediaPages = viewModel::mediaPages,
            )
        }
    }

    // Comments open over the feed rather than navigating away, so the reader
    // keeps their place in the list and the post stays visible above the
    // conversation about it.
    commentsFor?.let { postId ->
        CommentsSheet(postId = postId, onDismiss = { commentsFor = null })
    }
}

// Flat callbacks rather than a bundled object: a data class of lambdas gets a
// new identity on every recomposition, which would recompose every visible row.
@Suppress("LongParameterList")
@Composable
private fun FeedList(
    items: LazyPagingItems<FeedItem>,
    overlays: Map<String, EngagementOverlay>,
    pollVotes: Map<String, Set<String>>,
    onOpenPost: (String) -> Unit,
    onOpenAuthor: (String) -> Unit,
    onOpenComments: (String) -> Unit,
    onReact: (postId: String, serverReacted: Boolean) -> Unit,
    onBookmark: (postId: String, serverBookmarked: Boolean) -> Unit,
    onRepost: (postId: String, serverReposted: Boolean) -> Unit,
    onVotePoll: (postId: String, optionId: String) -> Unit,
    onExternalShared: (String) -> Unit,
    posterUrl: (FeedItem) -> String?,
    mediaPages: (FeedItem) -> List<PostCardMediaPage>,
    modifier: Modifier = Modifier,
) {
    val refresh = items.loadState.refresh
    // Hoisted out of the list body: this resolves a Context and would
    // otherwise be re-created inside every visible row.
    val share = rememberPostSharer()

    when {
        refresh is LoadState.Loading && items.itemCount == 0 ->
            UsLoadingState(modifier = modifier, label = "Loading feed")

        // Only when nothing is on screen. A refresh failure with rows already
        // loaded must never clear them — the reader would lose their place to
        // a transient network blip.
        refresh is LoadState.Error && items.itemCount == 0 -> UsErrorState(
            message = refresh.error.feedMessage(),
            modifier = modifier,
            onRetry = items::retry,
        )

        refresh is LoadState.NotLoading && items.itemCount == 0 -> UsEmptyState(
            title = "Nothing here yet",
            detail = "Posts from people you follow will show up here.",
            modifier = modifier,
        )

        // A scrollable column of compact cards, not a full-screen pager.
        //
        // The immersive one-post-per-screen presentation belongs to Reels,
        // where every item is video. Applying it to the home timeline turns a
        // two-line text post into a full screen of empty space and makes a
        // mixed feed unreadable. ImmersivePostPage remains available in
        // :core:ui for the surfaces that genuinely want it.
        else -> LazyColumn(
            state = rememberLazyListState(),
            modifier = modifier.fillMaxSize(),
            // contentPadding, not Modifier.padding: padding the list shrinks
            // the scroll viewport so cards are clipped at a hard inset edge
            // mid-scroll. Padding the content lets them reach the real screen
            // edges and insets only the first and last.
            //
            // Figma redesign: contained cards float on the canvas with a
            // 20dp side gutter and 16dp between neighbours — the separation
            // the old full-bleed rows drew as divider lines.
            contentPadding = PaddingValues(
                horizontal = UsTheme.spacing.xxxxl,
                vertical = UsTheme.spacing.xxl,
            ),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            // `key` is what lets Compose keep an item's state across a page
            // append. Without it every append re-keys by index and the whole
            // visible list recomposes, which is the classic feed jank.
            items(
                count = items.itemCount,
                key = { index -> items.peek(index)?.id ?: index },
            ) { index ->
                val item = items[index] ?: return@items
                val overlay = overlays[item.id] ?: EngagementOverlay()
                PostCard(
                    state = item.toCardState(
                        overlay,
                        posterUrl(item),
                        mediaPages(item),
                        pollVotes[item.id].orEmpty(),
                    ),
                    onClick = { onOpenPost(item.id) },
                    onAuthorClick = { onOpenAuthor(item.author.id) },
                    onReact = { onReact(item.id, item.viewer.hasReacted) },
                    onComment = { onOpenComments(item.id) },
                    onRepost = { onRepost(item.id, item.viewer.hasReposted) },
                    onBookmark = { onBookmark(item.id, item.viewer.isBookmarked) },
                    onVotePoll = if (item.poll?.hasEnded == false) {
                        { optionId -> onVotePoll(item.id, optionId) }
                    } else {
                        null
                    },
                    onShare = {
                        share(item.text, item.author.nameForDisplay)
                        // Recorded only after the chooser was actually
                        // launched, and only here — the repost endpoint
                        // already records repost/quote shares, so counting
                        // this one through both would double it.
                        onExternalShared(item.id)
                    },
                )
            }

            item(key = "append_state") {
                AppendState(state = items.loadState.append, onRetry = items::retry)
            }
        }
    }
}

/** The footer: a spinner while appending, an inline retry when it failed. */
@Composable
private fun AppendState(state: LoadState, onRetry: () -> Unit) {
    when (state) {
        is LoadState.Loading -> Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.xxl),
            contentAlignment = Alignment.Center,
        ) {
            CircularProgressIndicator(color = MaterialTheme.colorScheme.primary)
        }

        is LoadState.Error -> Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(UsTheme.spacing.xxl),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            // A failed APPEND must not clear the list. The rows already on
            // screen are still valid; only the next page is missing.
            Text(
                text = state.error.feedMessage(),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
            )
            UsSecondaryButton(text = "Try again", onClick = onRetry, modifier = Modifier.fillMaxWidth())
        }

        is LoadState.NotLoading -> Unit
    }
}

/**
 * Paging carries errors as `Throwable`, so the typed [AppError] the network
 * layer produced is re-read here rather than reduced to a generic message.
 */
private fun Throwable.feedMessage(): String = when ((this as? AppErrorException)?.error) {
    is AppError.NoNetwork -> "You're offline. Check your connection and try again."
    is AppError.Timeout -> "That took too long. Try again."
    is AppError.AuthFailed -> "Please sign in again to see your feed."
    else -> "We couldn't load the feed."
}

/**
 * Layers this session's local taps over the server's reported state.
 *
 * Membership in the set means "the user changed this since the page loaded",
 * which is why it is an XOR against the server value rather than a replacement.
 */
private fun FeedItem.toCardState(
    overlay: EngagementOverlay,
    posterUrl: String?,
    mediaPages: List<PostCardMediaPage>,
    localPollVotes: Set<String> = emptySet(),
) = PostCardState(
    postId = id,
    authorId = author.id,
    authorHandle = author.username?.let { "@$it" },
    // Real author identity, embedded by the server as of 2026-08-17. This was
    // a truncated user id until the feed carried `author` — resolving it
    // per-row would have been an N+1 fired from inside a scrolling list.
    authorName = author.nameForDisplay,
    text = text,
    timestamp = formatRelativeTime(createdAt),
    postType = postType,
    mediaCount = media.size,
    mediaUrl = posterUrl,
    mediaAspectRatio = media.firstOrNull()?.aspectRatio() ?: DEFAULT_MEDIA_ASPECT,
    // Slice C / C-CLB-3. The feed is where most images are seen, so this is
    // the surface where a dropped description costs the most.
    mediaContentDescription = media.firstOrNull()?.contentDescription,
    // The ordered carousel — Creator Studio P0-A.
    mediaPages = mediaPages,
    isPinned = isPinned,
    poll = poll?.let { p ->
        // A vote cast THIS session is layered onto the server's counts the
        // same way engagement taps are — one-step, derived, never accumulated.
        val localOnly = localPollVotes - p.viewerVotedOptionIds.toSet()
        PostCardPoll(
            options = p.options.map { option ->
                PostCardPollOption(
                    id = option.id,
                    label = option.label,
                    voteCount = option.voteCount + if (option.id in localOnly) 1 else 0,
                    percentage = option.percentage,
                )
            },
            totalVotes = p.totalVotes + localOnly.size,
            votedOptionIds = p.viewerVotedOptionIds.toSet() + localPollVotes,
            hasEnded = p.hasEnded,
        )
    },
    actions = PostActionState(
        // Server value plus at most a one-step correction for local intent.
        // Derived rather than accumulated, so repeated taps cannot drift the
        // number and an unlike at zero cannot go negative.
        likeCount = overlay.likeCountOr(counts.likes, viewer.hasReacted),
        commentCount = counts.comments,
        repostCount = overlay.repostCountOr(counts.reposts, viewer.hasReposted),
        hasReacted = overlay.reactedOr(viewer.hasReacted),
        hasReposted = overlay.repostedOr(viewer.hasReposted),
        isBookmarked = overlay.bookmarkedOr(viewer.isBookmarked),
        canRepost = isRepostable,
    ),
)

@Preview(name = "Feed — empty", showBackground = true, heightDp = 400)
@Composable
private fun FeedEmptyPreview() {
    UsTheme {
        UsEmptyState(
            title = "Nothing here yet",
            detail = "Posts from people you follow will show up here.",
        )
    }
}

/**
 * Aspect ratio from the server's real dimensions.
 *
 * Guards against a zero height: a still-processing asset can report 0x0, and
 * dividing by it yields Infinity, which Compose resolves to a zero-height box
 * that then jumps to full size when the real frame arrives.
 */
internal fun FeedMedia.aspectRatio(): Float =
    if (width > 0 && height > 0) width.toFloat() / height.toFloat() else DEFAULT_MEDIA_ASPECT

/**
 * The notification bell, with its unread badge — Slice D.
 *
 * ## WHY THE BADGE IS A COUNT AND NOT A DOT
 *
 * A dot says "something happened". A number says how much, which is what
 * decides whether the user opens it now or later. Above 99 the badge shows
 * "99+": the exact number stops being useful long before it stops being
 * renderable, and a four-digit badge overflows the icon.
 *
 * The count is refreshed when the feed appears rather than polled. Polling a
 * count on a timer costs a request per interval per user forever, and the feed
 * is looked at often enough that the badge is never meaningfully stale.
 */
@Composable
private fun NotificationsAction(
    onClick: () -> Unit,
    viewModel: UnreadBadgeViewModel = hiltViewModel(),
) {
    val count by viewModel.count.collectAsStateWithLifecycle()

    LaunchedEffect(Unit) { viewModel.refresh() }

    IconButton(
        onClick = onClick,
        modifier = Modifier.semantics {
            // The badge is decorative to a screen reader; the COUNT belongs in
            // the button's own description, because "Notifications" followed by
            // a detached "3" is not a sentence.
            contentDescription = when {
                count <= 0 -> "Notifications"
                count == 1 -> "Notifications, 1 unread"
                else -> "Notifications, $count unread"
            }
        },
    ) {
        // Momentum's white 16dp badge with the count in the deep accent red.
        UsBadgedIcon(icon = UsIcons.Notifications, count = count)
    }
}
