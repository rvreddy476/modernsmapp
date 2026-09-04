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
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.common.error.AppError
import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.engagement.data.CommentRow
import com.us.android.core.engagement.data.CommentsUiState

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
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val sheetHeight = LocalConfiguration.current.screenHeightDp.dp * SHEET_HEIGHT_FRACTION

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
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

    Column(modifier = modifier.fillMaxWidth()) {
        PanelHeader(onOverflow = onOverflow)

        // Weighted so the composer ALWAYS keeps its space: unweighted, the
        // state views filled the column and pushed the composer off-screen,
        // which made every zero-comment post impossible to comment on.
        Box(modifier = Modifier.weight(1f)) {
            when {
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
                    items(state.rows, key = { it.id }) { row -> CommentItem(row) }
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

        ComposerBlock(state = state, callbacks = callbacks)
    }
}

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
 * One comment, and the post owner's inline reply under it when there is one.
 *
 * No like heart and no "Reply" control — deliberately. post-service exposes
 * `POST /v1/comments/{id}/like`, but it is a TOGGLE and the list carries no
 * viewer-liked flag, so a heart could neither show its own state nor survive
 * a retry honestly. Replies are owner-only, one per comment, and arrive
 * nested in the list, so there is nothing to expand and nothing most viewers
 * could send. Dead controls are worse than none.
 */
@Composable
private fun CommentItem(row: CommentRow) {
    Column(modifier = Modifier.fillMaxWidth()) {
        CommentBody(row)
        row.reply?.let { reply ->
            Row(modifier = Modifier.padding(start = REPLY_INDENT, top = ROW_GAP)) {
                CommentBody(reply)
            }
        }
    }
}

@Composable
private fun CommentBody(row: CommentRow) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        modifier = Modifier.fillMaxWidth(),
    ) {
        // Named when the server's batch hydration supplied an author; the
        // seed keeps a stable colour identity either way.
        UsAvatar(name = row.authorName, size = UsAvatarSize.Small, seed = row.authorId.ifBlank { row.id })
        Column(modifier = Modifier.weight(1f)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                if (row.authorName.isNotBlank()) {
                    Text(
                        text = row.authorName,
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
            }
            Text(
                text = row.body,
                style = MaterialTheme.typography.bodyLarge,
                // A comment still being written to the server is dimmed rather
                // than shown as confirmed. It is the only honest state: it may
                // still fail.
                color = if (row.pending) UsTheme.extended.textMuted else UsTheme.extended.textPrimary,
                modifier = Modifier.padding(top = BODY_TOP),
            )
        }
    }
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
 * Everything pinned to the bottom: the submit error, the quick reactions and
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
        state.submitError?.let {
            Text(
                text = commentSubmitErrorMessage(it),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
        QuickReactions(enabled = !state.submitting, onPick = callbacks.onQuickReaction)
        Composer(state = state, callbacks = callbacks)
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
@Composable
private fun DraftField(
    value: String,
    enabled: Boolean,
    canSubmit: Boolean,
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
                        text = "Add a comment…",
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
            tint = Color.White,
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

/**
 * Whether the send control exists at all.
 *
 * Presence follows the DRAFT, not validity: a draft that is only whitespace
 * still shows the disc (disabled) so the control does not blink in and out
 * while someone types a space, and an over-long draft shows it disabled
 * rather than making it vanish.
 */
internal fun CommentsUiState.showsSend(): Boolean = draft.isNotEmpty()

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

/** The quick-reaction row, in Instagram's order. */
val QUICK_REACTIONS: List<String> = listOf("❤️", "🙌", "🔥", "👏", "😢", "😍", "😮", "😂")

internal const val EMPTY_TITLE = "No comments yet"
internal const val EMPTY_DETAIL = "Start the conversation."

private const val CODE_COMMENTS_RESTRICTED = "COMMENTS_RESTRICTED"

/** The sheet's share of the window: the post stays visible above it. */
private const val SHEET_HEIGHT_FRACTION = 0.66f
private const val SCRIM_ALPHA = 0.55f
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
private val HEADER_TEXT_SIZE = 16.sp
private val ICON_SIZE = 22.dp
private val ROW_GAP = 16.dp
private val REPLY_INDENT = 40.dp
private val BODY_TOP = 2.dp
private val EMOJI_SIZE = 24.sp
private val HAIRLINE = 1.dp
private val FIELD_RADIUS = 22.dp
private val FIELD_PADDING_H = 16.dp
private val FIELD_PADDING_V = 12.dp
private val SEND_SIZE = 36.dp
private val SEND_ICON_SIZE = 18.dp
