package com.us.android.core.feed.ui.more

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.analytics.AnalyticsSurface
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.feed.data.videoMediaId
import com.us.android.core.feed.offline.offlineMoreState
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.UsLongVideoMoreState
import com.us.android.core.ui.UsOfflineMoreState
import com.us.android.core.ui.UsPostDeleteState
import com.us.android.core.ui.UsPostDontRecommendState
import com.us.android.core.ui.UsPostMoreCallbacks
import com.us.android.core.ui.UsPostMoreFollowRow
import com.us.android.core.ui.UsPostMoreSheet
import com.us.android.core.ui.UsPostMoreState
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsReelMoreState
import com.us.android.core.ui.UsReelQuality
import com.us.android.core.ui.postShareLink

/**
 * The "more" sheet, bound to [PostMoreViewModel] for one [item].
 *
 * Every feed surface mounts exactly this when its ⋮ is tapped, so the
 * mapping from a row to a ViewModel call exists once. [onShare] stays with
 * the host because the system chooser needs an Activity context the
 * ViewModel must not hold.
 */
@Suppress("LongParameterList")
@Composable
fun PostMoreSheetHost(
    item: FeedItem,
    overlay: EngagementOverlay,
    followEdge: FollowStatus?,
    ownUserId: String,
    onShare: (FeedItem) -> Unit,
    onDismiss: () -> Unit,
    viewModel: PostMoreViewModel,
    /**
     * Set by the two VIDEO hosts, Reels and Tube's watch screen (2026-10-02:
     * one menu for both): what Description unfolds and what Quality offers.
     */
    reel: UsReelMoreState? = null,
    onClearScreen: () -> Unit = {},
    /** A rendition was picked from the Quality row; the host applies it to its player. */
    onSelectQuality: (UsReelQuality) -> Unit = {},
    /** Set by Reels alone: "Use this sound" was tapped on [item]. */
    onUseSound: (FeedItem) -> Unit = {},
    /**
     * Overrides whether the post reads as a suggestion. Null derives it from
     * the row's reason, as every feed does; Tube's watch screen passes false —
     * a video the viewer chose to open is not something to say "Interested"
     * about.
     */
    suggested: Boolean? = null,
    /**
     * Where the sheet was opened from, for analytics.
     *
     * The sheet is shared by the feed, reels and Tube's watch screen, so it
     * cannot derive its own surface. Defaulting to `feed` covers the two feed
     * surfaces; Tube passes `posttube`. Guessing instead would put every
     * "not interested" on a long video into the feed's numbers.
     */
    surface: AnalyticsSurface = AnalyticsSurface.FEED,
    /**
     * Set by Tube's watch screen alone (2026-10-02): the sheet then says
     * "video" where it says "reel" or "post", and names the channel in the
     * block confirmation. The ROWS are the reel's.
     */
    longVideo: UsLongVideoMoreState? = null,
    /**
     * A block was confirmed and sent. The watch screen leaves the video, as
     * the web does; every other host has nothing to add.
     */
    onBlocked: () -> Unit = {},
    /**
     * Set by the two VIDEO hosts (2026-10-02): this host keeps offline
     * copies, so the sheet offers Save offline / Cancel / Remove. The list
     * of copies is not a row of this sheet (2026-10-17).
     */
    offlineCopies: Boolean = false,
    /** The video hosts (T-32): a speed was picked from Playback speed; the host applies it to its player. */
    onSelectSpeed: (Float) -> Unit = {},
    /** The video hosts (T-32): a caption track was picked by language, or null for Off. */
    onSelectCaption: (String?) -> Unit = {},
    /** Reels alone (T-32): "Auto scroll" was tapped; the host flips and stores it. */
    onToggleAutoScroll: () -> Unit = {},
    /**
     * The video hosts (2026-10-21): an audio track was picked by id from the
     * "Audio track" row ([UsReelMoreState.audioTracks] feeds it); the host
     * stores the language and switches its player where it is.
     */
    onSelectAudioTrack: (String) -> Unit = {},
    /**
     * The video hosts (2026-10-21): the owner's "Edit". Non-null means this
     * host HAS the edit sheet, which is what offers the row — the web's
     * `can.edit`. The sheet has left when it fires.
     */
    onEdit: ((FeedItem) -> Unit)? = null,
    /**
     * The video hosts (2026-10-21): the owner's "Audio tracks", the dub
     * manager. Non-null offers the row, for a video that has a media id.
     */
    onManageAudio: ((FeedItem) -> Unit)? = null,
    /**
     * A FEED host's owner and viewer rows (2026-10-24): Pin to profile on the
     * viewer's own post, Mute on anyone else's. Off on the video hosts, whose
     * menu is the video one.
     */
    postTools: Boolean = false,
) {
    val report by viewModel.report.collectAsStateWithLifecycle()
    val delete by viewModel.delete.collectAsStateWithLifecycle()
    val dontRecommend by viewModel.dontRecommend.collectAsStateWithLifecycle()
    val offlineState by viewModel.offlineState.collectAsStateWithLifecycle()
    val offlineRefusal by viewModel.offlineRefusal.collectAsStateWithLifecycle()
    val pins by viewModel.pins.collectAsStateWithLifecycle()
    LaunchedEffect(item.id, surface) {
        viewModel.onSurface(surface)
        viewModel.opened()
    }

    val callbacks = remember(
        item,
        viewModel,
        onShare,
        onClearScreen,
        onSelectQuality,
        onUseSound,
        onBlocked,
        onSelectSpeed,
        onSelectCaption,
        onToggleAutoScroll,
        onSelectAudioTrack,
        onEdit,
        onManageAudio,
    ) {
        UsPostMoreCallbacks(
            onToggleSave = { viewModel.toggleSave(item) },
            onShare = { onShare(item) },
            onInterested = { viewModel.interested(item) },
            onNotInterested = { viewModel.notInterested(item) },
            onDontRecommend = { viewModel.dontRecommend(item) },
            onFollow = { viewModel.follow(item.author.id) },
            onUnfollow = { viewModel.unfollow(item.author.id) },
            onBlock = {
                viewModel.block(item)
                onBlocked()
            },
            onReport = { reason, details -> viewModel.report(item, reason, details) },
            onDelete = { viewModel.delete(item) },
            onClearScreen = onClearScreen,
            onSelectQuality = onSelectQuality,
            onUseSound = { onUseSound(item) },
            onSaveOffline = { viewModel.saveOffline(item) },
            onRemoveOffline = { viewModel.removeOffline(item) },
            onSelectSpeed = onSelectSpeed,
            onSelectCaption = onSelectCaption,
            onToggleAutoScroll = onToggleAutoScroll,
            onSelectAudioTrack = onSelectAudioTrack,
            onEdit = { onEdit?.invoke(item) },
            onManageAudio = { onManageAudio?.invoke(item) },
            onTogglePin = { viewModel.togglePin(item) },
            onMute = { viewModel.mute(item) },
        )
    }
    UsPostMoreSheet(
        state = item.toMoreState(
            overlay = overlay,
            followEdge = followEdge,
            ownUserId = ownUserId,
            report = report,
            delete = delete,
            reel = reel?.withOwnerSheets(item, edit = onEdit != null, manageAudio = onManageAudio != null),
            dontRecommend = dontRecommend,
            suggested = suggested,
            longVideo = longVideo,
            // A video, on a host that keeps offline copies: the offline row.
            offline = if (reel != null && offlineCopies) {
                offlineMoreState(
                    item = item,
                    isOwn = ownUserId.isNotBlank() && item.author.id == ownUserId,
                    entry = offlineState.copies[item.id],
                ).copy(refusal = offlineRefusal)
            } else {
                null
            },
        ).let { state ->
            // A feed post (2026-10-24): Edit post where the host has the
            // sheet, Pin / Unpin on the owner's own, Mute on anyone else's.
            if (reel != null || longVideo != null) {
                state
            } else {
                state.copy(
                    canEditPost = onEdit != null,
                    pinned = if (postTools) pins[item.id] ?: item.isPinned else null,
                    canMute = postTools,
                )
            }
        },
        callbacks = callbacks,
        onDismiss = onDismiss,
    )
}

/**
 * The owner's two rows (2026-10-21) exist only where the host has their
 * sheets — the web's `can.edit` and `can.manageAudio` — and the dub manager
 * only for a video with a media id to hang tracks off.
 */
private fun UsReelMoreState.withOwnerSheets(item: FeedItem, edit: Boolean, manageAudio: Boolean): UsReelMoreState =
    copy(canEdit = edit, canManageAudio = manageAudio && item.videoMediaId() != null)

/**
 * The sheet's state for one row: the server's values with this session's
 * bookmark tap layered in, and the relationship row decided by the graph.
 */
@Suppress("LongParameterList")
fun FeedItem.toMoreState(
    overlay: EngagementOverlay,
    followEdge: FollowStatus?,
    ownUserId: String,
    report: UsPostReportState = UsPostReportState.Idle,
    delete: UsPostDeleteState = UsPostDeleteState.Idle,
    reel: UsReelMoreState? = null,
    dontRecommend: UsPostDontRecommendState = UsPostDontRecommendState.Idle,
    suggested: Boolean? = null,
    longVideo: UsLongVideoMoreState? = null,
    offline: UsOfflineMoreState? = null,
): UsPostMoreState {
    val own = ownUserId.isNotBlank() && author.id == ownUserId
    return UsPostMoreState(
        postId = id,
        username = author.username?.takeIf { it.isNotBlank() } ?: author.nameForDisplay,
        channelName = creatorName,
        isOwnPost = own,
        isBookmarked = overlay.bookmarkedOr(viewer.isBookmarked),
        followRow = if (own) UsPostMoreFollowRow.HIDDEN else moreFollowRow(followEdge),
        reasonText = reasonText,
        // "following" and "connection" are the server's two "you asked for
        // this" reasons; everything else was suggested — unless the host
        // knows better (Tube's watch screen).
        suggested = suggested ?: (reason != "following" && reason != "connection"),
        link = postShareLink(id),
        report = report,
        delete = delete,
        dontRecommend = dontRecommend,
        reel = reel,
        longVideo = longVideo,
        offline = offline,
    )
}

/**
 * Unfollow when the viewer follows (or has asked to — a pending request is
 * undone the same way), Follow when they are known not to, nothing while
 * the edge is unknown. The same "known, not guessed" rule as `offersFollow`.
 */
fun moreFollowRow(edge: FollowStatus?): UsPostMoreFollowRow = when (edge) {
    FollowStatus.FOLLOWING, FollowStatus.REQUESTED -> UsPostMoreFollowRow.UNFOLLOW
    FollowStatus.NONE -> UsPostMoreFollowRow.FOLLOW
    null -> UsPostMoreFollowRow.HIDDEN
}
