package com.us.android.core.feed.ui.share

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.model.FeedItem
import com.us.android.core.model.allowsRepost
import com.us.android.core.ui.UsPostShareCallbacks
import com.us.android.core.ui.UsPostShareSheet

/**
 * The Share sheet for [item] (2026-10-17), wired to its [viewModel]: the
 * conversation picker and the card, the repost, the clipboard line, and
 * [onMore] — the host's system chooser, which the Share action used to open
 * directly.
 *
 * The ViewModel is the HOST's (one per screen, `hiltViewModel()` there), so
 * the screen's own message line can show what the sheet left behind after
 * the sheet has gone.
 */
@Composable
fun PostShareSheetHost(
    item: FeedItem,
    viewModel: PostShareViewModel,
    onMore: (FeedItem) -> Unit,
    onDismiss: () -> Unit,
) {
    // A reel takes no Repost and no Quote (founder, 24 Oct 2026); feed posts and long videos keep them.
    LaunchedEffect(item.id) { viewModel.open(item.id, item.viewer.hasReposted, allowRepost = item.allowsRepost) }
    // A card that went out closes the sheet; the screen says "Sent".
    LaunchedEffect(viewModel) { viewModel.sent.collect { onDismiss() } }

    val model by viewModel.state.collectAsStateWithLifecycle()
    val overlays by viewModel.overlays.collectAsStateWithLifecycle()
    // Until open() has run for this item, the model is the previous post's.
    val current = if (model.postId == item.id) model else PostShareModel(postId = item.id)
    val callbacks = remember(item, viewModel, onMore) {
        UsPostShareCallbacks(
            onOpenPicker = viewModel::openPicker,
            onClosePicker = viewModel::closePicker,
            onQueryChange = viewModel::onQueryChange,
            onSend = viewModel::send,
            onRetryTargets = viewModel::retryConversations,
            onLinkCopied = viewModel::onLinkCopied,
            onToggleRepost = viewModel::toggleRepost,
            onMore = { onMore(item) },
            // Chat parity: avatar multi-select (≤5), an optional note, one Send.
            onToggleTarget = viewModel::toggleTarget,
            onNoteChange = viewModel::onNoteChange,
            onSendSelected = viewModel::sendSelected,
            // Quote repost (2026-10-24).
            onOpenQuote = viewModel::openQuote,
            onQuoteChange = viewModel::onQuoteChange,
            onSendQuote = viewModel::sendQuote,
            onCloseQuote = viewModel::closeQuote,
        )
    }
    UsPostShareSheet(
        state = current.toUiState(viewModel.viewerId, overlays[item.id] ?: EngagementOverlay()),
        callbacks = callbacks,
        onDismiss = onDismiss,
    )
}
