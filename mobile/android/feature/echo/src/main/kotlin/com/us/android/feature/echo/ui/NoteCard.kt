package com.us.android.feature.echo.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.NoteAuthor
import com.us.android.feature.echo.data.NoteMedia
import com.us.android.feature.echo.data.VerificationMark
import com.us.android.feature.echo.data.compactCount
import com.us.android.feature.echo.data.shortAge
import java.time.Instant

/**
 * One note, X's card: the avatar on the left; name, mark, @handle · age and
 * `···` on the first line; the text with its links; up to 4 pictures or a
 * GIF, or a poll; the quoted note; then the action row: replies, reposts,
 * likes, views, bookmark, share.
 */
@Composable
fun NoteCard(
    note: Note,
    mediaUrls: Map<String, String>,
    callbacks: NoteCallbacks,
    modifier: Modifier = Modifier,
    now: Instant = Instant.now(),
    /** Kept for the profile tab's older callers: a focused card does not tap through. */
    focused: Boolean = false,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .then(if (focused) Modifier else Modifier.clickable { callbacks.onOpen(note) })
            .padding(start = UsTheme.spacing.xxl, end = UsTheme.spacing.l, top = UsTheme.spacing.l)
            .testTag("echo_note:${note.id}"),
        verticalAlignment = Alignment.Top,
    ) {
        val author = note.author
        UsAvatar(
            name = author?.name ?: "",
            seed = note.authorId,
            imageUrl = author?.avatarUrl,
            size = UsAvatarSize.Medium,
            modifier = Modifier.clickable { callbacks.onOpenAuthor(note.authorId) },
        )
        Column(modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.l)) {
            AuthorLine(
                author = author,
                age = shortAge(note.createdAt, now),
                edited = note.edited,
                onMore = callbacks.onMore?.let { { it(note) } },
            )
            NoteBody(note = note, mediaUrls = mediaUrls, callbacks = callbacks, now = now)
            NoteActionRow(note = note, callbacks = callbacks)
        }
    }
    HorizontalDivider(color = UsTheme.extended.borderSubtle)
}

/** The text, pictures or poll, and the quoted note: shared by the card and the post page. */
@Composable
internal fun NoteBody(
    note: Note,
    mediaUrls: Map<String, String>,
    callbacks: NoteCallbacks,
    now: Instant,
    large: Boolean = false,
) {
    if (note.text.isNotBlank()) {
        Text(
            text = rememberEchoText(note.text),
            style = if (large) MaterialTheme.typography.bodyLarge else MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.padding(top = UsTheme.spacing.xs).testTag("echo_note_text"),
        )
    }
    if (note.media.isNotEmpty()) {
        NoteMediaGrid(media = note.media, urls = mediaUrls, modifier = Modifier.padding(top = UsTheme.spacing.m))
    }
    note.poll?.let { poll ->
        PollView(
            poll = poll,
            viewerIsAuthor = callbacks.viewerId.isNotBlank() && callbacks.viewerId == note.authorId,
            now = now,
            onVote = callbacks.onVote?.let { vote -> { index: Int -> vote(note, index) } },
            modifier = Modifier.padding(top = UsTheme.spacing.m),
        )
    }
    when {
        note.quoted != null -> QuotedNote(
            note = note.quoted,
            mediaUrls = mediaUrls,
            now = now,
            onOpen = { callbacks.onOpen(note.quoted) },
            modifier = Modifier.padding(top = UsTheme.spacing.m),
        )
        note.quoteOfId != null -> QuoteGone(modifier = Modifier.padding(top = UsTheme.spacing.m))
    }
}

@Composable
private fun AuthorLine(author: NoteAuthor?, age: String, edited: Boolean, onMore: (() -> Unit)?) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Row(modifier = Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = author?.name ?: "",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
            author?.mark?.takeIf { it != VerificationMark.NONE }?.let { VerificationTick(it) }
            val meta = listOfNotNull(
                author?.handle?.takeIf { it.isNotBlank() },
                age.takeIf { it.isNotBlank() },
                EDITED.takeIf { edited },
            )
            Text(
                text = meta.joinToString(" · ").let { if (it.isBlank()) it else " $it" },
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textTertiary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
        }
        if (onMore != null) {
            Icon(
                imageVector = UsIcons.More,
                contentDescription = "More",
                tint = UsTheme.extended.textTertiary,
                modifier = Modifier
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .clickable(onClick = onMore)
                    .padding(UsTheme.spacing.xs)
                    .size(MORE_ICON)
                    .testTag("echo_note_more"),
            )
        }
    }
}

/**
 * The author's mark (design §3.4): Verified person, Official organisation in
 * the accent, Government in grey.
 */
@Composable
internal fun VerificationTick(mark: VerificationMark) {
    val tint = when (mark) {
        VerificationMark.PERSON -> UsTheme.extended.statusInfo
        VerificationMark.OFFICIAL -> UsTheme.extended.accent
        VerificationMark.GOVERNMENT -> UsTheme.extended.textTertiary
        VerificationMark.NONE -> return
    }
    Icon(
        imageVector = UsIcons.ShieldCheck,
        contentDescription = mark.label,
        tint = tint,
        modifier = Modifier
            .padding(start = UsTheme.spacing.xs)
            .size(TICK_SIZE)
            .testTag("echo_mark:${mark.name.lowercase()}"),
    )
}

/**
 * X's picture layouts: one wide; two side by side; three as one tall on the
 * left and two stacked on the right; four as a 2 x 2 grid. A GIF is alone.
 */
@Composable
internal fun NoteMediaGrid(media: List<NoteMedia>, urls: Map<String, String>, modifier: Modifier = Modifier) {
    val shown = media.take(MAX_SHOWN)
    val shape = RoundedCornerShape(UsTheme.radii.media)
    val frame = modifier
        .fillMaxWidth()
        .clip(shape)
        .border(1.dp, UsTheme.extended.borderSubtle, shape)
    when (shown.size) {
        1 -> MediaTile(shown[0], urls, frame.aspectRatio(SINGLE_ASPECT))
        2 -> Row(frame.aspectRatio(GRID_ASPECT), horizontalArrangement = Arrangement.spacedBy(GRID_GAP)) {
            shown.forEach { MediaTile(it, urls, Modifier.weight(1f).fillMaxHeight()) }
        }
        THREE_PICTURES -> Row(frame.aspectRatio(GRID_ASPECT), horizontalArrangement = Arrangement.spacedBy(GRID_GAP)) {
            MediaTile(shown[0], urls, Modifier.weight(1f).fillMaxHeight())
            Column(Modifier.weight(1f).fillMaxHeight(), verticalArrangement = Arrangement.spacedBy(GRID_GAP)) {
                MediaTile(shown[1], urls, Modifier.weight(1f).fillMaxWidth())
                MediaTile(shown[2], urls, Modifier.weight(1f).fillMaxWidth())
            }
        }
        else -> Column(frame.aspectRatio(GRID_ASPECT), verticalArrangement = Arrangement.spacedBy(GRID_GAP)) {
            shown.chunked(2).forEach { row ->
                Row(Modifier.weight(1f).fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(GRID_GAP)) {
                    row.forEach { MediaTile(it, urls, Modifier.weight(1f).fillMaxHeight()) }
                }
            }
        }
    }
}

@Composable
private fun MediaTile(item: NoteMedia, urls: Map<String, String>, modifier: Modifier) {
    Box(modifier = modifier.background(UsTheme.extended.fillSubtle)) {
        urls[item.mediaId]?.let { url ->
            AsyncImage(
                model = url,
                contentDescription = item.altText.ifBlank { if (item.isGif) "GIF" else "Photo" },
                contentScale = ContentScale.Crop,
                modifier = Modifier.fillMaxSize(),
            )
        }
        if (item.isGif) {
            Text(
                text = "GIF",
                style = MaterialTheme.typography.labelSmall,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.onMedia,
                modifier = Modifier
                    .align(Alignment.BottomStart)
                    .padding(UsTheme.spacing.s)
                    .background(UsTheme.extended.scrim, RoundedCornerShape(UsTheme.radii.pill))
                    .padding(horizontal = UsTheme.spacing.xs),
            )
        }
    }
}

/** The quoted note, one level deep, as a bordered card. */
@Composable
internal fun QuotedNote(
    note: Note,
    mediaUrls: Map<String, String>,
    now: Instant,
    onOpen: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .border(1.dp, UsTheme.extended.borderMedium, shape)
            .clickable(onClick = onOpen)
            .padding(UsTheme.spacing.l)
            .testTag("echo_quoted:${note.id}"),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            UsAvatar(
                name = note.author?.name.orEmpty(),
                seed = note.authorId,
                imageUrl = note.author?.avatarUrl,
                size = UsAvatarSize.Small,
                modifier = Modifier.padding(end = UsTheme.spacing.s),
            )
            Text(
                text = note.author?.name.orEmpty(),
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
            )
            note.author?.mark?.takeIf { it != VerificationMark.NONE }?.let { VerificationTick(it) }
            Text(
                text = listOfNotNull(
                    note.author?.handle?.takeIf { it.isNotBlank() },
                    shortAge(note.createdAt, now).takeIf { it.isNotBlank() },
                ).joinToString(" · ").let { if (it.isBlank()) it else " $it" },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textTertiary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        if (note.text.isNotBlank()) {
            Text(
                text = note.text,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = QUOTE_LINES,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(top = UsTheme.spacing.xs),
            )
        }
        if (note.media.isNotEmpty()) {
            NoteMediaGrid(media = note.media, urls = mediaUrls, modifier = Modifier.padding(top = UsTheme.spacing.s))
        }
    }
}

/** A quote whose note is gone or hidden (`quoted_note: null`, `quote_of_id` kept). */
@Composable
private fun QuoteGone(modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Text(
        text = "This post is unavailable.",
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textTertiary,
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.fillSubtle)
            .padding(UsTheme.spacing.l),
    )
}

/** X's action row: reply, repost, like, views, bookmark, share. */
@Composable
internal fun NoteActionRow(note: Note, callbacks: NoteCallbacks, showCounts: Boolean = true) {
    val idle = UsTheme.extended.textTertiary
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.xs),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        val count: (Long) -> Long = { if (showCounts) it else 0 }
        ActionButton(UsIcons.Comment, "Reply", count(note.replyCount), idle, "reply") { callbacks.onReply(note) }
        ActionButton(
            UsIcons.Repost,
            if (note.reposted) "Undo repost" else "Repost",
            count(note.repostCount),
            if (note.reposted) UsTheme.extended.statusSuccess else idle,
            "repost",
        ) { callbacks.onRepost(note) }
        ActionButton(
            if (note.liked) UsIcons.HeartFilled else UsIcons.HeartOutline,
            if (note.liked) "Unlike" else "Like",
            count(note.likeCount),
            if (note.liked) UsTheme.extended.accent else idle,
            "like",
        ) { callbacks.onLike(note) }
        ActionButton(UsIcons.Poll, "Views", count(note.viewCount), idle, "views", onClick = null)
        Row(verticalAlignment = Alignment.CenterVertically) {
            callbacks.onBookmark?.let { onBookmark ->
                ActionButton(
                    if (note.bookmarked) UsIcons.BookmarkFilled else UsIcons.BookmarkOutline,
                    if (note.bookmarked) "Remove bookmark" else "Bookmark",
                    0,
                    if (note.bookmarked) UsTheme.extended.accent else idle,
                    "bookmark",
                ) { onBookmark(note) }
            }
            ActionButton(UsIcons.Share, "Share", 0, idle, "share") { callbacks.onShare(note) }
        }
    }
}

@Composable
private fun ActionButton(
    icon: ImageVector,
    label: String,
    count: Long,
    tint: Color,
    tag: String,
    onClick: (() -> Unit)?,
) {
    val text = compactCount(count)
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(UsTheme.spacing.s)
            .semantics { contentDescription = if (text.isEmpty()) label else "$label, $text" }
            .testTag("echo_action:$tag"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(ACTION_ICON))
        if (text.isNotEmpty()) {
            Text(
                text = text,
                style = MaterialTheme.typography.labelMedium,
                color = tint,
                modifier = Modifier.padding(start = UsTheme.spacing.xs),
            )
        }
    }
}

private const val EDITED = "Edited"
private const val MAX_SHOWN = 4
private const val THREE_PICTURES = 3
private const val SINGLE_ASPECT = 16f / 10f
private const val GRID_ASPECT = 16f / 9f
private const val QUOTE_LINES = 4
private val GRID_GAP = 2.dp
private val TICK_SIZE = 16.dp
private val MORE_ICON = 18.dp
private val ACTION_ICON = 18.dp
