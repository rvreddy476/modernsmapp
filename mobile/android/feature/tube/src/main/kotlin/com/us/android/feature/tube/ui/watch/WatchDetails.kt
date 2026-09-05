package com.us.android.feature.tube.ui.watch

import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsFollowButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.bookmarkedOr
import com.us.android.core.engagement.data.likeCountOr
import com.us.android.core.engagement.data.reactedOr
import com.us.android.core.feed.data.VideoThumb
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedPostControls
import com.us.android.core.ui.formatCount
import com.us.android.feature.tube.ui.home.VideoRow
import com.us.android.feature.tube.ui.pressScale
import com.us.android.feature.tube.ui.videoMetaLine

/** The row of actions under a video, in order. Which appear is [watchActions]'s rule. */
enum class WatchActionKind { LIKE, COMMENT, SHARE, SAVE, MORE }

data class WatchAction(val kind: WatchActionKind, val label: String)

/**
 * Like · Comment · Share · Save · More, honouring the author's switches by
 * HIDING: no comments hides Comment, `hide_share` hides Share. Like and
 * Comment carry their count as the label when there is one.
 */
fun watchActions(controls: FeedPostControls, likes: Int, comments: Int, saved: Boolean): List<WatchAction> =
    buildList {
        add(WatchAction(WatchActionKind.LIKE, countLabel(likes, "Like")))
        if (!controls.noComments) add(WatchAction(WatchActionKind.COMMENT, countLabel(comments, "Comment")))
        if (!controls.hideShare) add(WatchAction(WatchActionKind.SHARE, "Share"))
        add(WatchAction(WatchActionKind.SAVE, if (saved) "Saved" else "Save"))
        add(WatchAction(WatchActionKind.MORE, "More"))
    }

private fun countLabel(count: Int, noun: String): String = if (count > 0) formatCount(count) else noun

/** Every callback the details column makes, hoisted once. */
// One parameter per action: the bundle IS the parameter list.
@Suppress("LongParameterList")
class WatchDetailsActions(
    val onOpenAuthor: (String) -> Unit,
    val onFollow: (authorId: String) -> Unit,
    val onReact: (postId: String, serverReacted: Boolean) -> Unit,
    val onBookmark: (postId: String, serverBookmarked: Boolean) -> Unit,
    val onComment: (postId: String) -> Unit,
    val onShare: (FeedItem) -> Unit,
    val onMore: (FeedItem) -> Unit,
    val onOpenVideo: (FeedItem) -> Unit,
)

/**
 * What sits under the player, top to bottom: the title and its line, the
 * author row with Follow, the action row, the description (three lines,
 * then "more"), the comments row, and "Up next".
 */
@Suppress("LongParameterList")
fun LazyListScope.watchDetails(
    item: FeedItem,
    overlay: EngagementOverlay,
    offersFollow: Boolean,
    upNext: List<FeedItem>,
    thumbFor: (FeedItem) -> VideoThumb,
    actions: WatchDetailsActions,
) {
    item(key = "title") { TitleBlock(item) }
    item(key = "author") { AuthorRow(item, offersFollow, actions) }
    item(key = "actions") { ActionRow(item, overlay, actions) }
    if (item.text.isNotBlank()) item(key = "description") { Description(item) }
    if (!item.controls.noComments) {
        item(key = "comments") { CommentsRow(count = item.counts.comments, onClick = { actions.onComment(item.id) }) }
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
private fun AuthorRow(item: FeedItem, offersFollow: Boolean, actions: WatchDetailsActions) {
    val open = { actions.onOpenAuthor(item.author.id) }
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
        if (offersFollow) {
            UsFollowButton(onClick = { actions.onFollow(item.author.id) }, modifier = Modifier.testTag("watch_follow"))
        }
    }
}

/** Five (or fewer) controls, evenly spaced across the width, each a glyph over its label. */
@Composable
private fun ActionRow(item: FeedItem, overlay: EngagementOverlay, actions: WatchDetailsActions) {
    val reacted = overlay.reactedOr(item.viewer.hasReacted)
    val saved = overlay.bookmarkedOr(item.viewer.isBookmarked)
    val controls = watchActions(
        controls = item.controls,
        likes = overlay.likeCountOr(item.counts.likes, item.viewer.hasReacted),
        comments = item.counts.comments,
        saved = saved,
    )
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.m)
            .testTag("watch_actions"),
    ) {
        controls.forEach { control ->
            ActionButton(control, item, reacted, saved, actions, modifier = Modifier.weight(1f))
        }
    }
    HorizontalDivider(
        color = UsTheme.extended.borderSubtle,
        modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m),
    )
}

@Suppress("LongParameterList")
@Composable
private fun ActionButton(
    control: WatchAction,
    item: FeedItem,
    reacted: Boolean,
    saved: Boolean,
    actions: WatchDetailsActions,
    modifier: Modifier = Modifier,
) {
    val (icon, tint, onClick) = when (control.kind) {
        WatchActionKind.LIKE -> Triple(
            if (reacted) UsIcons.HeartFilled else UsIcons.HeartOutline,
            if (reacted) UsTheme.extended.liveRed else UsTheme.extended.textPrimary,
            { actions.onReact(item.id, item.viewer.hasReacted) },
        )
        WatchActionKind.COMMENT -> Triple(UsIcons.Comment, UsTheme.extended.textPrimary, { actions.onComment(item.id) })
        WatchActionKind.SHARE -> Triple(UsIcons.Share, UsTheme.extended.textPrimary, { actions.onShare(item) })
        WatchActionKind.SAVE -> Triple(
            if (saved) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
            if (saved) UsTheme.extended.statusWarning else UsTheme.extended.textPrimary,
            { actions.onBookmark(item.id, item.viewer.isBookmarked) },
        )
        WatchActionKind.MORE -> Triple(UsIcons.More, UsTheme.extended.textPrimary, { actions.onMore(item) })
    }
    ActionGlyph(icon = icon, tint = tint, label = control.label, onClick = onClick, modifier = modifier)
}

@Composable
private fun ActionGlyph(
    icon: ImageVector,
    tint: Color,
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier
            .pressScale(onClick)
            .padding(vertical = UsTheme.spacing.s)
            .clearAndSetSemantics {
                role = Role.Button
                contentDescription = label
            }
            .testTag("watch_action:${label.lowercase()}"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(ACTION_GLYPH))
        Text(
            text = label,
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textSecondary,
            maxLines = 1,
        )
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
private val ACTION_GLYPH = 24.dp
private val CHEVRON = 18.dp
