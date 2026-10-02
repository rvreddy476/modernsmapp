package com.us.android.feature.tube.ui.watch

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.feed.data.VideoLibraryState
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.model.ChannelSubscription
import com.us.android.core.model.FeedItem
import com.us.android.core.ui.formatCount
import com.us.android.feature.tube.data.SeriesEpisode
import com.us.android.feature.tube.data.SeriesInfo
import com.us.android.feature.tube.ui.channel.SubscribeControl
import com.us.android.feature.tube.ui.home.VideoRow
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.videoMetaLine

/** The row of actions under a video, in order: the web watch page's row. */
enum class WatchActionKind { LIKE, DISLIKE, WATCH_LATER, ADD_TO_COLLECTION, SAVE, MORE }

/** One control: what it reads and whether it is lit (the viewer has done it). */
data class WatchAction(val kind: WatchActionKind, val label: String, val lit: Boolean = false)

/** What the viewer has done to this video, as the row draws it: this session's taps over the server's values. */
data class WatchViewerState(
    val liked: Boolean,
    val disliked: Boolean,
    val queued: Boolean,
    val saved: Boolean,
)

/**
 * The viewer's state for [item]: each local value when there is one (a tap
 * this session, or the post detail's answer), the row's own otherwise. Pure,
 * so "reopened and still saved" is a table test.
 */
fun watchViewerState(item: FeedItem, overlay: EngagementOverlay, library: VideoLibraryState) = WatchViewerState(
    liked = overlay.reactedOr(item.viewer.hasReacted),
    disliked = library.dislikedOr(item.id, item.viewer.hasDisliked),
    queued = library.queuedOr(item.id, item.viewer.isQueued),
    saved = overlay.bookmarkedOr(item.viewer.isBookmarked),
)

/**
 * Like · Dislike · Watch later · Add to collection · Save · More
 * (founder, 2026-10-02: the same row and the same words as the web's watch
 * page, RUTUBE's words). Like carries its count as the label when there is
 * one. Every control is always there; a lit one says so.
 *
 * Comment left this row (the "Comments" line under the description opens
 * them, as the web's section does) and Share moved into More, where the web
 * has it. Save is the one control the web's row does not have: the web saves
 * a long video through "Add to collection" only, while the app also keeps
 * the bookmark and its "Saved" list, so the control stays.
 */
fun watchActions(likes: Int, viewer: WatchViewerState): List<WatchAction> = listOf(
    WatchAction(WatchActionKind.LIKE, countLabel(likes, "Like"), lit = viewer.liked),
    WatchAction(WatchActionKind.DISLIKE, "Dislike", lit = viewer.disliked),
    WatchAction(WatchActionKind.WATCH_LATER, "Watch later", lit = viewer.queued),
    WatchAction(WatchActionKind.ADD_TO_COLLECTION, "Add to collection"),
    WatchAction(WatchActionKind.SAVE, if (viewer.saved) "Saved" else "Save", lit = viewer.saved),
    WatchAction(WatchActionKind.MORE, "More"),
)

/**
 * What a screen reader hears for the control: its label, except where the
 * tap UNDOES something, which is then named ("Remove like"), and Like, whose
 * label is a number.
 */
fun WatchAction.description(): String = when (kind) {
    WatchActionKind.LIKE -> if (lit) "Remove like" else "Like"
    WatchActionKind.DISLIKE -> if (lit) "Remove dislike" else "Dislike"
    WatchActionKind.WATCH_LATER -> if (lit) "Remove from Watch later" else "Watch later"
    WatchActionKind.SAVE -> if (lit) "Remove from Saved" else "Save"
    WatchActionKind.ADD_TO_COLLECTION, WatchActionKind.MORE -> label
}

private fun countLabel(count: Int, noun: String): String = if (count > 0) formatCount(count) else noun

/** Every callback the details column makes, hoisted once. */
// One parameter per action: the bundle IS the parameter list.
@Suppress("LongParameterList")
class WatchDetailsActions(
    val onOpenAuthor: (String) -> Unit,
    /** Keyed by the channel ref ([subscribeRef]), not the author: the graph is a map of channels. */
    val onSubscribe: (channelId: String) -> Unit,
    val onUnsubscribe: (channelId: String) -> Unit,
    val onToggleNotify: (channelId: String) -> Unit,
    val onReact: (FeedItem) -> Unit,
    val onDislike: (FeedItem) -> Unit,
    val onWatchLater: (FeedItem) -> Unit,
    /** Opens the collection picker for the video. */
    val onAddToCollection: (FeedItem) -> Unit,
    val onBookmark: (FeedItem) -> Unit,
    val onComment: (postId: String) -> Unit,
    val onMore: (FeedItem) -> Unit,
    val onOpenVideo: (FeedItem) -> Unit,
    /** An episode row was tapped: by post id, because an episode is not a row the queue knows. */
    val onOpenEpisode: (postId: String) -> Unit,
)

/** The author row's subscription state, as the screen resolved it for this video's channel. */
data class WatchSubscription(
    val edge: ChannelSubscription?,
    val offersSubscribe: Boolean,
    val busy: Boolean,
)

/**
 * What sits under the player, top to bottom: the title and its line, the
 * author row with Subscribe (or Subscribed and the bell), the action row,
 * the description (three lines, then "more"), the comments row, "In this
 * series" when the video is an episode, and "Up next".
 */
@Suppress("LongParameterList")
fun LazyListScope.watchDetails(
    item: FeedItem,
    /** The like count with this session's tap layered in. */
    likes: Int,
    viewer: WatchViewerState,
    subscription: WatchSubscription,
    upNext: List<FeedItem>,
    series: SeriesInfo?,
    thumbFor: (FeedItem) -> VideoThumb,
    actions: WatchDetailsActions,
) {
    item(key = "title") { TitleBlock(item) }
    item(key = "author") { AuthorRow(item, subscription, actions) }
    item(key = "actions") { ActionRow(item, watchActions(likes, viewer), actions) }
    if (item.text.isNotBlank()) item(key = "description") { Description(item) }
    if (!item.controls.noComments) {
        item(key = "comments") { CommentsRow(count = item.counts.comments, onClick = { actions.onComment(item.id) }) }
    }
    if (series != null && series.episodes.isNotEmpty()) {
        item(key = "series") { SectionTitle(seriesTitle(series)) }
        items(series.episodes.sortedBy { it.episodeNum }, key = { "episode:${it.postId}" }) { episode ->
            EpisodeRow(
                episode = episode,
                current = episode.postId == item.id,
                onClick = { actions.onOpenEpisode(episode.postId) },
            )
        }
    }
    if (upNext.isNotEmpty()) {
        item(key = "up-next") { SectionTitle("Up next") }
        items(upNext, key = { "next:${it.id}" }) { next ->
            VideoRow(item = next, thumb = thumbFor(next), onClick = { actions.onOpenVideo(next) })
        }
    }
}

@Composable
private fun TitleBlock(item: FeedItem) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .padding(top = UsTheme.spacing.xl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = item.title.ifBlank { item.text }.ifBlank { "Untitled video" },
            style = MaterialTheme.typography.titleMedium,
            fontSize = TITLE_SIZE,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.testTag("watch_title"),
        )
        Text(
            text = videoMetaLine(null, item.createdAt, item.counts.views),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

@Composable
private fun AuthorRow(item: FeedItem, subscription: WatchSubscription, actions: WatchDetailsActions) {
    val open = { actions.onOpenAuthor(item.author.id) }
    val channelId = subscribeRef(item)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(
            name = item.creatorName,
            seed = item.channel?.userId ?: item.author.id,
            size = UsAvatarSize.Post,
            imageUrl = item.channel?.avatarUrl,
            modifier = Modifier.clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = open,
            ),
        )
        // The channel's name and @handle when the row carries a channel
        // (Tube, 2026-09-05); the author's display name otherwise.
        Column(
            modifier = Modifier
                .weight(1f)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = open,
                )
                .semantics { role = Role.Button }
                .testTag("watch_author"),
        ) {
            Text(
                text = item.creatorName,
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            item.creatorHandle?.let { handle ->
                Text(
                    text = handle,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        // The channel page's control, not a Follow of this row's own: a
        // subscribe is follow plus notify made by the server, and the watch
        // screen offering a bare follow beside it would be two edges for one
        // channel (founder, 2026-09-12).
        SubscribeControl(
            channelName = item.creatorName,
            subscription = subscription.edge,
            offersSubscribe = subscription.offersSubscribe,
            busy = subscription.busy,
            onSubscribe = { actions.onSubscribe(channelId) },
            onUnsubscribe = { actions.onUnsubscribe(channelId) },
            onToggleNotify = { actions.onToggleNotify(channelId) },
            tagPrefix = "watch",
        )
    }
}

/**
 * The controls as a row of 36dp pills that scrolls sideways when it is wider
 * than the phone (the web's row on a narrow screen): Like and Dislike share
 * one pill with a hairline between them, the rest are one pill each, and
 * More is the glyph alone.
 */
@Composable
private fun ActionRow(item: FeedItem, controls: List<WatchAction>, actions: WatchDetailsActions) {
    val vote = controls.filter { it.kind == WatchActionKind.LIKE || it.kind == WatchActionKind.DISLIKE }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .testTag("watch_actions"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        VotePill(vote = vote, onTap = { control -> actions.onTap(control.kind, item) })
        controls.filterNot { it in vote }.forEach { control ->
            ActionPill(control = control, onClick = { actions.onTap(control.kind, item) })
        }
    }
    HorizontalDivider(
        color = UsTheme.extended.borderSubtle,
        modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
    )
}

/** The control → callback table, in one place. */
private fun WatchDetailsActions.onTap(kind: WatchActionKind, item: FeedItem) = when (kind) {
    WatchActionKind.LIKE -> onReact(item)
    WatchActionKind.DISLIKE -> onDislike(item)
    WatchActionKind.WATCH_LATER -> onWatchLater(item)
    WatchActionKind.ADD_TO_COLLECTION -> onAddToCollection(item)
    WatchActionKind.SAVE -> onBookmark(item)
    WatchActionKind.MORE -> onMore(item)
}

/** Like with its count, a hairline, Dislike: one pill, two targets. A lit Like is the danger red, as on the web. */
@Composable
private fun VotePill(vote: List<WatchAction>, onTap: (WatchAction) -> Unit) {
    Row(
        modifier = Modifier
            .height(PILL_HEIGHT)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.fillSubtle),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        vote.forEachIndexed { index, control ->
            if (index > 0) {
                Box(
                    modifier = Modifier
                        .size(width = HAIRLINE, height = DIVIDER_HEIGHT)
                        .background(UsTheme.extended.borderMedium),
                )
            }
            val like = control.kind == WatchActionKind.LIKE
            val tint = if (like && control.lit) UsTheme.extended.statusDanger else UsTheme.extended.textPrimary
            Row(
                modifier = Modifier
                    .height(PILL_HEIGHT)
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .then(if (!like && control.lit) Modifier.background(UsTheme.extended.fillStrong) else Modifier)
                    .pressScale { onTap(control) }
                    .padding(horizontal = UsTheme.spacing.l)
                    .pillSemantics(control),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                Icon(
                    imageVector = control.icon(),
                    contentDescription = null,
                    tint = tint,
                    modifier = Modifier.size(PILL_GLYPH),
                )
                // Like reads its count (or "Like"); Dislike is the glyph alone, as on the web.
                if (like) PillLabel(control.label)
            }
        }
    }
}

/** One labelled pill; More is the glyph alone. A lit pill sits on the stronger fill. */
@Composable
private fun ActionPill(control: WatchAction, onClick: () -> Unit) {
    val glyphOnly = control.kind == WatchActionKind.MORE
    Row(
        modifier = Modifier
            .height(PILL_HEIGHT)
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(if (control.lit) UsTheme.extended.fillStrong else UsTheme.extended.fillSubtle)
            .pressScale(onClick)
            .padding(horizontal = if (glyphOnly) UsTheme.spacing.l else UsTheme.spacing.xl)
            .pillSemantics(control),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Icon(
            imageVector = control.icon(),
            contentDescription = null,
            tint = UsTheme.extended.textPrimary,
            modifier = Modifier.size(PILL_GLYPH),
        )
        if (!glyphOnly) PillLabel(control.label)
    }
}

@Composable
private fun PillLabel(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        fontWeight = FontWeight.SemiBold,
        color = UsTheme.extended.textPrimary,
        maxLines = 1,
    )
}

/** One button to a screen reader: what the tap does, and whether it is on. */
private fun Modifier.pillSemantics(control: WatchAction): Modifier =
    clearAndSetSemantics {
        role = Role.Button
        contentDescription = control.description()
        selected = control.lit
    }.testTag("watch_action:${control.kind.name.lowercase()}")

/** Lucide, the web's glyphs: heart · thumbs-down · list-video · folder-plus · bookmark · more. */
private fun WatchAction.icon(): ImageVector = when (kind) {
    WatchActionKind.LIKE -> if (lit) UsIcons.HeartFilled else UsIcons.HeartOutline
    WatchActionKind.DISLIKE -> UsIcons.ThumbsDown
    WatchActionKind.WATCH_LATER -> UsIcons.ListVideo
    WatchActionKind.ADD_TO_COLLECTION -> UsIcons.FolderPlus
    WatchActionKind.SAVE -> if (lit) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline
    WatchActionKind.MORE -> UsIcons.More
}

@Preview
@Composable
private fun ActionRowPreview() {
    UsTheme {
        val viewer = WatchViewerState(liked = true, disliked = false, queued = true, saved = false)
        val controls = watchActions(likes = 8_800, viewer = viewer)
        Row(
            modifier = Modifier.background(UsTheme.extended.bgCanvas),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            VotePill(vote = controls.take(2), onTap = {})
            controls.drop(2).forEach { ActionPill(control = it, onClick = {}) }
        }
    }
}

/** Three lines, then "more" unfolds the rest in place; "less" folds it back. */
@Composable
private fun Description(item: FeedItem) {
    var expanded by rememberSaveable(item.id) { mutableStateOf(false) }
    var overflowed by remember(item.id) { mutableStateOf(false) }
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Text(
            text = item.text,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            maxLines = if (expanded) Int.MAX_VALUE else DESCRIPTION_LINES,
            overflow = TextOverflow.Ellipsis,
            onTextLayout = { if (!expanded) overflowed = it.hasVisualOverflow },
            modifier = Modifier.testTag("watch_description"),
        )
        if (overflowed || expanded) {
            Text(
                text = if (expanded) "less" else "more",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .clickable(
                        interactionSource = remember { MutableInteractionSource() },
                        indication = null,
                    ) { expanded = !expanded }
                    .semantics { role = Role.Button }
                    .testTag("watch_description_toggle"),
            )
        }
    }
}

/** "Comments · N" with a chevron; opens the shared sheet. */
@Composable
private fun CommentsRow(count: Int, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .semantics { role = Role.Button }
            .testTag("watch_comments"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = if (count > 0) "Comments · ${formatCount(count)}" else "Comments",
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        Icon(
            imageVector = UsIcons.ChevronRight,
            contentDescription = null,
            tint = UsTheme.extended.textMuted,
            modifier = Modifier.size(CHEVRON),
        )
    }
}

/** "In this series", with the series' name after it when it has one. */
private fun seriesTitle(series: SeriesInfo): String =
    if (series.title.isBlank()) "In this series" else "In this series · ${series.title}"

/**
 * One episode: its number, its title, and a play mark on the one playing.
 * The current row is still a target (the ViewModel ignores a re-open) so
 * every row reads the same to a screen reader; `selected` says which.
 */
@Composable
private fun EpisodeRow(episode: SeriesEpisode, current: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                selected = current
            }
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("watch_episode:${episode.postId}"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text(
            text = "${episode.episodeNum}",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = if (current) UsTheme.extended.accentSolid else UsTheme.extended.textMuted,
            modifier = Modifier.width(EPISODE_NUMBER_WIDTH),
        )
        Text(
            text = episode.title.ifBlank { "Episode ${episode.episodeNum}" },
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = if (current) FontWeight.SemiBold else FontWeight.Normal,
            color = if (current) UsTheme.extended.textPrimary else UsTheme.extended.textSecondary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.weight(1f),
        )
        if (current) {
            Icon(
                imageVector = UsIcons.Play,
                contentDescription = "Now playing",
                tint = UsTheme.extended.accentSolid,
                modifier = Modifier.size(CHEVRON),
            )
        }
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        color = UsTheme.extended.textPrimary,
        modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
    )
}

private const val DESCRIPTION_LINES = 3
private val TITLE_SIZE = 18.sp
private val PILL_HEIGHT = 36.dp
private val PILL_GLYPH = 17.dp
private val HAIRLINE = 1.dp
private val DIVIDER_HEIGHT = 18.dp
private val CHEVRON = 18.dp
private val EPISODE_NUMBER_WIDTH = 24.dp
