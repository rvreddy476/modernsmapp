package com.us.android.core.ui

import android.content.ClipData
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.Spring
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.spring
import androidx.compose.animation.expandVertically
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.shrinkVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsPressedAsState
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.composed
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalClipboard
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.toClipEntry
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * The post "more" sheet — what the ⋮ on every post card and every reel
 * opens, everywhere (founder, 2026-09-04, from Instagram's "About this
 * reel" sheet).
 *
 * Three groups of 52dp rows with hairline dividers between them:
 *
 *  1. Save / Unsave · Copy link · Share
 *  2. Why you're seeing this post (expands inline) · Interested · Not interested
 *     · Don't recommend @user
 *  3. Unfollow @user / Follow · Block @user · Report (red, last)
 *
 * The viewer's own post shows group 1 and then "Delete post" (red, last).
 * Opened from a REEL ([UsPostMoreState.reel]), a group of its own goes
 * first — Description (unfolds the caption) · Clear screen / Show controls
 * · Quality (unfolds the rendition picker) — and the rest follows unchanged.
 *
 * Which rows appear is [rowGroups]'s decision, pinned by its own test: the
 * viewer's own post shows group 1 and Delete, the relationship row needs a known
 * edge, and the "why" row needs a sentence to show. Report is a second step
 * INSIDE the same sheet ([UsPostReportStep]); Block and Delete confirm in a
 * small dialog over it. Delete and "Don't recommend" are the two rows that
 * WAIT on the sheet: the host answers through [UsPostMoreState.delete] /
 * [UsPostMoreState.dontRecommend], and the sheet shows "Post deleted" / "We
 * won't recommend posts from @user" and leaves, or the refusal under the rows.
 *
 * Stateless, like everything in this module. [state] and [callbacks] come
 * from the host; the only state held here is presentation — which step is
 * showing, whether the reason is expanded, the two-second "Link copied"
 * pill. Actions that are complete on the tap (Share, Interested, Not
 * interested, Follow, Unfollow, a confirmed Block) slide the sheet away
 * FIRST and then fire, so whatever they open arrives on a clear screen.
 *
 * The idiom is the Create sheet's and the comments sheet's: navy
 * `bgCardSolid`, 28dp corners, a 55% scrim, a 32×4 grab handle drawn inside
 * the content.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun UsPostMoreSheet(
    state: UsPostMoreState,
    callbacks: UsPostMoreCallbacks,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val scope = rememberCoroutineScope()
    val clipboard = LocalClipboard.current
    // Presentation only, keyed to the post. Plain remember: the host keeps
    // "which post" in plain remember too, so a recreated activity has no
    // sheet to restore this into.
    val ui = remember(state.postId) { MorePresentation() }

    // Slide the sheet away FIRST, then act — the Create sheet's rule.
    fun leaveThen(action: () -> Unit) {
        scope.launch { sheetState.hide() }.invokeOnCompletion {
            onDismiss()
            action()
        }
    }

    // A filed (or already-filed) report shows its confirmation for a beat,
    // then the sheet leaves on its own. Nothing is left for the reader to do.
    LaunchedEffect(state.report) {
        if (state.report.isSettled) {
            delay(REPORT_LINGER_MILLIS)
            leaveThen {}
        }
    }
    // A delete that landed shows "Post deleted" for the same beat, then the
    // sheet leaves: the row under it is already gone from every list.
    LaunchedEffect(state.delete) {
        if (state.delete == UsPostDeleteState.Deleted) {
            delay(REPORT_LINGER_MILLIS)
            leaveThen {}
        }
    }
    // "Don't recommend" that landed: the author's posts are already gone
    // from every list; the confirmation shows for the beat, then the sheet leaves.
    LaunchedEffect(state.dontRecommend) {
        if (state.dontRecommend == UsPostDontRecommendState.Done) {
            delay(REPORT_LINGER_MILLIS)
            leaveThen {}
        }
    }
    LaunchedEffect(ui.linkCopied) {
        if (ui.linkCopied) {
            delay(LINK_COPIED_MILLIS)
            ui.linkCopied = false
        }
    }

    fun onRow(row: UsPostMoreRow) = ui.onRow(
        row = row,
        callbacks = callbacks,
        leaveThen = ::leaveThen,
        copyLink = {
            scope.launch { clipboard.setClipEntry(ClipData.newPlainText(CLIP_LABEL, state.link).toClipEntry()) }
        },
    )

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
        modifier = modifier.testTag("post_more_sheet:${state.postId}"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .navigationBarsPadding()
                .padding(bottom = CONTENT_BOTTOM),
        ) {
            SheetGrabHandle()
            when (ui.step) {
                MoreStep.MENU -> MoreMenu(
                    state = state,
                    ui = ui,
                    onRow = ::onRow,
                    onSelectQuality = { quality ->
                        ui.qualityOpen = false
                        callbacks.onSelectQuality(quality)
                    },
                )

                MoreStep.REPORT -> UsPostReportStep(
                    report = state.report,
                    onBack = { ui.step = MoreStep.MENU },
                    onSubmit = callbacks.onReport,
                )
            }
        }
    }

    MoreConfirmations(ui = ui, state = state, callbacks = callbacks, leaveThen = ::leaveThen)
}

/** The two "are you sure" dialogs, over the sheet: Block leaves on yes, Delete waits. */
@Composable
private fun MoreConfirmations(
    ui: MorePresentation,
    state: UsPostMoreState,
    callbacks: UsPostMoreCallbacks,
    leaveThen: (() -> Unit) -> Unit,
) {
    if (ui.confirmBlock) {
        ConfirmDialog(
            title = "Block @${state.username}?",
            body = "They won't be able to see your posts or message you.",
            confirmLabel = "Block",
            testTag = "post_more_block_dialog",
            confirmTestTag = "post_more_block_confirm",
            onConfirm = {
                ui.confirmBlock = false
                leaveThen(callbacks.onBlock)
            },
            onDismiss = { ui.confirmBlock = false },
        )
    }
    if (ui.confirmDelete) {
        // The sheet stays: the host answers through state.delete, and the
        // confirmation or the refusal is shown where the viewer is looking.
        ConfirmDialog(
            title = "Delete post?",
            body = "It will be removed from your profile and feeds. " +
                "You can restore it from Recently deleted for 30 days.",
            confirmLabel = "Delete",
            testTag = "post_more_delete_dialog",
            confirmTestTag = "post_more_delete_confirm",
            onConfirm = {
                ui.confirmDelete = false
                callbacks.onDelete()
            },
            onDismiss = { ui.confirmDelete = false },
        )
    }
}

private enum class MoreStep { MENU, REPORT }

/** The report step is over: the sheet may leave on its own. */
private val UsPostReportState.isSettled: Boolean
    get() = this == UsPostReportState.Sent || this == UsPostReportState.AlreadyReported

/**
 * The sheet's own presentation state and what each row does to it. Held
 * outside the composable so the row → action table is one plain `when`
 * rather than a branch of the composable's body.
 */
private class MorePresentation {
    var step by mutableStateOf(MoreStep.MENU)
    var reasonOpen by mutableStateOf(false)
    var descriptionOpen by mutableStateOf(false)
    var qualityOpen by mutableStateOf(false)
    var confirmBlock by mutableStateOf(false)
    var confirmDelete by mutableStateOf(false)
    var linkCopied by mutableStateOf(false)

    /**
     * [leaveThen] slides the sheet away and then runs the action; the rows
     * that are complete on the tap use it. Save flips in place, Copy link
     * shows its pill, Why / Description / Quality expand, Block and Delete
     * ask first, Report steps in, Clear screen leaves and then clears.
     * "Don't recommend" stays and waits for the host's answer, like Delete.
     */
    fun onRow(
        row: UsPostMoreRow,
        callbacks: UsPostMoreCallbacks,
        leaveThen: (() -> Unit) -> Unit,
        copyLink: () -> Unit,
    ) {
        when (row) {
            UsPostMoreRow.DESCRIPTION, UsPostMoreRow.CLEAR_SCREEN, UsPostMoreRow.SHOW_CONTROLS, UsPostMoreRow.QUALITY ->
                onReelRow(row, callbacks, leaveThen)
            UsPostMoreRow.SAVE, UsPostMoreRow.UNSAVE -> callbacks.onToggleSave()
            UsPostMoreRow.COPY_LINK -> {
                copyLink()
                linkCopied = true
            }
            UsPostMoreRow.SHARE -> leaveThen(callbacks.onShare)
            UsPostMoreRow.WHY -> reasonOpen = !reasonOpen
            UsPostMoreRow.INTERESTED -> leaveThen(callbacks.onInterested)
            UsPostMoreRow.NOT_INTERESTED -> leaveThen(callbacks.onNotInterested)
            UsPostMoreRow.DONT_RECOMMEND -> callbacks.onDontRecommend()
            UsPostMoreRow.UNFOLLOW -> leaveThen(callbacks.onUnfollow)
            UsPostMoreRow.FOLLOW -> leaveThen(callbacks.onFollow)
            UsPostMoreRow.BLOCK -> confirmBlock = true
            UsPostMoreRow.REPORT -> step = MoreStep.REPORT
            UsPostMoreRow.DELETE -> confirmDelete = true
        }
    }

    /** The reel's group: Description and Quality unfold in place; Clear screen leaves and then clears. */
    private fun onReelRow(row: UsPostMoreRow, callbacks: UsPostMoreCallbacks, leaveThen: (() -> Unit) -> Unit) {
        when (row) {
            UsPostMoreRow.DESCRIPTION -> descriptionOpen = !descriptionOpen
            UsPostMoreRow.QUALITY -> qualityOpen = !qualityOpen
            UsPostMoreRow.CLEAR_SCREEN, UsPostMoreRow.SHOW_CONTROLS -> leaveThen(callbacks.onClearScreen)
            else -> error("not a reel row: $row")
        }
    }
}

// ── The menu ────────────────────────────────────────────────────────────

/** The grouped rows, with the "Link copied" pill floating over the top. */
@Composable
private fun MoreMenu(
    state: UsPostMoreState,
    ui: MorePresentation,
    onRow: (UsPostMoreRow) -> Unit,
    onSelectQuality: (UsReelQuality) -> Unit,
) {
    val delete = state.delete
    val dontRecommend = state.dontRecommend
    val rowsEnabled = !state.busy &&
        delete != UsPostDeleteState.Deleting &&
        dontRecommend != UsPostDontRecommendState.Sending
    // The one refusal the sheet can be showing: a delete's, or a "don't recommend"'s.
    val refusal = (delete as? UsPostDeleteState.Failed)?.message
        ?: (dontRecommend as? UsPostDontRecommendState.Failed)?.message
    Box(modifier = Modifier.fillMaxWidth()) {
        Column(modifier = Modifier.fillMaxWidth()) {
            val groups = state.rowGroups()
            groups.forEachIndexed { index, group ->
                if (index > 0) GroupDivider()
                group.forEach { row ->
                    MenuRow(
                        row = row,
                        state = state,
                        ui = ui,
                        // Quality with Auto alone is a fact, not a choice: the row stays, inert.
                        enabled = rowsEnabled && (row != UsPostMoreRow.QUALITY || state.reel?.canPickQuality == true),
                        onClick = { onRow(row) },
                        onSelectQuality = onSelectQuality,
                    )
                }
            }
            // A refused delete or "don't recommend" stays on the sheet, under
            // the rows, so the viewer reads the reason where they are looking.
            AnimatedVisibility(
                visible = refusal != null,
                enter = expandVertically() + fadeIn(),
                exit = shrinkVertically() + fadeOut(),
            ) {
                Text(
                    text = refusal.orEmpty(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.liveRed,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(start = REASON_INDENT, end = ROW_SIDE, bottom = UsTheme.spacing.m)
                        .testTag("post_more_delete_error"),
                )
            }
        }
        StatusPill(
            visible = ui.linkCopied,
            text = "Link copied",
            testTag = "post_more_link_copied",
            modifier = Modifier.align(Alignment.TopCenter),
        )
        StatusPill(
            visible = delete == UsPostDeleteState.Deleted,
            text = "Post deleted",
            testTag = "post_more_deleted",
            modifier = Modifier.align(Alignment.TopCenter),
        )
        StatusPill(
            visible = dontRecommend == UsPostDontRecommendState.Done,
            text = "We won't recommend posts from @${state.username}",
            testTag = "post_more_dont_recommend_done",
            modifier = Modifier.align(Alignment.TopCenter),
        )
    }
}

/**
 * One row, and what unfolds under it when open: the "why" sentence, the
 * reel's full caption, or the quality picker. The three expanders share one
 * shape — a chevron at the right that turns over, the content indented to
 * the label — so the sheet reads as one idiom, not three.
 */
@Suppress("LongMethod")
@Composable
private fun MenuRow(
    row: UsPostMoreRow,
    state: UsPostMoreState,
    ui: MorePresentation,
    enabled: Boolean,
    onClick: () -> Unit,
    onSelectQuality: (UsReelQuality) -> Unit,
) {
    val label = row.menuLabel(state.username)
    val tint = if (row.isDestructive) UsTheme.extended.liveRed else UsTheme.extended.textPrimary
    val reel = state.reel
    Column(modifier = Modifier.fillMaxWidth()) {
        SheetRow(
            icon = row.icon(),
            label = label,
            tint = tint,
            enabled = enabled,
            onClick = onClick,
            trailing = when (row) {
                UsPostMoreRow.WHY -> {
                    { ExpandChevron(open = ui.reasonOpen) }
                }
                UsPostMoreRow.DESCRIPTION -> {
                    { ExpandChevron(open = ui.descriptionOpen) }
                }
                UsPostMoreRow.QUALITY -> {
                    { QualityValue(reel = reel, open = ui.qualityOpen) }
                }
                else -> null
            },
            testTag = "post_more_row:${row.name.lowercase()}",
        )
        when (row) {
            UsPostMoreRow.WHY -> Unfolded(open = ui.reasonOpen, text = state.reasonText, testTag = "post_more_reason")
            UsPostMoreRow.DESCRIPTION -> Unfolded(
                open = ui.descriptionOpen,
                text = reel?.description.orEmpty(),
                testTag = "post_more_description",
            )
            UsPostMoreRow.QUALITY -> AnimatedVisibility(
                visible = ui.qualityOpen && reel != null,
                enter = expandVertically() + fadeIn(),
                exit = shrinkVertically() + fadeOut(),
            ) {
                QualityPicker(reel = reel, onSelect = onSelectQuality)
            }
            else -> Unit
        }
    }
}

/**
 * What the row prints: the rows that act on the author carry the handle —
 * "Unfollow @user", "Block @user", "Don't recommend @user" — the rest their
 * own label.
 */
private fun UsPostMoreRow.menuLabel(username: String): String = when (this) {
    UsPostMoreRow.DONT_RECOMMEND, UsPostMoreRow.UNFOLLOW, UsPostMoreRow.BLOCK -> "$label @$username"
    else -> label
}

/** Report and Delete are red: the two rows that cannot be taken back from the sheet. */
private val UsPostMoreRow.isDestructive: Boolean
    get() = this == UsPostMoreRow.REPORT || this == UsPostMoreRow.DELETE

/** The paragraph under an expander row, in the secondary step, indented to the label. */
@Composable
private fun Unfolded(open: Boolean, text: String, testTag: String) {
    AnimatedVisibility(
        visible = open,
        enter = expandVertically() + fadeIn(),
        exit = shrinkVertically() + fadeOut(),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier
                .fillMaxWidth()
                .padding(start = REASON_INDENT, end = ROW_SIDE, bottom = UsTheme.spacing.l)
                .testTag(testTag),
        )
    }
}

/** "Auto" / "720p" at the right of the Quality row, with the chevron only when there is a choice to make. */
@Composable
private fun QualityValue(reel: UsReelMoreState?, open: Boolean) {
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(ROW_GAP / 2)) {
        Text(
            text = (reel?.selected ?: UsReelQuality.Auto).label,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.testTag("post_more_quality_value"),
        )
        if (reel?.canPickQuality == true) ExpandChevron(open = open)
    }
}

/**
 * The picker: one 44dp line per option, indented to the label, the chosen
 * one marked with a check. Auto first, then the ladder tallest-first — the
 * order [reelQualityOptions] fixed.
 */
@Composable
private fun QualityPicker(reel: UsReelMoreState?, onSelect: (UsReelQuality) -> Unit) {
    Column(modifier = Modifier.fillMaxWidth().padding(bottom = UsTheme.spacing.s)) {
        reel?.qualities.orEmpty().forEach { option ->
            val chosen = option == reel?.selected
            val interaction = remember { MutableInteractionSource() }
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .height(OPTION_HEIGHT)
                    .sheetPressScale(interaction, scale = ROW_PRESS_SCALE)
                    .clickable(
                        interactionSource = interaction,
                        indication = null,
                        role = Role.RadioButton,
                        onClick = { onSelect(option) },
                    )
                    .padding(start = REASON_INDENT, end = ROW_SIDE)
                    .testTag("post_more_quality:${option.label.lowercase()}")
                    .semantics { contentDescription = if (chosen) "${option.label}, selected" else option.label },
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Text(
                    text = option.label,
                    style = MaterialTheme.typography.bodyLarge,
                    fontSize = ROW_TEXT_SIZE,
                    color = if (chosen) UsTheme.extended.textPrimary else UsTheme.extended.textSecondary,
                    modifier = Modifier.weight(1f),
                )
                if (chosen) {
                    Icon(
                        imageVector = UsIcons.Check,
                        contentDescription = null,
                        tint = UsTheme.extended.accentSolid,
                        modifier = Modifier.size(CHEVRON_SIZE),
                    )
                }
            }
        }
    }
}

/**
 * Lucide, one per row: the reel's four (text · maximize · minimize ·
 * sliders), the four about the author (user-x · user-minus · user-plus ·
 * ban), then the post's own.
 */
private fun UsPostMoreRow.icon(): ImageVector = reelIcon() ?: personIcon() ?: postIcon()

private fun UsPostMoreRow.reelIcon(): ImageVector? = when (this) {
    UsPostMoreRow.DESCRIPTION -> UsIcons.FileText
    UsPostMoreRow.CLEAR_SCREEN -> UsIcons.Maximize
    UsPostMoreRow.SHOW_CONTROLS -> UsIcons.Minimize
    UsPostMoreRow.QUALITY -> UsIcons.Sliders
    else -> null
}

/** The rows that act on the author: a figure with an x, a minus, a plus; a ban sign. */
private fun UsPostMoreRow.personIcon(): ImageVector? = when (this) {
    UsPostMoreRow.DONT_RECOMMEND -> UsIcons.UserX
    UsPostMoreRow.UNFOLLOW -> UsIcons.UserMinus
    UsPostMoreRow.FOLLOW -> UsIcons.UserPlus
    UsPostMoreRow.BLOCK -> UsIcons.Ban
    else -> null
}

/** bookmark · link · share · info · thumbs · flag · trash. */
private fun UsPostMoreRow.postIcon(): ImageVector = when (this) {
    UsPostMoreRow.DESCRIPTION, UsPostMoreRow.CLEAR_SCREEN, UsPostMoreRow.SHOW_CONTROLS, UsPostMoreRow.QUALITY ->
        error("a reel row: $this")
    UsPostMoreRow.DONT_RECOMMEND, UsPostMoreRow.UNFOLLOW, UsPostMoreRow.FOLLOW, UsPostMoreRow.BLOCK ->
        error("a row about the author: $this")
    UsPostMoreRow.SAVE -> UsIcons.BookmarkOutline
    UsPostMoreRow.UNSAVE -> UsIcons.BookmarkFilled
    UsPostMoreRow.COPY_LINK -> UsIcons.Link
    UsPostMoreRow.SHARE -> UsIcons.Share
    UsPostMoreRow.WHY -> UsIcons.Info
    UsPostMoreRow.INTERESTED -> UsIcons.ThumbsUp
    UsPostMoreRow.NOT_INTERESTED -> UsIcons.ThumbsDown
    UsPostMoreRow.REPORT -> UsIcons.Flag
    UsPostMoreRow.DELETE -> UsIcons.Trash
}

/** The chevron turns over when the reason is open. */
@Composable
private fun ExpandChevron(open: Boolean) {
    val turn by animateFloatAsState(targetValue = if (open) CHEVRON_OPEN_DEGREES else 0f, label = "reasonChevron")
    Icon(
        imageVector = UsIcons.ChevronDown,
        contentDescription = null,
        tint = UsTheme.extended.textMuted,
        modifier = Modifier
            .size(CHEVRON_SIZE)
            .graphicsLayer { rotationZ = turn },
    )
}

/** "✓ Link copied" / "✓ Post deleted": a raised pill that shows for a beat. */
@Composable
private fun StatusPill(visible: Boolean, text: String, testTag: String, modifier: Modifier = Modifier) {
    AnimatedVisibility(
        visible = visible,
        enter = fadeIn() + expandVertically(),
        exit = fadeOut() + shrinkVertically(),
        modifier = modifier.padding(top = UsTheme.spacing.m),
    ) {
        Row(
            modifier = Modifier
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .background(UsTheme.extended.bgRaised)
                .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m)
                .testTag(testTag),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            Icon(
                imageVector = UsIcons.Check,
                contentDescription = null,
                tint = UsTheme.extended.statusSuccess,
                modifier = Modifier.size(PILL_GLYPH),
            )
            Text(
                text = text,
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textPrimary,
            )
        }
    }
}

// ── Confirmations ───────────────────────────────────────────────────────

/**
 * "Block @user?" / "Delete post?" — the two actions here that cannot be
 * undone from the sheet, so each asks once. A navy card in the sheet's
 * idiom, not Material's dialog: the same surface, corners and type as
 * everything around it. The confirming action is always red.
 */
@Suppress("LongParameterList")
@Composable
private fun ConfirmDialog(
    title: String,
    body: String,
    confirmLabel: String,
    testTag: String,
    confirmTestTag: String,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
) {
    Dialog(onDismissRequest = onDismiss) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .clip(RoundedCornerShape(DIALOG_RADIUS))
                .background(UsTheme.extended.bgCardSolid)
                .padding(DIALOG_PADDING)
                .testTag(testTag),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge.copy(fontSize = DIALOG_TITLE_SIZE),
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = body,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            Spacer(Modifier.height(UsTheme.spacing.xs))
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.End,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                DialogAction(label = "Cancel", tint = UsTheme.extended.textSecondary, onClick = onDismiss)
                Spacer(Modifier.width(UsTheme.spacing.xxl))
                DialogAction(
                    label = confirmLabel,
                    tint = UsTheme.extended.liveRed,
                    onClick = onConfirm,
                    modifier = Modifier.testTag(confirmTestTag),
                )
            }
        }
    }
}

@Composable
private fun DialogAction(label: String, tint: Color, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val interaction = remember { MutableInteractionSource() }
    Text(
        text = label,
        style = MaterialTheme.typography.labelLarge.copy(fontSize = DIALOG_ACTION_SIZE),
        fontWeight = FontWeight.SemiBold,
        color = tint,
        modifier = modifier
            .sheetPressScale(interaction)
            .clickable(
                interactionSource = interaction,
                indication = null,
                role = Role.Button,
                onClick = onClick,
            )
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.m),
    )
}

// ── Shared pieces (also used by the report step) ────────────────────────

/**
 * A 52dp row: a 22dp glyph, a 15sp label, an optional trailing slot. No
 * ripple — the press dips the row to 97% on a spring, the way every control
 * in this design gives under the thumb.
 */
@Suppress("LongParameterList")
@Composable
internal fun SheetRow(
    icon: ImageVector?,
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    tint: Color = UsTheme.extended.textPrimary,
    enabled: Boolean = true,
    trailing: (@Composable () -> Unit)? = null,
    testTag: String? = null,
) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier = modifier
            .fillMaxWidth()
            .height(ROW_HEIGHT)
            .sheetPressScale(interaction, scale = ROW_PRESS_SCALE)
            .clickable(
                interactionSource = interaction,
                indication = null,
                enabled = enabled,
                role = Role.Button,
                onClick = onClick,
            )
            .padding(horizontal = ROW_SIDE)
            .then(if (testTag != null) Modifier.testTag(testTag) else Modifier)
            .semantics { contentDescription = label },
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(ROW_GAP),
    ) {
        if (icon != null) {
            Icon(
                imageVector = icon,
                contentDescription = null,
                tint = if (enabled) tint else UsTheme.extended.textGhost,
                modifier = Modifier.size(ROW_GLYPH),
            )
        }
        Text(
            text = label,
            style = MaterialTheme.typography.bodyLarge,
            fontSize = ROW_TEXT_SIZE,
            color = if (enabled) tint else UsTheme.extended.textGhost,
            modifier = Modifier.weight(1f),
        )
        trailing?.invoke()
    }
}

/** A hairline between groups, breathing 6dp either side. */
@Composable
internal fun GroupDivider() {
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.s)
            .height(HAIRLINE)
            .background(UsTheme.extended.borderSubtle),
    )
}

/** 32×4, muted at 35%: a handle, not a decoration. */
@Composable
internal fun SheetGrabHandle() {
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

/** The press feedback every control here uses: a spring scale, no ripple. */
internal fun Modifier.sheetPressScale(interaction: MutableInteractionSource, scale: Float = PRESS_SCALE): Modifier =
    composed {
        val pressed by interaction.collectIsPressedAsState()
        val factor by animateFloatAsState(
            targetValue = if (pressed) scale else 1f,
            animationSpec = spring(dampingRatio = Spring.DampingRatioMediumBouncy, stiffness = PRESS_STIFFNESS),
            label = "press",
        )
        graphicsLayer {
            scaleX = factor
            scaleY = factor
        }
    }

// ── Metrics ─────────────────────────────────────────────────────────────

private const val CLIP_LABEL = "Post link"
private const val LINK_COPIED_MILLIS = 2_000L
private const val REPORT_LINGER_MILLIS = 1_400L
private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private const val PRESS_SCALE = 0.85f
private const val ROW_PRESS_SCALE = 0.97f
private const val PRESS_STIFFNESS = 1200f
private const val CHEVRON_OPEN_DEGREES = 180f

private val SHEET_RADIUS = 28.dp
private val CONTENT_BOTTOM = 12.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 8.dp
private val HANDLE_BOTTOM = 8.dp
private val HAIRLINE = 1.dp
private val ROW_HEIGHT = 52.dp

/** A quality option is a line inside a row, not a row: 44dp, still a full target. */
private val OPTION_HEIGHT = 44.dp
private val ROW_SIDE = 18.dp
private val ROW_GAP = 16.dp
private val ROW_GLYPH = 22.dp
private val ROW_TEXT_SIZE = 15.sp
private val REASON_INDENT = ROW_SIDE + ROW_GLYPH + ROW_GAP
private val CHEVRON_SIZE = 18.dp
private val PILL_GLYPH = 16.dp
private val DIALOG_RADIUS = 20.dp
private val DIALOG_PADDING = 22.dp
private val DIALOG_TITLE_SIZE = 18.sp
private val DIALOG_ACTION_SIZE = 14.sp
