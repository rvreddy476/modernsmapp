package com.us.android.feature.echo.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import com.us.android.core.ui.rememberRichText
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.NoteAuthor
import com.us.android.feature.echo.data.NoteMedia
import com.us.android.feature.echo.data.VerificationMark
import com.us.android.feature.echo.data.compactCount
import com.us.android.feature.echo.data.shortAge
import java.time.Instant

/**
 * One note: avatar, name with the author's mark, handle and age, the text,
 * up to 4 pictures or a GIF, the quoted note, then Reply, Repost, Like
 * (heart only, 24 Oct rule) and Share with their counts.
 */
@Composable
fun NoteCard(
    note: Note,
    mediaUrls: Map<String, String>,
    callbacks: NoteCallbacks,
    modifier: Modifier = Modifier,
    now: Instant = Instant.now(),
    /** The note a detail page is about: larger text, no tap-through. */
    focused: Boolean = false,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .then(if (focused) Modifier else Modifier.clickable { callbacks.onOpen(note) })
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l)
            .testTag("echo_note:${note.id}"),
    ) {
        Row(verticalAlignment = Alignment.Top) {
            val author = note.author
            UsAvatar(
                name = author?.name ?: "",
                seed = note.authorId,
                imageUrl = author?.avatarUrl,
                size = UsAvatarSize.Post,
                modifier = Modifier.clickable { callbacks.onOpenAuthor(note.authorId) },
            )
            Column(modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.l)) {
                AuthorLine(
                    author = author,
                    age = shortAge(note.createdAt, now),
                    edited = note.edited,
                    onMore = callbacks.onMore?.let { { it(note) } }
                )
                if (note.text.isNotBlank()) {
                    Text(
                        text = rememberRichText(note.text),
                        style = MaterialTheme.typography.run { if (focused) bodyLarge else bodyMedium },
                        color = UsTheme.extended.textBody,
                        modifier = Modifier.padding(top = UsTheme.spacing.xs),
                    )
                }
                if (note.media.isNotEmpty()) {
                    NoteMediaGrid(
                        media = note.media,
                        urls = mediaUrls,
                        modifier = Modifier.padding(top = UsTheme.spacing.m)
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
                NoteActions(note = note, callbacks = callbacks)
            }
        }
    }
    HorizontalDivider(color = UsTheme.extended.borderSubtle)
}

@Composable
private fun AuthorLine(author: NoteAuthor?, age: String, edited: Boolean, onMore: (() -> Unit)?) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        Row(modifier = Modifier.weight(1f), verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = author?.name ?: "",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
            author?.mark?.takeIf { it != VerificationMark.NONE }?.let { VerificationTick(it) }
            val meta =
                listOfNotNull(
                    author?.handle?.takeIf { it.isNotBlank() },
                    age.takeIf { it.isNotBlank() },
                    EDITED.takeIf { edited }
                )
            Text(
                text = meta.joinToString(" · ").let { if (it.isBlank()) it else " $it" },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textTertiary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.weight(1f, fill = false),
            )
        }
        if (onMore != null) {
            IconButton(onClick = onMore, modifier = Modifier.size(MORE_SIZE).testTag("echo_note_more")) {
                Icon(UsIcons.More, contentDescription = "More", tint = UsTheme.extended.textTertiary)
            }
        }
    }
}

/**
 * The author's mark (design §3.4): Verified person, Official organisation in
 * the accent, Government in grey. A placeholder glyph until the design
 * system has its own badge.
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
        modifier = Modifier.padding(
            start = UsTheme.spacing.xs
        ).size(TICK_SIZE).testTag("echo_mark:${mark.name.lowercase()}"),
    )
}

/** Up to 4 pictures: one full width, two side by side, then a 2×2 grid. A GIF is alone. */
@Composable
private fun NoteMediaGrid(media: List<NoteMedia>, urls: Map<String, String>, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.media)
    Column(
        modifier = modifier.fillMaxWidth().clip(shape),
        verticalArrangement = Arrangement.spacedBy(GRID_GAP),
    ) {
        media.take(MAX_SHOWN).chunked(if (media.size == 1) 1 else 2).forEach { row ->
            Row(horizontalArrangement = Arrangement.spacedBy(GRID_GAP)) {
                row.forEach { item ->
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .aspectRatio(if (media.size == 1) SINGLE_ASPECT else 1f)
                            .background(UsTheme.extended.fillSubtle),
                    ) {
                        urls[item.mediaId]?.let { url ->
                            AsyncImage(
                                model = url,
                                contentDescription = item.altText.ifBlank { if (item.isGif) "GIF" else "Photo" },
                                contentScale = ContentScale.Crop,
                                modifier = Modifier.matchParentSize(),
                            )
                        }
                        if (item.isGif) {
                            Text(
                                text = "GIF",
                                style = MaterialTheme.typography.labelSmall,
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
            }
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
    modifier: Modifier = Modifier
) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
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
            Text(
                text = note.author?.name.orEmpty(),
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
            )
            note.author?.mark?.takeIf { it != VerificationMark.NONE }?.let { VerificationTick(it) }
            Text(
                text = listOfNotNull(
                    note.author?.handle?.takeIf { it.isNotBlank() },
                    shortAge(note.createdAt, now).takeIf { it.isNotBlank() }
                )
                    .joinToString(" · ").let { if (it.isBlank()) it else " $it" },
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textTertiary,
                maxLines = 1,
            )
        }
        if (note.text.isNotBlank()) {
            Text(
                text = note.text,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textBody,
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
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Text(
        text = "This note is unavailable.",
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textTertiary,
        modifier = modifier
            .fillMaxWidth()
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .padding(UsTheme.spacing.l),
    )
}

@Composable
private fun NoteActions(note: Note, callbacks: NoteCallbacks) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.s),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        ActionButton(
            UsIcons.Comment,
            "Reply",
            note.replyCount,
            UsTheme.extended.textTertiary,
            "reply"
        ) { callbacks.onReply(note) }
        ActionButton(
            UsIcons.Repost,
            if (note.reposted) "Undo repost" else "Repost",
            note.repostCount,
            if (note.reposted) UsTheme.extended.statusSuccess else UsTheme.extended.textTertiary,
            "repost",
        ) { callbacks.onRepost(note) }
        ActionButton(UsIcons.Pencil, "Quote", 0, UsTheme.extended.textTertiary, "quote") { callbacks.onQuote(note) }
        ActionButton(
            if (note.liked) UsIcons.HeartFilled else UsIcons.HeartOutline,
            if (note.liked) "Unlike" else "Like",
            note.likeCount,
            if (note.liked) UsTheme.extended.accent else UsTheme.extended.textTertiary,
            "like",
        ) { callbacks.onLike(note) }
        ActionButton(UsIcons.Share, "Share", 0, UsTheme.extended.textTertiary, "share") { callbacks.onShare(note) }
    }
}

@Composable
private fun ActionButton(icon: ImageVector, label: String, count: Long, tint: Color, tag: String, onClick: () -> Unit) {
    val text = compactCount(count)
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .clickable(onClick = onClick)
            .padding(UsTheme.spacing.xs)
            .semantics { contentDescription = if (text.isEmpty()) label else "$label, $text" }
            .testTag("echo_action:$tag"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(ACTION_ICON))
        if (text.isNotEmpty()) {
            Text(
                text = text,
                style = MaterialTheme.typography.labelSmall,
                color = tint,
                modifier = Modifier.padding(start = UsTheme.spacing.xs),
            )
        }
    }
}

private const val EDITED = "Edited"
private const val MAX_SHOWN = 4
private const val SINGLE_ASPECT = 16f / 10f
private const val QUOTE_LINES = 4
private val GRID_GAP = 2.dp
private val TICK_SIZE = 16.dp
private val MORE_SIZE = 32.dp
private val ACTION_ICON = 18.dp
