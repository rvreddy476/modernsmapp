package com.us.android.feature.echo.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.echo.data.EchoPollRules
import com.us.android.feature.echo.data.EchoRules
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.data.StagedMedia
import com.us.android.feature.echo.data.pollLengthLabel
import com.us.android.feature.echo.data.scheduleLabel
import java.time.Instant

/**
 * X's full-screen composer: Cancel and Post on top; one post, or a thread
 * with "+"; a reply, a quote (the quoted post under the text) or an edit.
 * The toolbar adds photos, a poll or a time to send; the ring counts to 500.
 * A viewer the eligibility read refuses sees why (and Get Premium when
 * Premium is the answer and `:app` allows it), and nothing can be sent.
 */
@Composable
@Suppress("LongMethod", "CyclomaticComplexMethod") // one screen: top bar, blocked view, body, toolbar and sheets
fun ComposerScreen(
    onClose: () -> Unit,
    onPosted: (noteId: String) -> Unit,
    onScheduled: () -> Unit = onClose,
    premiumEntryShown: () -> Boolean = { false },
    onGetPremium: () -> Unit = {},
    viewModel: ComposerViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    var pickingFor by rememberSaveable { mutableIntStateOf(0) }
    var policySheet by remember { mutableStateOf(false) }
    var scheduling by remember { mutableStateOf(false) }
    var pickingGif by remember { mutableStateOf(false) }
    val picker = rememberLauncherForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia(EchoRules.MAX_IMAGES)
    ) { uris ->
        viewModel.addMedia(pickingFor, uris.map { it.toString() })
    }
    val pick: (Int) -> Unit = { index ->
        pickingFor = index
        picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
    }

    LaunchedEffect(state.doneNoteId, state.doneScheduled) {
        when {
            state.doneScheduled -> onScheduled()
            else -> state.doneNoteId?.let(onPosted)
        }
    }

    UsScaffold(
        applyPageGutter = false,
        topBar = {
            ComposerTopBar(
                label = if (state.scheduledAt != null) "Schedule" else submitLabel(state.mode, state.isThread),
                enabled = state.canSubmit,
                submitting = state.submitting,
                showSubmit = state.blocked == null && !state.checking,
                onCancel = onClose,
                onSubmit = viewModel::submit,
            )
        },
        bottomBar = {
            if (state.blocked == null && !state.checking) {
                ComposerToolbar(
                    state = state,
                    onPhotos = { pick(state.entries.lastIndex) },
                    onGif = { pickingGif = true },
                    onPoll = viewModel::startPoll,
                    onSchedule = { scheduling = true },
                    onAddEntry = viewModel::addEntry,
                )
            }
        },
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            val blocked = state.blocked
            when {
                state.checking -> UsLoadingState(label = "Checking")
                blocked != null -> BlockedView(
                    blocked = blocked,
                    showGetPremium = showGetPremium(blocked, premiumEntryShown()),
                    onGetPremium = onGetPremium,
                    onClose = onClose,
                )
                else -> ComposerBody(
                    state = state,
                    urls = urls,
                    viewModel = viewModel,
                    onPick = pick,
                    onPolicy = { policySheet = true },
                    onSchedule = { scheduling = true },
                )
            }
            val message = remember(state.error, state.notice) {
                state.error?.let { UsMessage(it) } ?: state.notice?.let { UsMessage(it, UsMessageType.Info) }
            }
            UsMessageHost(
                message = message,
                onDismiss = {
                    viewModel.errorShown()
                    viewModel.noticeShown()
                },
            )
        }
    }
    if (policySheet) {
        ReplyPolicySheet(
            chosen = state.replyPolicy,
            onPick = {
                viewModel.setReplyPolicy(it)
                policySheet = false
            },
            onDismiss = { policySheet = false },
        )
    }
    if (pickingGif) {
        GifSheet(
            onPick = { url ->
                pickingGif = false
                viewModel.addGif(state.entries.lastIndex, url)
            },
            onDismiss = { pickingGif = false },
        )
    }
    if (scheduling) {
        val now = Instant.now()
        SchedulePicker(
            initial = state.scheduledAt ?: now.plusSeconds(DEFAULT_AHEAD_SECONDS),
            now = now,
            onPicked = {
                scheduling = false
                viewModel.setSchedule(it)
            },
            onDismiss = { scheduling = false },
        )
    }
}

@Composable
@Suppress("LongParameterList")
private fun ComposerTopBar(
    label: String,
    enabled: Boolean,
    submitting: Boolean,
    showSubmit: Boolean,
    onCancel: () -> Unit,
    onSubmit: () -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        TextButton(onClick = onCancel, modifier = Modifier.testTag("echo_composer_cancel")) {
            Text("Cancel", color = UsTheme.extended.textPrimary)
        }
        Box(modifier = Modifier.weight(1f))
        if (showSubmit) {
            Button(
                onClick = onSubmit,
                enabled = enabled,
                shape = RoundedCornerShape(UsTheme.radii.full),
                colors = ButtonDefaults.buttonColors(
                    containerColor = UsTheme.extended.accent,
                    contentColor = UsTheme.extended.onAccent,
                    disabledContainerColor = UsTheme.extended.bgCardHover,
                    disabledContentColor = UsTheme.extended.textDim,
                ),
                modifier = Modifier.testTag("echo_composer_submit"),
            ) {
                if (submitting) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(SPINNER),
                        color = UsTheme.extended.onAccent,
                        strokeWidth = 2.dp
                    )
                } else {
                    Text(label, fontWeight = FontWeight.Bold)
                }
            }
        }
    }
}

@Composable
private fun BlockedView(
    blocked: ComposeGate.Blocked,
    showGetPremium: Boolean,
    onGetPremium: () -> Unit,
    onClose: () -> Unit
) {
    Column(
        modifier = Modifier.fillMaxSize().padding(UsTheme.spacing.pageHorizontal).testTag("echo_composer_blocked"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m, Alignment.CenterVertically),
    ) {
        Text(text = blocked.title, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
        Text(text = blocked.detail, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
        if (showGetPremium) {
            UsButton(
                text = GET_PREMIUM,
                onClick = onGetPremium,
                modifier = Modifier.fillMaxWidth().testTag("echo_composer_get_premium"),
            )
            OutlinedButton(onClick = onClose, modifier = Modifier.fillMaxWidth()) {
                Text("Not now", color = UsTheme.extended.textPrimary)
            }
        } else {
            UsButton(text = "OK", onClick = onClose, modifier = Modifier.fillMaxWidth())
        }
    }
}

@Composable
@Suppress("LongMethod", "LongParameterList") // the entries, poll, schedule line, quote and policy chip
private fun ComposerBody(
    state: ComposerUiState,
    urls: Map<String, String>,
    viewModel: ComposerViewModel,
    onPick: (Int) -> Unit,
    onPolicy: () -> Unit,
    onSchedule: () -> Unit,
) {
    LazyColumn(modifier = Modifier.fillMaxSize().testTag("echo_composer")) {
        state.scheduledAt?.let { at ->
            item(key = "schedule") {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable(onClick = onSchedule)
                        .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m)
                        .testTag("echo_composer_schedule_line"),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        UsIcons.Clock,
                        contentDescription = null,
                        tint = UsTheme.extended.textTertiary,
                        modifier = Modifier.size(SMALL_ICON)
                    )
                    Text(
                        text = scheduleLabel(at),
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textTertiary,
                        modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.s),
                    )
                    Icon(
                        UsIcons.Close,
                        contentDescription = "Don't schedule",
                        tint = UsTheme.extended.textTertiary,
                        modifier = Modifier.clickable(
                            onClick = viewModel::clearSchedule
                        ).size(SMALL_ICON).testTag("echo_composer_unschedule"),
                    )
                }
            }
        }
        state.target?.takeIf { state.mode == ComposeMode.REPLY }?.let { parent ->
            item(key = "replying") {
                val who = parent.author?.handle?.takeIf { it.isNotBlank() } ?: parent.author?.name.orEmpty()
                Text(
                    text = "Replying to $who",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textTertiary,
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m),
                )
            }
        }
        itemsIndexed(state.entries, key = { _, e -> e.key }) { index, entry ->
            EntryEditor(
                index = index,
                entry = entry,
                total = state.entries.size,
                locked = state.textLocked,
                canAddMedia = state.canAddMedia,
                urls = urls,
                onText = { viewModel.setText(index, it) },
                onRemove = { viewModel.removeEntry(index) },
                onPick = { onPick(index) },
                onRemoveMedia = { key -> viewModel.removeMedia(index, key) },
            )
            if (index == 0) {
                state.poll?.let { poll ->
                    PollEditorView(
                        poll = poll,
                        onOption = viewModel::setPollOption,
                        onAdd = viewModel::addPollOption,
                        onRemoveOption = viewModel::removePollOption,
                        onLength = viewModel::setPollLength,
                        onRemove = viewModel::removePoll,
                    )
                }
            }
        }
        state.target?.takeIf { state.mode == ComposeMode.QUOTE }?.let { quoted ->
            item(key = "quoted") {
                QuotedNote(
                    note = quoted,
                    mediaUrls = urls,
                    now = Instant.now(),
                    onOpen = {},
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m),
                )
            }
        }
        if (state.showsReplyPolicy) {
            item(key = "policy") {
                Row(
                    modifier = Modifier
                        .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .clickable(onClick = onPolicy)
                        .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs)
                        .testTag("echo_reply_policy_chip"),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        policyIcon(state.replyPolicy),
                        contentDescription = null,
                        tint = UsTheme.extended.accent,
                        modifier = Modifier.size(SMALL_ICON)
                    )
                    Text(
                        text = policyChip(state.replyPolicy),
                        style = MaterialTheme.typography.labelLarge,
                        fontWeight = FontWeight.Bold,
                        color = UsTheme.extended.accent,
                        modifier = Modifier.padding(start = UsTheme.spacing.s),
                    )
                }
            }
        }
        val problem = state.problem ?: state.scheduleProblem
        val touched = state.entries.any { it.text.isNotBlank() || it.media.isNotEmpty() } || state.poll != null
        problem?.takeIf { touched }?.let {
            item(key = "problem") {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.statusDanger,
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.xxl).testTag("echo_composer_problem"),
                )
            }
        }
    }
}

/** X's poll card: 2 to 4 choices of up to 25 characters, the length, Remove poll. */
@Composable
@Suppress("LongParameterList", "LongMethod")
private fun PollEditorView(
    poll: PollEditor,
    onOption: (Int, String) -> Unit,
    onAdd: () -> Unit,
    onRemoveOption: (Int) -> Unit,
    onLength: (Int) -> Unit,
    onRemove: () -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m)
            .border(BorderStroke(1.dp, UsTheme.extended.borderSubtle), RoundedCornerShape(UsTheme.radii.medium))
            .padding(UsTheme.spacing.l)
            .testTag("echo_poll_editor"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        poll.options.forEachIndexed { index, option ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .border(
                            BorderStroke(1.dp, UsTheme.extended.borderSubtle),
                            RoundedCornerShape(UsTheme.radii.small)
                        )
                        .padding(UsTheme.spacing.m),
                ) {
                    if (option.isEmpty()) {
                        Text(
                            text = "Choice ${index + 1}" +
                                if (index >= EchoPollRules.MIN_OPTIONS) " (optional)" else "",
                            style = MaterialTheme.typography.bodyMedium,
                            color = UsTheme.extended.textDim,
                        )
                    }
                    BasicTextField(
                        value = option,
                        onValueChange = { onOption(index, it) },
                        singleLine = true,
                        textStyle = MaterialTheme.typography.bodyMedium.copy(color = UsTheme.extended.textPrimary),
                        cursorBrush = SolidColor(UsTheme.extended.accent),
                        modifier = Modifier.fillMaxWidth().testTag("echo_poll_option:$index"),
                    )
                }
                Text(
                    text = "${option.length}/${EchoPollRules.MAX_OPTION_CHARS}",
                    style = MaterialTheme.typography.labelSmall,
                    color = if (option.length > EchoPollRules.MAX_OPTION_CHARS) {
                        UsTheme.extended.statusDanger
                    } else {
                        UsTheme.extended.textTertiary
                    },
                    modifier = Modifier.padding(start = UsTheme.spacing.s),
                )
                if (poll.options.size > EchoPollRules.MIN_OPTIONS) {
                    IconButton(
                        onClick = { onRemoveOption(index) },
                        modifier = Modifier.testTag("echo_poll_remove_option:$index")
                    ) {
                        Icon(UsIcons.Close, contentDescription = "Remove choice", tint = UsTheme.extended.textTertiary)
                    }
                }
            }
        }
        if (poll.canAddOption) {
            TextButton(onClick = onAdd, modifier = Modifier.testTag("echo_poll_add_option")) {
                Text("Add a choice", color = UsTheme.extended.accent)
            }
        }
        Text("Poll length", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textSecondary)
        FlowRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
            EchoPollRules.LENGTHS.forEach { minutes ->
                FilterChip(
                    selected = poll.minutes == minutes,
                    onClick = { onLength(minutes) },
                    label = { Text(pollLengthLabel(minutes)) },
                    colors = FilterChipDefaults.filterChipColors(
                        selectedContainerColor = UsTheme.extended.accent,
                        selectedLabelColor = UsTheme.extended.onAccent,
                        labelColor = UsTheme.extended.textSecondary,
                    ),
                    modifier = Modifier.testTag("echo_poll_length:$minutes"),
                )
            }
        }
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        TextButton(onClick = onRemove, modifier = Modifier.fillMaxWidth().testTag("echo_poll_remove")) {
            Text("Remove poll", color = UsTheme.extended.statusDanger)
        }
    }
}

/**
 * X's order: photos, GIF, poll, schedule; then the ring and the thread's "+".
 * GIF shows only when this build has a Giphy key, and only while the entry
 * has no media (a GIF stands alone).
 */
@Composable
@Suppress("LongParameterList")
private fun ComposerToolbar(
    state: ComposerUiState,
    onPhotos: () -> Unit,
    onGif: () -> Unit,
    onPoll: () -> Unit,
    onSchedule: () -> Unit,
    onAddEntry: () -> Unit,
) {
    Column(modifier = Modifier.fillMaxWidth().background(UsTheme.extended.bgCanvas)) {
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            ToolButton(UsIcons.ImagePlus, "Add photos", "echo_tool_photos", state.canAddMedia, onPhotos)
            if (EchoGiphy.available) {
                GifToolButton(enabled = state.canAddMedia && state.entries.last().media.isEmpty(), onClick = onGif)
            }
            ToolButton(UsIcons.Poll, "Add a poll", "echo_tool_poll", state.canAddPoll && state.poll == null, onPoll)
            ToolButton(UsIcons.Clock, "Schedule", "echo_tool_schedule", state.canSchedule, onSchedule)
            Box(modifier = Modifier.weight(1f))
            val last = state.entries.last()
            CharacterRing(remaining = last.remaining)
            if (state.canAddEntry) {
                Box(
                    modifier = Modifier
                        .padding(start = UsTheme.spacing.m)
                        .width(1.dp)
                        .height(RING)
                        .background(UsTheme.extended.borderSubtle),
                )
                ToolButton(UsIcons.Create, "Add to thread", "echo_add_entry", enabled = true, onClick = onAddEntry)
            }
        }
    }
}

/** X's GIF button: the word in a rounded box, tinted like the other tools. */
@Composable
private fun GifToolButton(enabled: Boolean, onClick: () -> Unit) {
    val tint = if (enabled) UsTheme.extended.accent else UsTheme.extended.textDim
    IconButton(onClick = onClick, enabled = enabled, modifier = Modifier.testTag("echo_tool_gif")) {
        Text(
            text = "GIF",
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Black,
            color = tint,
            modifier = Modifier
                .semantics { contentDescription = "Add a GIF" }
                .border(BorderStroke(GIF_BORDER, tint), RoundedCornerShape(GIF_RADIUS))
                .padding(horizontal = GIF_PAD_H, vertical = GIF_PAD_V),
        )
    }
}

@Composable
private fun ToolButton(icon: ImageVector, label: String, tag: String, enabled: Boolean, onClick: () -> Unit) {
    IconButton(onClick = onClick, enabled = enabled, modifier = Modifier.testTag(tag)) {
        Icon(
            icon,
            contentDescription = label,
            tint = if (enabled) UsTheme.extended.accent else UsTheme.extended.textDim
        )
    }
}

/** X's ring: fills toward 500; the number shows in the last 20, red past the limit. */
@Composable
internal fun CharacterRing(remaining: Int) {
    val used = (EchoRules.MAX_CHARS - remaining).coerceAtLeast(0)
    val progress = (used.toFloat() / EchoRules.MAX_CHARS).coerceIn(0f, 1f)
    val color = when {
        remaining < 0 -> UsTheme.extended.statusDanger
        remaining <= EchoRules.WARN_AT_REMAINING -> UsTheme.extended.statusWarning
        else -> UsTheme.extended.accent
    }
    Box(
        modifier = Modifier
            .size(RING)
            .semantics { contentDescription = "$remaining characters left" }
            .testTag("echo_ring"),
        contentAlignment = Alignment.Center,
    ) {
        CircularProgressIndicator(
            progress = { progress },
            modifier = Modifier.size(RING),
            color = color,
            trackColor = UsTheme.extended.borderSubtle,
            strokeWidth = 2.dp,
        )
        if (remaining <= EchoRules.WARN_AT_REMAINING) {
            Text(
                text = remaining.toString(),
                style = MaterialTheme.typography.labelSmall,
                color = color,
                modifier = Modifier.testTag("echo_ring_count"),
            )
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ReplyPolicySheet(chosen: ReplyPolicy, onPick: (ReplyPolicy) -> Unit, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgCanvas) {
        Column(modifier = Modifier.padding(bottom = UsTheme.spacing.xxl).testTag("echo_reply_policy_sheet")) {
            Text(
                text = "Who can reply?",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.xxl),
            )
            Text(
                text = "Pick who can reply to this post. Anyone mentioned can always reply.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textTertiary,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.s),
            )
            ReplyPolicy.entries.forEach { policy ->
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable { onPick(policy) }
                        .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.l)
                        .testTag("echo_reply_policy:${policy.wire}"),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(policyIcon(policy), contentDescription = null, tint = UsTheme.extended.accent)
                    Text(
                        text = policy.label,
                        style = MaterialTheme.typography.bodyLarge,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.l),
                    )
                    if (policy == chosen) {
                        Icon(UsIcons.Check, contentDescription = "Selected", tint = UsTheme.extended.accent)
                    }
                }
            }
        }
    }
}

@Composable
@Suppress("LongParameterList")
private fun EntryEditor(
    index: Int,
    entry: ComposerEntry,
    total: Int,
    locked: Boolean,
    canAddMedia: Boolean,
    urls: Map<String, String>,
    onText: (String) -> Unit,
    onRemove: () -> Unit,
    onPick: () -> Unit,
    onRemoveMedia: (String) -> Unit,
) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.m)
            .testTag("echo_entry:$index"),
    ) {
        Box {
            if (entry.text.isEmpty()) {
                Text(
                    text = if (index == 0) "What's happening?" else "Add another post",
                    style = MaterialTheme.typography.bodyLarge,
                    color = UsTheme.extended.textDim,
                )
            }
            BasicTextField(
                value = entry.text,
                onValueChange = onText,
                readOnly = locked,
                textStyle = MaterialTheme.typography.bodyLarge.copy(color = UsTheme.extended.textPrimary),
                cursorBrush = SolidColor(UsTheme.extended.accent),
                modifier = Modifier.fillMaxWidth().testTag("echo_entry_text:$index"),
            )
        }
        if (entry.media.isNotEmpty()) {
            Row(
                modifier = Modifier.padding(top = UsTheme.spacing.m),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)
            ) {
                entry.media.forEach { media ->
                    StagedThumb(
                        media = media,
                        url = media.mediaId?.let(urls::get),
                        onRemove = { onRemoveMedia(media.localKey) }
                    )
                }
            }
        }
        if (total > 1) {
            Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                if (canAddMedia) {
                    IconButton(onClick = onPick, modifier = Modifier.testTag("echo_pick_media:$index")) {
                        Icon(UsIcons.ImagePlus, contentDescription = "Add photos", tint = UsTheme.extended.accent)
                    }
                }
                IconButton(onClick = onRemove, modifier = Modifier.testTag("echo_remove_entry:$index")) {
                    Icon(UsIcons.Trash, contentDescription = "Remove this post", tint = UsTheme.extended.textTertiary)
                }
                Box(modifier = Modifier.weight(1f))
                Text(
                    text = entry.remaining.toString(),
                    style = MaterialTheme.typography.labelMedium,
                    color = if (entry.remaining < 0) UsTheme.extended.statusDanger else UsTheme.extended.textTertiary,
                    modifier = Modifier.testTag("echo_counter:$index"),
                )
            }
        }
    }
}

@Composable
private fun StagedThumb(media: StagedMedia, url: String?, onRemove: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.small)
    Box(modifier = Modifier.size(THUMB).clip(shape).background(UsTheme.extended.fillSubtle)) {
        AsyncImage(
            model = url ?: media.localKey,
            contentDescription = if (media.isGif) "GIF" else "Photo",
            contentScale = ContentScale.Crop,
            modifier = Modifier.matchParentSize(),
        )
        when {
            media.uploading -> CircularProgressIndicator(
                modifier = Modifier.align(Alignment.Center).size(SPINNER),
                color = UsTheme.extended.onMedia,
            )
            media.failed -> Text(
                text = "Failed",
                style = MaterialTheme.typography.labelSmall,
                color = UsTheme.extended.onMedia,
                modifier = Modifier.align(
                    Alignment.Center
                ).background(UsTheme.extended.scrim).padding(UsTheme.spacing.xs),
            )
        }
        Icon(
            UsIcons.Close,
            contentDescription = "Remove photo",
            tint = UsTheme.extended.onMedia,
            modifier = Modifier
                .align(Alignment.TopEnd)
                .padding(UsTheme.spacing.xs)
                .background(UsTheme.extended.scrim, RoundedCornerShape(UsTheme.radii.full))
                .clickable(onClick = onRemove)
                .padding(UsTheme.spacing.xs)
                .size(CLOSE_ICON),
        )
    }
}

internal fun policyChip(policy: ReplyPolicy): String = when (policy) {
    ReplyPolicy.EVERYONE -> "Everyone can reply"
    ReplyPolicy.FOLLOWING -> "Accounts you follow can reply"
    ReplyPolicy.MENTIONED -> "Only accounts you mention can reply"
}

private fun policyIcon(policy: ReplyPolicy): ImageVector = when (policy) {
    ReplyPolicy.EVERYONE -> UsIcons.Globe
    ReplyPolicy.FOLLOWING -> UsIcons.Friends
    ReplyPolicy.MENTIONED -> UsIcons.AtSign
}

internal fun titleFor(mode: ComposeMode): String = when (mode) {
    ComposeMode.NEW -> "New post"
    ComposeMode.REPLY -> "Reply"
    ComposeMode.QUOTE -> "Quote"
    ComposeMode.EDIT -> "Edit post"
}

internal fun submitLabel(mode: ComposeMode, thread: Boolean): String = when {
    mode == ComposeMode.EDIT -> "Save"
    mode == ComposeMode.REPLY -> "Reply"
    thread -> "Post all"
    else -> "Post"
}

private const val DEFAULT_AHEAD_SECONDS = 60L * 60
private val SPINNER = 18.dp
private val THUMB = 72.dp
private val CLOSE_ICON = 14.dp
private val SMALL_ICON = 16.dp
private val RING = 24.dp
private val GIF_BORDER = 1.5.dp
private val GIF_RADIUS = 4.dp
private val GIF_PAD_H = 3.dp
private val GIF_PAD_V = 1.dp
