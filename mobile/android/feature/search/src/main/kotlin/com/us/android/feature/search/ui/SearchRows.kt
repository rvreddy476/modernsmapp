package com.us.android.feature.search.ui

import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil3.compose.AsyncImage
import com.us.android.core.common.time.formatDuration
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsFollowButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.offersFollow
import com.us.android.core.model.FollowStatus
import com.us.android.core.ui.formatCount
import com.us.android.feature.search.data.SearchAuthor
import com.us.android.feature.search.data.SearchHit
import java.time.Instant

/** Everything a row can be asked to do; `:app` resolves each through the destinations. */
internal class SearchRowActions(
    val onOpenUser: (SearchHit.User) -> Unit,
    val onFollow: (SearchHit.User) -> Unit,
    val onOpenPost: (SearchHit.Post) -> Unit,
    val onOpenVideo: (SearchHit.Video) -> Unit,
    val onOpenChannel: (SearchHit.Channel) -> Unit,
)

/** What the user rows need to decide on Follow: the viewer, the known edges, the follows in flight. */
internal class FollowFacts(
    val ownUserId: String,
    val edges: Map<String, FollowStatus>,
    val busy: Set<String>,
)

/** The row for a hit, by its kind. */
@Composable
internal fun SearchHitRow(hit: SearchHit, follow: FollowFacts, actions: SearchRowActions, now: Instant) {
    when (hit) {
        is SearchHit.User -> UserRow(
            hit = hit,
            offersFollow = offersFollow(follow.ownUserId, hit.id, follow.edges[hit.id]),
            busy = hit.id in follow.busy,
            onOpen = { actions.onOpenUser(hit) },
            onFollow = { actions.onFollow(hit) },
        )
        is SearchHit.Post -> PostRow(hit = hit, now = now, onOpen = { actions.onOpenPost(hit) })
        is SearchHit.Video -> VideoRow(hit = hit, now = now, onOpen = { actions.onOpenVideo(hit) })
        is SearchHit.Channel -> ChannelRow(hit = hit, onOpen = { actions.onOpenChannel(hit) })
    }
}

/** Avatar, name over `@username`, and Follow at the right end when the viewer is not already following. */
@Composable
private fun UserRow(
    hit: SearchHit.User,
    offersFollow: Boolean,
    busy: Boolean,
    onOpen: () -> Unit,
    onFollow: () -> Unit,
) {
    RowFrame(onClick = onOpen, description = hit.nameForDisplay, testTag = "search_user:${hit.id}") {
        UsAvatar(name = hit.nameForDisplay, seed = hit.id, size = UsAvatarSize.Medium, imageUrl = hit.avatarUrl)
        TwoLines(primary = hit.nameForDisplay, secondary = hit.handle, modifier = Modifier.weight(1f))
        if (offersFollow) {
            UsFollowButton(onClick = onFollow, busy = busy, modifier = Modifier.testTag("search_follow:${hit.id}"))
        }
    }
}

/** The author line — small avatar, name, the time — over two lines of the post's words. */
@Composable
private fun PostRow(hit: SearchHit.Post, now: Instant, onOpen: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .pressScale(onOpen)
            .semantics {
                role = Role.Button
                contentDescription = "Post by ${hit.author.nameForDisplay}"
            }
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.l)
            .testTag("search_post:${hit.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        AuthorLine(author = hit.author, createdAt = hit.createdAt, now = now)
        Text(
            text = hit.title.ifBlank { hit.text }.ifBlank { "Untitled post" },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

/** The still with the length in a pill, then the title and the creator line. */
@Composable
private fun VideoRow(hit: SearchHit.Video, now: Instant, onOpen: () -> Unit) {
    val kind = if (hit.isReel) "Reel" else "Video"
    RowFrame(onClick = onOpen, description = "$kind: ${hit.title}", testTag = "search_video:${hit.id}") {
        Thumbnail(url = hit.thumbnailUrl, durationMs = hit.durationMs, portrait = hit.isReel)
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = hit.title.ifBlank { "Untitled ${kind.lowercase()}" },
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 2,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                text = listOfNotNull(
                    hit.author.nameForDisplay,
                    formatRelativeTime(hit.createdAt, now).takeIf { it.isNotBlank() },
                ).joinToString(" · "),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

/** Avatar, name over `@handle`, and the video count at the end. */
@Composable
private fun ChannelRow(hit: SearchHit.Channel, onOpen: () -> Unit) {
    RowFrame(onClick = onOpen, description = "Channel ${hit.name}", testTag = "search_channel:${hit.id}") {
        UsAvatar(name = hit.name, seed = hit.id, size = UsAvatarSize.Medium, imageUrl = hit.avatarUrl)
        TwoLines(primary = hit.name, secondary = hit.handleForDisplay, modifier = Modifier.weight(1f))
        Text(
            text = videoCountLabel(hit.videoCount),
            style = MaterialTheme.typography.labelMedium,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** "1 video", "12 videos", "1.2K videos". */
internal fun videoCountLabel(count: Int): String = when (count) {
    1 -> "1 video"
    else -> "${formatCount(count)} videos"
}

@Composable
private fun RowFrame(
    onClick: () -> Unit,
    description: String,
    testTag: String,
    content: @Composable RowScope.() -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .pressScale(onClick)
            .semantics {
                role = Role.Button
                contentDescription = description
            }
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.l)
            .testTag(testTag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        content = content,
    )
}

@Composable
private fun TwoLines(primary: String, secondary: String?, modifier: Modifier = Modifier) {
    Column(modifier = modifier) {
        Text(
            text = primary,
            style = MaterialTheme.typography.bodyLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        if (!secondary.isNullOrBlank()) {
            Text(
                text = secondary,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
    }
}

@Composable
private fun AuthorLine(author: SearchAuthor, createdAt: String, now: Instant) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        UsAvatar(
            name = author.nameForDisplay,
            seed = author.id,
            size = UsAvatarSize.Small,
            imageUrl = author.avatarUrl,
        )
        Text(
            text = author.nameForDisplay,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        author.handle?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        formatRelativeTime(createdAt, now).takeIf { it.isNotBlank() }?.let {
            Text(text = "· $it", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
    }
}

/** A still on the raised ground — 16:9 for a video, 9:16 for a reel — with the length bottom-right. */
@Composable
private fun Thumbnail(url: String?, durationMs: Long, portrait: Boolean) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Box(
        modifier = Modifier
            .size(
                width = if (portrait) THUMB_PORTRAIT_WIDTH else THUMB_WIDTH,
                height = if (portrait) THUMB_PORTRAIT_HEIGHT else THUMB_HEIGHT,
            )
            .clip(shape)
            .background(UsTheme.extended.bgRaised),
    ) {
        if (url != null) {
            AsyncImage(
                model = url,
                contentDescription = null,
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        } else {
            Icon(
                imageVector = UsIcons.Play,
                contentDescription = null,
                tint = UsTheme.extended.textMuted,
                modifier = Modifier
                    .align(Alignment.Center)
                    .size(PLACEHOLDER_GLYPH),
            )
        }
        if (durationMs > 0L) {
            Text(
                text = formatDuration(durationMs),
                style = MaterialTheme.typography.labelSmall,
                fontSize = PILL_TEXT,
                fontWeight = FontWeight.SemiBold,
                color = Color.White,
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .padding(PILL_MARGIN)
                    .background(Color.Black.copy(alpha = PILL_ALPHA), RoundedCornerShape(PILL_RADIUS))
                    .padding(horizontal = PILL_PAD_H, vertical = PILL_PAD_V),
            )
        }
    }
}

/**
 * No ripple. The press is shown by the row dipping to 97% on a spring —
 * the app's one touch, the same as Tube's and the feed card's.
 */
@Composable
internal fun Modifier.pressScale(onClick: () -> Unit): Modifier {
    val interaction = remember { MutableInteractionSource() }
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = PRESS_STIFFNESS),
        label = "searchPress",
    )
    return this
        .clickable(interactionSource = interaction, indication = null, onClick = onClick)
        .graphicsLayer {
            scaleX = scale
            scaleY = scale
        }
}

private const val PRESS_SCALE = 0.97f
private const val PRESS_STIFFNESS = 1200f
private const val PILL_ALPHA = 0.7f
private val THUMB_WIDTH = 120.dp
private val THUMB_HEIGHT = 68.dp
private val THUMB_PORTRAIT_WIDTH = 56.dp
private val THUMB_PORTRAIT_HEIGHT = 84.dp
private val PLACEHOLDER_GLYPH = 20.dp
private val PILL_MARGIN = 4.dp
private val PILL_RADIUS = 4.dp
private val PILL_PAD_H = 4.dp
private val PILL_PAD_V = 1.dp
private val PILL_TEXT = 10.sp
