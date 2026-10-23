package com.us.android.core.ui

import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.us.android.core.common.error.AppError
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.designsystem.theme.UsTypeScale
import com.us.android.core.engagement.data.CommentNotice
import com.us.android.core.engagement.data.CommentRow
import com.us.android.core.engagement.data.CommentsUiState
import com.us.android.core.engagement.data.MentionCandidate
import com.us.android.core.engagement.data.ReplyThread

/**
 * The comments sheet — the ONE way comments open anywhere in the app.
 *
 * The founder's direction (2026-09-04, from an Instagram reels capture):
 * "Comments should be like that everywhere; make it a separate component we
 * can reuse in PostTube long-form videos." So the feed card, the reels rail,
 * the in-place media viewer and post detail all open exactly this. It is a
 * thin host: a fixed-height navy sheet in the Create sheet's idiom (28dp
 * corners, 55% scrim, 32×4 grab handle) around [UsCommentsPanel].
 *
 * Stateless, like everything in this module: [state] and [callbacks] come
 * from a controller the caller owns, and nothing here can fetch. [postId]
 * keys the sheet so opening a different post's comments is a new sheet, not
 * a re-render of the previous conversation.
 *
 * The height is a FIXED share of the window, not a fraction of whatever the
 * sheet is measured against: a fraction resolved differently while the list
 * was loading and once it was empty, so the sheet opened tall and then shrank
 * (founder, 2026-09-04). Fixed, it never moves; the keyboard lifts only the
 * composer, and the list gives up the space (see [UsCommentsPanel]).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UsCommentsSheet(
    postId: String,
    state: CommentsUiState,
    callbacks: UsCommentsCallbacks,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
    /** Rendered as the header's ⋮ only when a host has something to put behind it. */
    onOverflow: (() -> Unit)? = null,
    /**
     * What a members-only thread shows a non-member (paid memberships,
     * 2026-10-23) in place of the list and the composer — the host's Join
     * lock. Null shows the sentence alone.
     */
    membersOnlyContent: (@Composable () -> Unit)? = null,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val sheetHeight = LocalConfiguration.current.screenHeightDp.dp * SHEET_HEIGHT_FRACTION

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = UsTheme.extended.scrim,
        // Drawn inside the content, in the Create sheet's proportions, rather
        // than Material's default pill.
        dragHandle = null,
        modifier = modifier.testTag("comments_sheet:$postId"),
    ) {
        Column(modifier = Modifier.fillMaxWidth().height(sheetHeight)) {
            GrabHandle()
            UsCommentsPanel(
                state = state,
                callbacks = callbacks,
                onOverflow = onOverflow,
                modifier = Modifier.weight(1f),
                membersOnlyContent = membersOnlyContent,
            )
        }
    }
}

/**
 * The comments surface itself: header, list (or its empty/loading/error
 * state), the quick-reaction row and the composer.
 *
 * Separate from [UsCommentsSheet] so a host that is not a bottom sheet — a
 * side panel beside a landscape PostTube player — can mount the same thing.
 *
 * Paging is driven by what is visible: the trigger is the LAST INDEX rather
 * than a scroll offset, so it fires once per page instead of on every pixel.
 */
@Composable
fun UsCommentsPanel(
    state: CommentsUiState,
    callbacks: UsCommentsCallbacks,
    modifier: Modifier = Modifier,
    onOverflow: (() -> Unit)? = null,
    membersOnlyContent: (@Composable () -> Unit)? = null,
) {
    val listState = rememberLazyListState()
    val shouldLoadMore by remember(state) {
        derivedStateOf {
            val last = listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: return@derivedStateOf false
            state.canLoadMore && last >= state.rows.lastIndex - PREFETCH_DISTANCE
        }
    }
    LaunchedEffect(shouldLoadMore) {
        if (shouldLoadMore) callbacks.onLoadMore()
    }
    // The comment whose ⋮ is open, by id: the menu always reflects the row
    // as it is NOW (a like or a pin that landed meanwhile), and closes
    // itself once the row is gone.
    var menuFor by rememberSaveable { mutableStateOf<String?>(null) }

    Column(modifier = modifier.fillMaxWidth()) {
        PanelHeader(onOverflow = onOverflow)

        // Weighted so the composer ALWAYS keeps its space: unweighted, the
        // state views filled the column and pushed the composer off-screen,
        // which made every zero-comment post impossible to comment on.
        Box(modifier = Modifier.weight(1f)) {
            when {
                // A members-only thread, to a non-member: no list, no composer, Join.
                state.membersOnly -> MembersOnlyComments(content = membersOnlyContent)

                state.loading && state.rows.isEmpty() ->
                    UsLoadingState(label = "Loading comments")

                // Only when there is nothing to show. A refresh failure over an
                // already-loaded list keeps the list and surfaces the error
                // under it, so a network blip never costs the conversation.
                state.refreshError != null && state.rows.isEmpty() -> UsErrorState(
                    message = "We couldn't load comments.",
                    onRetry = callbacks.onRetryRefresh,
                )

                state.rows.isEmpty() -> EmptyComments()

                else -> LazyColumn(
                    state = listState,
                    contentPadding = PaddingValues(
                        horizontal = UsTheme.spacing.pageHorizontal,
                        vertical = UsTheme.spacing.m,
                    ),
                    verticalArrangement = Arrangement.spacedBy(ROW_GAP),
                ) {
                    items(state.rows, key = { it.id }) { row ->
                        CommentItem(row = row, state = state, callbacks = callbacks, onMenu = { menuFor = it })
                    }
                    if (state.appending || state.appendError != null) {
                        item(key = "append") {
                            AppendFooter(
                                loading = state.appending,
                                failed = state.appendError != null,
                                onRetry = callbacks.onRetryAppend,
                            )
                        }
                    }
                }
            }
        }

        if (!state.membersOnly) ComposerBlock(state = state, callbacks = callbacks)
    }

    menuFor?.let { id ->
        val row = state.findRow(id)
        val actions = row?.let(state::menuFor).orEmpty()
        if (row == null || actions.isEmpty()) {
            LaunchedEffect(id) { menuFor = null }
        } else {
            UsCommentActionsSheet(
                row = row,
                actions = actions,
                report = state.report,
                callbacks = callbacks,
                onDismiss = { menuFor = null },
            )
        }
    }
}

/** A members-only thread's comments, to a non-member: the sentence, then the host's Join. */
@Composable
private fun MembersOnlyComments(content: (@Composable () -> Unit)?) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(UsTheme.spacing.pageHorizontal)
            .testTag("comments_members_only"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text(
            text = "Comments on this post are for members.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
        content?.invoke()
    }
}

/** A comment wherever the panel shows it: the list, an open thread, or a preview. */
internal fun CommentsUiState.findRow(id: String): CommentRow? =
    rows.firstOrNull { it.id == id }
        ?: threads.values.firstNotNullOfOrNull { t -> t.replies.firstOrNull { it.id == id } }
        ?: rows.firstNotNullOfOrNull { r -> r.reply?.takeIf { it.id == id } }

// ── Header ──────────────────────────────────────────────────────────────

/** 32×4, muted at 35%: a handle, not a decoration. */
@Composable
private fun GrabHandle() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = HANDLE_TOP, bottom = HANDLE_BOTTOM),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(width = HANDLE_WIDTH, height = HANDLE_HEIGHT)
                .clip(CircleShape)
                .background(UsTheme.extended.textMuted.copy(alpha = HANDLE_ALPHA)),
        )
    }
}

/** "Comments" centred; the overflow ⋮ on the right only when it does something. */
@Composable
private fun PanelHeader(onOverflow: (() -> Unit)?) {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .height(HEADER_HEIGHT),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = "Comments",
            style = MaterialTheme.typography.titleMedium.copy(fontSize = HEADER_TEXT_SIZE),
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
        if (onOverflow != null) {
            val interaction = remember { MutableInteractionSource() }
            Icon(
                imageVector = UsIcons.More,
                contentDescription = "More options",
                tint = UsTheme.extended.textPrimary,
                modifier = Modifier
                    .align(Alignment.CenterEnd)
                    .padding(end = UsTheme.spacing.pageHorizontal)
                    .pressScale(interaction)
                    .clickable(
                        interactionSource = interaction,
                        indication = null,
                        role = Role.Button,
                        onClick = onOverflow,
                    )
                    .size(ICON_SIZE),
            )
        }
    }
}

// ── List ────────────────────────────────────────────────────────────────

/** Centred, and worded as an invitation rather than an absence. */
@Composable
private fun EmptyComments() {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            text = EMPTY_TITLE,
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
        )
        Text(
            text = EMPTY_DETAIL,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = UsTheme.spacing.m),
        )
    }
}

/**
 * One top-level comment and its replies — two levels, as on the web.
 *
 * Collapsed, the oldest reply shows as a preview (the list response carries
 * it inline) and "View N replies" opens the thread; open, the paged thread
 * replaces the preview, with "View more replies" while the server has more
 * and "Hide replies" to fold it again.
 */
@Composable
private fun CommentItem(
    row: CommentRow,
    state: CommentsUiState,
    callbacks: UsCommentsCallbacks,
    onMenu: (String) -> Unit,
) {
    val thread = state.thread(row.id)
    Column(modifier = Modifier.fillMaxWidth()) {
        CommentBody(row = row, state = state, callbacks = callbacks, onMenu = onMenu)
        val shown = if (thread.expanded) thread.replies else listOfNotNull(row.reply)
        Column(
            modifier = Modifier.padding(start = REPLY_INDENT, top = if (shown.isEmpty()) 0.dp else REPLY_GAP),
            verticalArrangement = Arrangement.spacedBy(REPLY_GAP),
        ) {
            shown.forEach { reply ->
                CommentBody(row = reply, state = state, callbacks = callbacks, onMenu = onMenu)
            }
            ReplyControls(row = row, thread = thread, callbacks = callbacks)
        }
    }
}

/** The thread's own controls under a top-level comment. */
@Composable
private fun ReplyControls(row: CommentRow, thread: ReplyThread, callbacks: UsCommentsCallbacks) {
    if (thread.expanded) {
        when {
            thread.loading -> CircularProgressIndicator(
                strokeWidth = 2.dp,
                color = UsTheme.extended.accentSolid,
                modifier = Modifier
                    .padding(top = UsTheme.spacing.s)
                    .size(SMALL_SPINNER),
            )

            thread.error != null -> Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                Text(
                    text = "Replies couldn't load.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
                TextAction(text = "Try again", onClick = { callbacks.onLoadMoreReplies(row.id) })
            }

            thread.nextCursor != null || !thread.loaded -> TextAction(
                text = "View more replies",
                onClick = { callbacks.onLoadMoreReplies(row.id) },
            )
        }
        TextAction(text = "Hide replies", onClick = { callbacks.onToggleReplies(row.id) })
    } else if (row.replyCount > if (row.reply != null) 1 else 0) {
        TextAction(text = repliesLabel(row.replyCount), onClick = { callbacks.onToggleReplies(row.id) })
    }
}

@Composable
private fun CommentBody(
    row: CommentRow,
    state: CommentsUiState,
    callbacks: UsCommentsCallbacks,
    onMenu: (String) -> Unit,
) {
    val menu = state.menuFor(row)
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier
            .fillMaxWidth()
            .testTag("comment_row:${row.id}"),
    ) {
        // Named when the server's batch hydration supplied an author; the
        // seed keeps a stable colour identity either way.
        UsAvatar(
            name = row.authorName.ifBlank { row.authorUsername },
            size = UsAvatarSize.Small,
            seed = row.authorId.ifBlank { row.id },
        )
        Column(modifier = Modifier.weight(1f)) {
            CommentMeta(row)
            Text(
                text = row.body,
                style = MaterialTheme.typography.bodyLarge,
                // A comment still being written to the server is dimmed rather
                // than shown as confirmed. It is the only honest state: it may
                // still fail.
                color = if (row.pending) UsTheme.extended.textMuted else UsTheme.extended.textPrimary,
                modifier = Modifier.padding(top = BODY_TOP),
            )
            if (!row.pending) {
                TextAction(
                    text = "Reply",
                    onClick = { callbacks.onReply(row.id) },
                    modifier = Modifier.semantics { contentDescription = "Reply to ${row.displayName()}" },
                )
            }
        }
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            if (menu.isNotEmpty()) {
                IconTap(
                    icon = UsIcons.More,
                    description = "Comment options",
                    tint = UsTheme.extended.textMuted,
                    size = MENU_GLYPH,
                    onClick = { onMenu(row.id) },
                )
            }
            if (!row.pending) LikeControl(row = row, onToggle = { callbacks.onToggleLike(row.id) })
        }
    }
}

/** Name, age, and the creator's marks: "Pinned" and their heart. */
@Composable
private fun CommentMeta(row: CommentRow) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        val name = row.displayName()
        if (name.isNotBlank()) {
            Text(
                text = name,
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textPrimary,
            )
        }
        val age = formatRelativeTime(row.createdAt)
        if (age.isNotEmpty()) {
            Text(
                text = age,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        if (row.edited) {
            Text(
                text = EDITED_LABEL,
                style = MaterialTheme.typography.bodySmall,
                fontStyle = FontStyle.Italic,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.testTag("comment_edited:${row.id}"),
            )
        }
        if (row.pinned) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
                modifier = Modifier.semantics { contentDescription = "Pinned by the creator" },
            ) {
                Icon(
                    imageVector = UsIcons.Pin,
                    contentDescription = null,
                    tint = UsTheme.extended.textSecondary,
                    modifier = Modifier.size(BADGE_GLYPH),
                )
                Text(
                    text = "Pinned",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
            }
        }
        if (row.heartedByAuthor) {
            Icon(
                imageVector = UsIcons.HeartFilled,
                contentDescription = "Hearted by the creator",
                tint = UsTheme.extended.liveRed,
                modifier = Modifier.size(BADGE_GLYPH),
            )
        }
    }
}

/** The heart and its count, on the right of the row. Red when the viewer likes it. */
@Composable
private fun LikeControl(row: CommentRow, onToggle: () -> Unit) {
    IconTap(
        icon = if (row.viewerLiked) UsIcons.HeartFilled else UsIcons.HeartOutline,
        description = if (row.viewerLiked) "Unlike comment" else "Like comment",
        tint = if (row.viewerLiked) UsTheme.extended.liveRed else UsTheme.extended.textMuted,
        size = LIKE_GLYPH,
        onClick = onToggle,
    )
    if (row.likeCount > 0) {
        Text(
            text = formatCount(row.likeCount),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.semantics { contentDescription = "${row.likeCount} likes" },
        )
    }
}

@Suppress("LongParameterList")
@Composable
private fun IconTap(
    icon: ImageVector,
    description: String,
    tint: Color,
    size: Dp,
    onClick: () -> Unit,
) {
    val interaction = remember { MutableInteractionSource() }
    Box(
        modifier = Modifier
            .pressScale(interaction)
            .clickable(
                interactionSource = interaction,
                indication = null,
                role = Role.Button,
                onClick = onClick,
            )
            .semantics { contentDescription = description }
            .padding(UsTheme.spacing.xs),
        contentAlignment = Alignment.Center,
    ) {
        Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(size))
    }
}

/** A small text control: "Reply", "View 3 replies", "Hide replies". */
@Composable
private fun TextAction(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val interaction = remember { MutableInteractionSource() }
    Text(
        text = text,
        style = MaterialTheme.typography.labelMedium,
        fontWeight = FontWeight.SemiBold,
        color = UsTheme.extended.textSecondary,
        modifier = Modifier
            .pressScale(interaction)
            .clickable(
                interactionSource = interaction,
                indication = null,
                role = Role.Button,
                onClick = onClick,
            )
            .padding(vertical = UsTheme.spacing.xs)
            .then(modifier),
    )
}

@Composable
private fun AppendFooter(loading: Boolean, failed: Boolean, onRetry: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.l),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        when {
            loading -> CircularProgressIndicator(color = UsTheme.extended.accentSolid)
            failed -> {
                Text(
                    text = "Couldn't load more comments.",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textSecondary,
                )
                UsSecondaryButton(text = "Try again", onClick = onRetry)
            }
        }
    }
}

// ── Composer ────────────────────────────────────────────────────────────

/**
 * Everything pinned to the bottom: a failed row action's notice, the submit
 * error, "Replying to @name", the `@` suggestions, the quick reactions and
 * the composer. One block so `imePadding` lifts all of it together above the
 * keyboard — without it the field is covered by the very keyboard being used
 * to type in it — and the list above simply gives up the space.
 */
@Composable
private fun ComposerBlock(state: CommentsUiState, callbacks: UsCommentsCallbacks) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .imePadding()
            .navigationBarsPadding()
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
            .padding(top = UsTheme.spacing.s, bottom = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        state.notice?.let { notice ->
            NoticeLine(text = commentNoticeMessage(notice), onDismiss = callbacks.onDismissNotice)
        }
        state.submitError?.let {
            Text(
                text = if (state.editing != null) commentEditErrorMessage(it) else commentSubmitErrorMessage(it),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        state.replyingTo?.let { target ->
            ReplyingToBar(name = target.name, enabled = !state.submitting, onCancel = callbacks.onCancelReply)
        }
        if (state.editing != null) {
            ComposerTargetBar(
                text = EDITING_LABEL,
                cancelDescription = "Cancel edit",
                enabled = !state.submitting,
                onCancel = callbacks.onCancelEdit,
                testTag = "comments_editing",
            )
        }
        if (state.mentions.isNotEmpty()) {
            MentionSuggestions(candidates = state.mentions, onPick = callbacks.onPickMention)
        } else {
            QuickReactions(enabled = !state.submitting, onPick = callbacks.onQuickReaction)
        }
        Composer(state = state, callbacks = callbacks)
    }
}

/** One line, dismissable: a row action that did not stick. */
@Composable
private fun NoticeLine(text: String, onDismiss: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.fillMaxWidth().testTag("comments_notice"),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.weight(1f),
        )
        IconTap(
            icon = UsIcons.Close,
            description = "Dismiss",
            tint = UsTheme.extended.textMuted,
            size = BADGE_GLYPH,
            onClick = onDismiss,
        )
    }
}

/** "Replying to @name" with its ×: the composer's target, and the way back to a plain comment. */
@Composable
private fun ReplyingToBar(name: String, enabled: Boolean, onCancel: () -> Unit) {
    ComposerTargetBar(
        text = replyingToLabel(name),
        cancelDescription = "Cancel reply",
        enabled = enabled,
        onCancel = onCancel,
        testTag = "comments_replying_to",
    )
}

/** One line over the composer saying what Send will do — reply, or save an edit — with its ×. */
@Composable
private fun ComposerTargetBar(
    text: String,
    cancelDescription: String,
    enabled: Boolean,
    onCancel: () -> Unit,
    testTag: String,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier.fillMaxWidth().testTag(testTag),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.weight(1f),
        )
        if (enabled) {
            IconTap(
                icon = UsIcons.Close,
                description = cancelDescription,
                tint = UsTheme.extended.textMuted,
                size = BADGE_GLYPH,
                onClick = onCancel,
            )
        }
    }
}

/** Up to six people for the `@` being typed; a tap completes the handle. */
@Composable
private fun MentionSuggestions(candidates: List<MentionCandidate>, onPick: (MentionCandidate) -> Unit) {
    Column(modifier = Modifier.fillMaxWidth().testTag("comments_mentions")) {
        candidates.forEach { candidate ->
            val interaction = remember(candidate.userId) { MutableInteractionSource() }
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
                modifier = Modifier
                    .fillMaxWidth()
                    .pressScale(interaction)
                    .clickable(
                        interactionSource = interaction,
                        indication = null,
                        role = Role.Button,
                        onClick = { onPick(candidate) },
                    )
                    .semantics { contentDescription = "Mention @${candidate.username}" }
                    .padding(vertical = UsTheme.spacing.xs),
            ) {
                UsAvatar(
                    name = candidate.displayName.ifBlank { candidate.username },
                    size = UsAvatarSize.Small,
                    seed = candidate.userId,
                )
                Column {
                    Text(
                        text = "@${candidate.username}",
                        style = MaterialTheme.typography.labelLarge,
                        color = UsTheme.extended.textPrimary,
                    )
                    if (candidate.displayName.isNotBlank()) {
                        Text(
                            text = candidate.displayName,
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textMuted,
                        )
                    }
                }
            }
        }
    }
}

/** The eight one-tap reactions, in Instagram's order. */
@Composable
private fun QuickReactions(enabled: Boolean, onPick: (String) -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.s),
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        QUICK_REACTIONS.forEach { emoji ->
            val interaction = remember { MutableInteractionSource() }
            Text(
                text = emoji,
                fontSize = EMOJI_SIZE,
                modifier = Modifier
                    .pressScale(interaction)
                    .clickable(
                        interactionSource = interaction,
                        indication = null,
                        enabled = enabled,
                        role = Role.Button,
                        onClick = { onPick(emoji) },
                    )
                    .semantics { contentDescription = "React with $emoji" }
                    .padding(UsTheme.spacing.xs),
            )
        }
    }
}

@Composable
private fun Composer(state: CommentsUiState, callbacks: UsCommentsCallbacks) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        val viewer = state.viewer
        UsAvatar(
            name = viewer?.name.orEmpty(),
            size = UsAvatarSize.Post,
            seed = viewer?.id ?: "viewer",
            imageUrl = viewer?.avatarUrl,
        )
        DraftField(
            value = state.draft,
            enabled = !state.submitting,
            canSubmit = state.canSubmit,
            placeholder = when {
                state.editing != null -> "Edit your comment…"
                state.replyingTo != null -> "Add a reply…"
                else -> "Add a comment…"
            },
            onValueChange = callbacks.onDraftChange,
            onSubmit = callbacks.onSubmit,
            modifier = Modifier.weight(1f),
        )
        // Appears only once there is something to send. Nothing is disabled
        // and nothing is greyed: an empty field has no send at all.
        when {
            state.submitting -> CircularProgressIndicator(
                strokeWidth = 2.dp,
                color = UsTheme.extended.accentSolid,
                modifier = Modifier
                    .size(SEND_SIZE)
                    .padding(UsTheme.spacing.s),
            )

            state.showsSend() -> SendButton(enabled = state.canSubmit, onClick = callbacks.onSubmit)
        }
    }
}

/** A pill on the raised surface with a hairline border. No Material outline. */
@Suppress("LongParameterList")
@Composable
private fun DraftField(
    value: String,
    enabled: Boolean,
    canSubmit: Boolean,
    placeholder: String,
    onValueChange: (String) -> Unit,
    onSubmit: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val shape = RoundedCornerShape(FIELD_RADIUS)
    BasicTextField(
        value = value,
        onValueChange = onValueChange,
        enabled = enabled,
        maxLines = COMPOSER_MAX_LINES,
        textStyle = MaterialTheme.typography.bodyLarge.copy(color = UsTheme.extended.textPrimary),
        cursorBrush = SolidColor(UsTheme.extended.accentSolid),
        keyboardOptions = KeyboardOptions(imeAction = ImeAction.Send),
        keyboardActions = KeyboardActions(onSend = { if (canSubmit) onSubmit() }),
        modifier = modifier
            .background(UsTheme.extended.bgRaised, shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .padding(horizontal = FIELD_PADDING_H, vertical = FIELD_PADDING_V)
            .semantics { contentDescription = "Add a comment" },
        decorationBox = { inner ->
            Box(contentAlignment = Alignment.CenterStart) {
                if (value.isEmpty()) {
                    Text(
                        text = placeholder,
                        style = MaterialTheme.typography.bodyLarge,
                        color = UsTheme.extended.textDim,
                    )
                }
                inner()
            }
        },
    )
}

/** The ember gradient disc with the send glyph. */
@Composable
private fun SendButton(enabled: Boolean, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    Box(
        modifier = Modifier
            .pressScale(interaction)
            .size(SEND_SIZE)
            .clip(CircleShape)
            .background(UsTheme.extended.ctaGradient)
            .clickable(
                interactionSource = interaction,
                indication = null,
                enabled = enabled,
                role = Role.Button,
                onClick = onClick,
            )
            .semantics { contentDescription = "Send comment" },
        contentAlignment = Alignment.Center,
    ) {
        Icon(
            imageVector = UsIcons.Send,
            contentDescription = null,
            tint = UsTheme.extended.onAccent,
            modifier = Modifier.size(SEND_ICON_SIZE),
        )
    }
}

/** The press feedback every control here uses: a spring scale, no ripple. */
private fun Modifier.pressScale(interaction: MutableInteractionSource): Modifier = composed {
    val pressed by interaction.collectIsPressedAsState()
    val scale by animateFloatAsState(
        targetValue = if (pressed) PRESS_SCALE else 1f,
        animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = PRESS_STIFFNESS),
        label = "press",
    )
    graphicsLayer {
        scaleX = scale
        scaleY = scale
    }
}

// ── State → UI rules ────────────────────────────────────────────────────

/** The name a row shows: the display name, else the handle. */
private fun CommentRow.displayName(): String = authorName.ifBlank { authorUsername.takeIf { it.isNotBlank() }?.let { "@$it" }.orEmpty() }

/**
 * Whether the send control exists at all.
 *
 * Presence follows the DRAFT, not validity: a draft that is only whitespace
 * still shows the disc (disabled) so the control does not blink in and out
 * while someone types a space, and an over-long draft shows it disabled
 * rather than making it vanish.
 */
internal fun CommentsUiState.showsSend(): Boolean = draft.isNotEmpty()

/** "View 1 reply" / "View 3 replies" — the web's words. */
internal fun repliesLabel(count: Int): String = if (count == 1) "View 1 reply" else "View $count replies"

/** "Replying to @name" — the handle when there is one, else the display name. */
internal fun replyingToLabel(name: String): String =
    if (name.isBlank()) "Replying to a comment" else "Replying to $name"

/** A row action that was rolled back, said once and plainly. */
internal fun commentNoticeMessage(notice: CommentNotice): String = when (notice) {
    CommentNotice.LIKE -> "Your like wasn't saved. Try again."
    CommentNotice.DELETE -> "That comment wasn't deleted. Try again."
    CommentNotice.PIN -> "The pin wasn't saved. Try again."
    CommentNotice.HEART -> "The heart wasn't saved. Try again."
}

/**
 * What the composer says under a failed submit.
 *
 * `COMMENTS_RESTRICTED` is a friends-only refusal, not a transient failure —
 * `state.draft` already keeps the typed text (see `CommentsController.submit`),
 * so nothing here needs to re-save it, only word the message honestly: no
 * "tap send to try again" for a request that will keep failing until the
 * audience changes.
 */
internal fun commentSubmitErrorMessage(error: AppError): String =
    if (error is AppError.Forbidden && error.code == CODE_COMMENTS_RESTRICTED) {
        "Only friends can comment on this post"
    } else {
        "Your comment wasn't posted. Tap send to try again."
    }

/**
 * What the composer says under a refused edit (2026-10-22): post-service
 * answers `403 EDIT_WINDOW_EXPIRED` once the 15 minutes are over, which no
 * retry will fix; anything else may be tried again.
 */
internal fun commentEditErrorMessage(error: AppError): String =
    if (error is AppError.Forbidden && error.code == CODE_EDIT_WINDOW_EXPIRED) {
        "Comments can only be edited within 15 minutes of posting."
    } else {
        "Your edit wasn't saved. Tap send to try again."
    }

/** The row's mark for a comment its author changed (the web's channel comments say "Edited"). */
internal const val EDITED_LABEL = "Edited"

/** The bar over the composer while an edit is in it. */
internal const val EDITING_LABEL = "Editing your comment"

private const val CODE_EDIT_WINDOW_EXPIRED = "EDIT_WINDOW_EXPIRED"

/** The quick-reaction row, in Instagram's order. */
val QUICK_REACTIONS: List<String> = listOf("❤️", "🙌", "🔥", "👏", "😢", "😍", "😮", "😂")

internal const val EMPTY_TITLE = "No comments yet"
internal const val EMPTY_DETAIL = "Start the conversation."

private const val CODE_COMMENTS_RESTRICTED = "COMMENTS_RESTRICTED"

/** The sheet's share of the window: the post stays visible above it. */
private const val SHEET_HEIGHT_FRACTION = 0.66f
private const val HANDLE_ALPHA = 0.35f
private const val PRESS_SCALE = 0.85f
private const val PRESS_STIFFNESS = 1200f

/**
 * How many rows before the end to start fetching.
 *
 * Small on purpose: a comment page is 20 rows, and prefetching further ahead
 * spends data on a conversation the reader may not scroll into.
 */
private const val PREFETCH_DISTANCE = 3
private const val COMPOSER_MAX_LINES = 5

private val SHEET_RADIUS = 28.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 8.dp
private val HANDLE_BOTTOM = 4.dp
private val HEADER_HEIGHT = 44.dp
private val HEADER_TEXT_SIZE = UsTypeScale.base.size
private val ICON_SIZE = 22.dp
private val ROW_GAP = 16.dp
private val REPLY_GAP = 10.dp
private val REPLY_INDENT = 40.dp
private val BODY_TOP = 2.dp
private val BADGE_GLYPH = 14.dp
private val LIKE_GLYPH = 18.dp
private val MENU_GLYPH = 18.dp
private val SMALL_SPINNER = 18.dp
private val EMOJI_SIZE = UsTypeScale.xl2.size
private val HAIRLINE = 1.dp
private val FIELD_RADIUS = 22.dp
private val FIELD_PADDING_H = 16.dp
private val FIELD_PADDING_V = 12.dp
private val SEND_SIZE = 36.dp
private val SEND_ICON_SIZE = 18.dp
