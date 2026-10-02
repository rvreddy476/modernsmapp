package com.us.android.feature.live.ui

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.MutableTransitionState
import androidx.compose.animation.fadeIn
import androidx.compose.animation.slideInVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsEmojiPanel
import com.us.android.feature.live.data.ChatRole
import com.us.android.feature.live.data.LiveChatAuthorDto
import com.us.android.feature.live.data.LiveChatMessageDto
import com.us.android.feature.live.data.canSendChat
import com.us.android.feature.live.data.chatAuthorName
import com.us.android.feature.live.data.chatRoleOf
import com.us.android.feature.live.data.isFoundingCreator

/*
 * The live chat's pieces, shared by the host and the viewer so the two
 * cannot drift (2026-10-02): the list, one row, the comments that float
 * over the video, and the composer.
 */

/**
 * The chat, newest at the bottom. `GET …/chat` is newest first, which is
 * exactly what a reversed layout wants at index 0 — no re-reversing.
 */
@Suppress("LongParameterList")
@Composable
internal fun LiveChatList(
    messages: List<LiveChatMessageDto>,
    hostId: String,
    moderators: List<String>,
    onMessageClick: ((LiveChatMessageDto) -> Unit)?,
    onMessageLongClick: (LiveChatMessageDto) -> Unit,
    hint: String,
    modifier: Modifier = Modifier,
) {
    if (messages.isEmpty()) {
        Text(
            "No messages yet.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.onMediaMuted,
            modifier = modifier.padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
        )
        return
    }
    LazyColumn(
        modifier = modifier
            .fillMaxWidth()
            .height(CHAT_HEIGHT)
            .testTag("live-chat"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        reverseLayout = true,
    ) {
        items(messages, key = { it.id }) { message ->
            LiveChatRow(
                message = message,
                role = chatRoleOf(message, hostId, moderators),
                onClick = onMessageClick?.let { { it(message) } },
                onLongClick = { onMessageLongClick(message) },
            )
        }
        item(key = "hint") {
            Text(hint, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.onMediaMuted)
        }
    }
}

@Composable
private fun LiveChatRow(
    message: LiveChatMessageDto,
    role: ChatRole,
    onClick: (() -> Unit)?,
    onLongClick: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .combinedClickable(onClick = onClick ?: {}, onLongClick = onLongClick)
            .testTag("live-chat-row"),
    ) {
        LiveChatAuthorLine(author = message.author, role = role)
        Text(message.text, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.onMedia)
    }
}

/**
 * Who is speaking: the name (never a piece of the user id), the Founding
 * creator badge when the author has one, and "Host" or "Mod" beside it.
 */
@Composable
internal fun LiveChatAuthorLine(author: LiveChatAuthorDto?, role: ChatRole, modifier: Modifier = Modifier) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = modifier,
    ) {
        Text(
            chatAuthorName(author),
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.onMediaMuted,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier
                .weight(1f, fill = false)
                .testTag("live-chat-author"),
        )
        if (isFoundingCreator(author)) FoundingCreatorBadge()
        chatRoleLabel(role)?.let { label -> ChatRoleTag(label = label, host = role == ChatRole.Host) }
    }
}

/** The Founding creator badge: the award glyph in the accent, named for a screen reader. */
@Composable
internal fun FoundingCreatorBadge(modifier: Modifier = Modifier) {
    Icon(
        imageVector = UsIcons.Award,
        contentDescription = FOUNDING_CREATOR_LABEL,
        tint = UsTheme.extended.statusWarning,
        modifier = modifier
            .size(BADGE_GLYPH)
            .testTag("live-founding-badge"),
    )
}

/** "Host" on the live red, "Mod" on the raised surface: small, beside the name. */
@Composable
private fun ChatRoleTag(label: String, host: Boolean) {
    Text(
        label,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.onMedia,
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.small))
            .background(if (host) UsTheme.extended.liveRed else UsTheme.extended.mediaPlate)
            .padding(horizontal = UsTheme.spacing.xs)
            .testTag("live-chat-role"),
    )
}

// ── Over the video ──────────────────────────────────────────────────────

/**
 * The latest comments over the video, bottom-left, in small type (founder,
 * 2026-10-02, TikTok's idiom): the name a step stronger, the message beside
 * it, each on the on-media plate. [messages] is `ChatLog.overlayComments()` —
 * oldest first, so the newest is the bottom row: it slides in from below,
 * the others move up and fade, and one that was removed simply leaves.
 *
 * The whole block is one button: [onClick] opens the full chat. With nothing
 * to show, [emptyHint] stands in so the way into the chat is still there;
 * null draws nothing.
 */
@Suppress("LongParameterList")
@Composable
internal fun LiveChatOverlay(
    messages: List<LiveChatMessageDto>,
    hostId: String,
    moderators: List<String>,
    onClick: () -> Unit,
    emptyHint: String?,
    modifier: Modifier = Modifier,
) {
    if (messages.isEmpty() && emptyHint == null) return
    Box(
        modifier = modifier
            .fillMaxWidth(OVERLAY_WIDTH_FRACTION)
            .padding(UsTheme.spacing.l)
            .clickable(role = Role.Button, onClickLabel = "Open chat", onClick = onClick)
            .testTag("live-chat-overlay"),
    ) {
        if (messages.isEmpty()) {
            OverlayPlate { OverlayText(name = null, text = emptyHint.orEmpty()) }
            return@Box
        }
        LazyColumn(
            userScrollEnabled = false,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
            modifier = Modifier.heightIn(max = OVERLAY_MAX_HEIGHT),
        ) {
            itemsIndexed(messages, key = { _, message -> message.id }) { index, message ->
                OverlayComment(
                    message = message,
                    role = chatRoleOf(message, hostId, moderators),
                    alpha = overlayAlpha(fromNewest = messages.lastIndex - index),
                    modifier = Modifier.animateItem(),
                )
            }
        }
    }
}

/** One floating comment. It enters from below, once, when it first appears. */
@Composable
private fun OverlayComment(message: LiveChatMessageDto, role: ChatRole, alpha: Float, modifier: Modifier = Modifier) {
    val entered = remember { MutableTransitionState(false).apply { targetState = true } }
    AnimatedVisibility(
        visibleState = entered,
        enter = fadeIn() + slideInVertically { it },
        modifier = modifier.alpha(alpha),
    ) {
        OverlayPlate {
            if (isFoundingCreator(message.author)) FoundingCreatorBadge()
            val name = chatAuthorName(message.author)
            OverlayText(
                name = chatRoleLabel(role)?.let { "$name · $it" } ?: name,
                text = message.text,
            )
        }
    }
}

@Composable
private fun OverlayPlate(content: @Composable () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .background(UsTheme.extended.mediaPlate)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
            .testTag("live-chat-overlay-row"),
    ) { content() }
}

/** The name in a slightly stronger weight, the message beside it, in the on-media white. */
@Composable
private fun OverlayText(name: String?, text: String) {
    Text(
        text = buildAnnotatedString {
            if (name != null) {
                withStyle(SpanStyle(fontWeight = FontWeight.SemiBold)) { append(name) }
                append("  ")
            }
            append(text)
        },
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.onMedia,
        maxLines = OVERLAY_LINES,
        overflow = TextOverflow.Ellipsis,
    )
}

/** The newest comment is solid; each older one is fainter, on its way out at the top. */
internal fun overlayAlpha(fromNewest: Int): Float = when {
    fromNewest <= 0 -> OVERLAY_ALPHA_NEWEST
    fromNewest == 1 -> OVERLAY_ALPHA_MIDDLE
    else -> OVERLAY_ALPHA_OLDEST
}

// ── Writing ─────────────────────────────────────────────────────────────

/**
 * The chat's composer, for the viewer and the host.
 *
 * ## EMOJI (founder, 2026-10-02: "the comment box does not take emoji")
 *
 * Reading the code found NO filter: nothing in the app strips emoji from
 * this field, and the server counts runes. What the field was: a SINGLE-LINE
 * Material form field with default keyboard options — a "Done" key that only
 * closed the keyboard, no send action, and emoji only wherever the phone's
 * keyboard keeps them for a form field — and the host's screen had no box at
 * all. Whether that is what the founder hit can only be confirmed on the
 * phone. So the field is now what a chat box should be:
 *
 *  - it is a multi-line message field (up to [COMPOSER_LINES] lines) with a
 *    Send action, which is what keyboards expect of a chat box;
 *  - the smiley opens the app's own emoji panel ([UsEmojiPanel], the chat
 *    thread's), so emoji are one tap away whatever keyboard the phone has;
 *  - the draft is held to the server's 500 by CODE POINTS and never cut
 *    through an emoji (`clampChatDraft`, applied by the ViewModels).
 */
@Composable
internal fun LiveChatComposer(
    draft: String,
    sending: Boolean,
    onDraftChanged: (String) -> Unit,
    onSend: () -> Unit,
    modifier: Modifier = Modifier,
) {
    var emojiOpen by rememberSaveable { mutableStateOf(false) }
    val canSend = canSendChat(draft) && !sending
    Column(modifier = modifier.fillMaxWidth()) {
        if (emojiOpen) {
            UsEmojiPanel(
                onPick = { emoji -> onDraftChanged(draft + emoji) },
                modifier = Modifier.testTag("live-emoji-panel"),
            )
        }
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m),
        ) {
            IconButton(onClick = { emojiOpen = !emojiOpen }, modifier = Modifier.testTag("live-chat-emoji")) {
                Icon(
                    UsIcons.Smile,
                    contentDescription = if (emojiOpen) "Hide emoji" else "Emoji",
                    tint = if (emojiOpen) UsTheme.extended.accentSolid else UsTheme.extended.onMedia,
                )
            }
            OutlinedTextField(
                value = draft,
                onValueChange = onDraftChanged,
                placeholder = { Text("Say something…", style = MaterialTheme.typography.bodyMedium) },
                textStyle = MaterialTheme.typography.bodyMedium,
                maxLines = COMPOSER_LINES,
                keyboardOptions = KeyboardOptions(
                    capitalization = KeyboardCapitalization.Sentences,
                    keyboardType = KeyboardType.Text,
                    imeAction = ImeAction.Send,
                ),
                keyboardActions = KeyboardActions(onSend = { if (canSend) onSend() }),
                modifier = Modifier
                    .weight(1f)
                    .testTag("live-chat-input"),
            )
            IconButton(
                onClick = onSend,
                enabled = canSend,
                modifier = Modifier
                    .testTag("live-chat-send")
                    .semantics { contentDescription = if (canSend) "Send" else "Send. Unavailable." },
            ) {
                Icon(
                    UsIcons.Send,
                    contentDescription = null,
                    tint = if (canSend) UsTheme.extended.onMedia else UsTheme.extended.onMediaDim,
                )
            }
        }
    }
}

private val CHAT_HEIGHT = 160.dp
private val BADGE_GLYPH = 12.dp
private val OVERLAY_MAX_HEIGHT = 168.dp
private const val OVERLAY_WIDTH_FRACTION = 0.78f
private const val OVERLAY_LINES = 2
private const val OVERLAY_ALPHA_NEWEST = 1f
private const val OVERLAY_ALPHA_MIDDLE = 0.8f
private const val OVERLAY_ALPHA_OLDEST = 0.55f
private const val COMPOSER_LINES = 3

// ── Previews ────────────────────────────────────────────────────────────

private val previewMessages = listOf(
    LiveChatMessageDto(
        id = "3",
        userId = "u3",
        text = "newest 🎉",
        author = LiveChatAuthorDto(userId = "u3", name = "Asha Rao", role = "host"),
    ),
    LiveChatMessageDto(
        id = "2",
        userId = "u2",
        text = "hello from the chat",
        author = LiveChatAuthorDto(userId = "u2", handle = "kiran", badges = listOf("founding_creator")),
    ),
    LiveChatMessageDto(id = "1", userId = "u1", text = "oldest"),
)

@Preview
@Composable
private fun LiveChatListPreview() {
    UsTheme(darkTheme = true) {
        Column(Modifier.background(MaterialTheme.colorScheme.scrim)) {
            LiveChatList(
                messages = previewMessages,
                hostId = "u3",
                moderators = listOf("u2"),
                onMessageClick = null,
                onMessageLongClick = {},
                hint = "Press and hold a message to report it.",
            )
            LiveChatComposer(draft = "", sending = false, onDraftChanged = {}, onSend = {})
        }
    }
}

@Preview
@Composable
private fun LiveChatOverlayPreview() {
    UsTheme(darkTheme = true) {
        Box(Modifier.background(UsTheme.extended.stage)) {
            LiveChatOverlay(
                messages = previewMessages.asReversed(),
                hostId = "u3",
                moderators = emptyList(),
                onClick = {},
                emptyHint = null,
            )
        }
    }
}

@Preview
@Composable
private fun LiveChatOverlayEmptyPreview() {
    UsTheme(darkTheme = true) {
        Box(Modifier.background(UsTheme.extended.stage)) {
            LiveChatOverlay(
                messages = emptyList(),
                hostId = "",
                moderators = emptyList(),
                onClick = {},
                emptyHint = "No comments yet. Tap to open chat.",
            )
        }
    }
}
