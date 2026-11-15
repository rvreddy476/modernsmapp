package com.us.android.feature.echo.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
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
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsEchoType
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.NoteAuthor
import com.us.android.feature.echo.data.NoteMedia
import com.us.android.feature.echo.data.VerificationMark
import com.us.android.feature.echo.data.compactCount
import com.us.android.feature.echo.data.shortAge
import java.time.Instant

/**
 * One note as Echo's serif conversation card (echo-look-v3): the author's
 * name and how long ago it was posted (`Asha Rao · 2h`) on top with the
 * topic (`#tag`, Thread, Quote) at the right, the text in Bodoni, pictures,
 * a poll or the quoted note, up to two top replies as bubbles
 * with "N replies", and the labelled pills: Reply, Echo it, the heart and
 * More. No left avatar column, no bare icon row.
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
    /** The first replies, once they arrived; null draws no bubble area. */
    preview: ReplyPreview? = null,
) {
    EchoCard(
        modifier = modifier.testTag("echo_note:${note.id}"),
        onClick = if (focused) null else ({ callbacks.onOpen(note) }),
    ) {
        // The server counts a thread's parts on its first note; the replies only hint at one.
        val thread = isThreadCard(note, preview)
        val topic = topicLine(note, age = "", thread = thread, threadParts = note.threadParts.takeIf { it > 0 })
        Signature(
            author = note.author,
            authorId = note.authorId,
            onOpenAuthor = callbacks.onOpenAuthor,
            age = shortAge(note.createdAt, now),
            trailing = if (topic.text.isBlank()) {
                null
            } else {
                { TopicText(topic, Modifier.padding(start = UsTheme.spacing.m)) }
            },
        )
        NoteText(
            note = note,
            style = if (isLongNote(note.text)) UsEchoType.noteLong else UsEchoType.note,
        )
        NoteAttachments(note = note, mediaUrls = mediaUrls, callbacks = callbacks, now = now)
        val shown = preview?.copy(thread = thread) ?: ReplyPreview(emptyList(), null, thread = true).takeIf { thread }
        shown?.let {
            ReplyBubbles(
                preview = it,
                onOpenNote = { callbacks.onOpen(note) },
                onOpenReply = callbacks.onOpen,
            )
        }
        NotePills(note = note, callbacks = callbacks)
    }
}

/**
 * Echo's card: rounded 16, the surface colour, a hairline border (the ember
 * one when [accent], the note page's focused note), padding 14.
 */
@Composable
internal fun EchoCard(
    modifier: Modifier = Modifier,
    accent: Boolean = false,
    onClick: (() -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgCardSolid)
            .border(1.dp, if (accent) UsTheme.extended.echo.border else UsTheme.extended.borderSubtle, shape)
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(UsTheme.spacing.xl),
        content = content,
    )
}

/** The topic line: ember when it names something, secondary text when it is only the time. */
@Composable
internal fun TopicText(line: TopicLine, modifier: Modifier = Modifier) {
    if (line.text.isBlank()) return
    Text(
        text = line.text,
        style = MaterialTheme.typography.labelMedium,
        color = if (line.accent) UsTheme.extended.echo.text else UsTheme.extended.textTertiary,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
        modifier = modifier.testTag("echo_topic"),
    )
}

/** The note's words in the serif, with their links. */
@Composable
internal fun NoteText(note: Note, style: TextStyle) {
    if (note.text.isBlank()) return
    Text(
        text = rememberEchoText(note.text),
        style = style,
        color = UsTheme.extended.textPrimary,
        modifier = Modifier.padding(top = UsTheme.spacing.s).testTag("echo_note_text"),
    )
}

/** Pictures or a GIF, a poll, and the quoted note: inside the card, between the text and the signature. */
@Composable
internal fun NoteAttachments(note: Note, mediaUrls: Map<String, String>, callbacks: NoteCallbacks, now: Instant) {
    if (note.media.isNotEmpty()) {
        NoteMediaGrid(media = note.media, urls = mediaUrls, modifier = Modifier.padding(top = UsTheme.spacing.l))
    }
    note.poll?.let { poll ->
        PollView(
            poll = poll,
            viewerIsAuthor = callbacks.viewerId.isNotBlank() && callbacks.viewerId == note.authorId,
            now = now,
            onVote = callbacks.onVote?.let { vote -> { index: Int -> vote(note, index) } },
            modifier = Modifier.padding(top = UsTheme.spacing.l),
        )
    }
    when {
        note.quoted != null -> QuotedNote(
            note = note.quoted,
            mediaUrls = mediaUrls,
            now = now,
            onOpen = { callbacks.onOpen(note.quoted) },
            modifier = Modifier.padding(top = UsTheme.spacing.l),
        )
        note.quoteOfId != null -> QuoteGone(modifier = Modifier.padding(top = UsTheme.spacing.l))
    }
}

/**
 * The author row on top of a note: a small avatar and the display name, the
 * mark, then how long ago it was posted (`· 2h`) when [age] is given, or the
 * @handle in secondary text when it is not (the note page, whose meta line
 * carries the full date). [trailing] is the topic on a card, or the note
 * page's Follow pill.
 */
@Composable
internal fun Signature(
    author: NoteAuthor?,
    authorId: String,
    onOpenAuthor: (String) -> Unit,
    modifier: Modifier = Modifier,
    age: String? = null,
    trailing: (@Composable RowScope.() -> Unit)? = null,
) {
    Row(
        modifier = modifier.fillMaxWidth().testTag("echo_signature"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Row(
            modifier = Modifier
                .weight(1f)
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .clickable { onOpenAuthor(authorId) },
            verticalAlignment = Alignment.CenterVertically,
        ) {
            UsAvatar(
                name = author?.name.orEmpty(),
                seed = authorId,
                imageUrl = author?.avatarUrl,
                size = UsAvatarSize.Tiny,
            )
            Text(
                text = author?.name.orEmpty(),
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(start = UsTheme.spacing.m).weight(1f, fill = false),
            )
            if (age == null) {
                author?.handle?.takeIf { it.isNotBlank() }?.let { handle ->
                    Text(
                        text = handle,
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textTertiary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.padding(start = UsTheme.spacing.xs).weight(1f, fill = false),
                    )
                }
            }
            author?.mark?.takeIf { it != VerificationMark.NONE }?.let { VerificationTick(it) }
            age?.takeIf { it.isNotBlank() }?.let { AgeText(it, Modifier.testTag("echo_age")) }
        }
        trailing?.invoke(this)
    }
}

/** "· 2h" after the name, in secondary text. */
@Composable
private fun AgeText(age: String, modifier: Modifier = Modifier) {
    Text(
        text = "· $age",
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textTertiary,
        maxLines = 1,
        modifier = modifier.padding(start = UsTheme.spacing.xs),
    )
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
 * Up to two top replies as chat bubbles (bold name, the text, one line
 * each), then "Read the thread" when the author continued it, and "N
 * replies", which opens the note.
 */
@Composable
internal fun ReplyBubbles(preview: ReplyPreview, onOpenNote: () -> Unit, onOpenReply: (Note) -> Unit) {
    Column(
        modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l).testTag("echo_reply_bubbles"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        val nameColor = UsTheme.extended.textPrimary
        preview.bubbles.forEach { reply ->
            val name = reply.author?.name.orEmpty()
            Text(
                text = buildAnnotatedString {
                    if (name.isNotBlank()) {
                        withStyle(SpanStyle(fontWeight = FontWeight.Bold, color = nameColor)) { append(name) }
                        append("  ")
                    }
                    append(reply.text.replace('\n', ' '))
                },
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.medium))
                    .background(UsTheme.extended.bgRaised)
                    .clickable { onOpenReply(reply) }
                    .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m)
                    .testTag("echo_reply_bubble:${reply.id}"),
            )
        }
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
            if (preview.thread) {
                Text(
                    text = READ_THREAD,
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.echo.text,
                    modifier = Modifier.clickable(onClick = onOpenNote).testTag("echo_read_thread"),
                )
            }
            preview.label?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.textTertiary,
                    modifier = Modifier.clickable(onClick = onOpenNote).testTag("echo_replies_count"),
                )
            }
        }
    }
}

/**
 * The card's labelled pills: Reply, Echo it (the Re-echo / Quote sheet), the
 * heart with its count, and More (Save, Share, Copy link, Report, Mute ...).
 * Where the host draws no menu, More is the share sheet.
 */
@Composable
internal fun NotePills(note: Note, callbacks: NoteCallbacks) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        EchoPill(
            icon = UsIcons.Comment,
            label = REPLY,
            description = REPLY,
            tag = "echo_action:reply",
        ) { callbacks.onReply(note) }
        EchoPill(
            icon = UsIcons.Repost,
            label = ECHO_IT,
            description = if (note.reposted) UNDO_RE_ECHO else ECHO_IT,
            lit = note.reposted,
            tag = "echo_action:repost",
        ) { callbacks.onRepost(note) }
        val hearts = compactCount(note.likeCount)
        EchoPill(
            icon = if (note.liked) UsIcons.HeartFilled else UsIcons.HeartOutline,
            label = hearts.ifEmpty { null },
            description = (if (note.liked) "Unlike" else "Like") + if (hearts.isEmpty()) "" else ", $hearts",
            lit = note.liked,
            tag = "echo_action:like",
        ) { callbacks.onLike(note) }
        Box(modifier = Modifier.weight(1f))
        val onMore = callbacks.onMore
        EchoPill(
            icon = UsIcons.More,
            label = null,
            description = if (onMore != null) MORE else SHARE,
            tag = if (onMore != null) "echo_note_more" else "echo_action:share",
        ) { if (onMore != null) onMore(note) else callbacks.onShare(note) }
    }
}

/**
 * One labelled pill: radius full, a hairline border, 12 sp. [lit] draws it in
 * Echo's ember (a heart given, a note re-echoed, a note saved).
 */
@Composable
@Suppress("LongParameterList")
internal fun EchoPill(
    icon: ImageVector?,
    label: String?,
    description: String,
    tag: String,
    modifier: Modifier = Modifier,
    lit: Boolean = false,
    onClick: (() -> Unit)?,
) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val tint: Color = if (lit) UsTheme.extended.echo.text else UsTheme.extended.textSecondary
    Row(
        modifier = modifier
            .clip(shape)
            .border(1.dp, if (lit) UsTheme.extended.echo.border else UsTheme.extended.borderMedium, shape)
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .semantics { contentDescription = description }
            .testTag(tag),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        icon?.let {
            Icon(imageVector = it, contentDescription = null, tint = tint, modifier = Modifier.size(PILL_ICON))
        }
        label?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.labelMedium,
                color = tint,
                maxLines = 1,
            )
        }
    }
}

/**
 * Picture layouts, radius 12: one wide; two side by side; three as one tall
 * on the left and two stacked on the right; four as a 2 x 2 grid. A GIF is alone.
 */
@Composable
internal fun NoteMediaGrid(media: List<NoteMedia>, urls: Map<String, String>, modifier: Modifier = Modifier) {
    val shown = media.take(MAX_SHOWN)
    val shape = RoundedCornerShape(UsTheme.radii.medium)
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

/** The quoted note, one level deep, as a smaller nested serif card (radius 12). */
@Composable
internal fun QuotedNote(
    note: Note,
    mediaUrls: Map<String, String>,
    now: Instant,
    onOpen: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgRaised)
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .clickable(onClick = onOpen)
            .padding(UsTheme.spacing.l)
            .testTag("echo_quoted:${note.id}"),
    ) {
        Row(modifier = Modifier.padding(bottom = UsTheme.spacing.xs), verticalAlignment = Alignment.CenterVertically) {
            UsAvatar(
                name = note.author?.name.orEmpty(),
                seed = note.authorId,
                imageUrl = note.author?.avatarUrl,
                size = UsAvatarSize.Tiny,
            )
            Text(
                text = note.author?.name.orEmpty(),
                style = MaterialTheme.typography.labelMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.padding(start = UsTheme.spacing.s).weight(1f, fill = false),
            )
            note.author?.mark?.takeIf { it != VerificationMark.NONE }?.let { VerificationTick(it) }
            AgeText(shortAge(note.createdAt, now))
        }
        TopicText(topicLine(note, age = ""))
        if (note.text.isNotBlank()) {
            Text(
                text = note.text,
                style = UsEchoType.quoted,
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
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Text(
        text = "This note is unavailable.",
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textTertiary,
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgRaised)
            .padding(UsTheme.spacing.l),
    )
}

const val REPLY = "Reply"
const val ECHO_IT = "Echo it"
const val UNDO_RE_ECHO = "Undo re-echo"
const val READ_THREAD = "Read the thread"
private const val MORE = "More"
private const val SHARE = "Share"
private const val MAX_SHOWN = 4
private const val THREE_PICTURES = 3
private const val SINGLE_ASPECT = 16f / 10f
private const val GRID_ASPECT = 16f / 9f
private const val QUOTE_LINES = 4
private val GRID_GAP = 2.dp
private val TICK_SIZE = 16.dp
private val PILL_ICON = 14.dp
