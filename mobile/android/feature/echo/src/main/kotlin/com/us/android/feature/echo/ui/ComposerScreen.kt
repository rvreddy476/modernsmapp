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
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
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
import androidx.compose.ui.draw.alpha
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
import com.us.android.core.designsystem.theme.UsEchoType
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
 * Echo's composer (echo-look-v3): Cancel and a filled "Post note" pill on
 * top; the text area is a serif card, and a thread stacks its parts as cards;
 * a reply (its parent shown above), a quote or an edit. Labelled pills add
 * photos, a GIF, a poll or a later time; "N left" counts to 500.
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
    val echo = UsTheme.extended.echo
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
                    containerColor = echo.fill,
                    contentColor = echo.onFill,
                    disabledContainerColor = UsTheme.extended.bgCardHover,
                    disabledContentColor = UsTheme.extended.textDim,
                ),
                modifier = Modifier.testTag("echo_composer_submit"),
            ) {
                if (submitting) {
                    CircularProgressIndicator(
                        modifier = Modifier.size(SPINNER),
                        color = echo.onFill,
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

/**
 * The draft: in reply mode the parent note as a small card above; then each
 * entry as a serif card (a thread's parts stack as cards), the first carrying
 * who can reply; the poll under the first; the quoted note; the schedule.
 */
@Composable
@Suppress("LongMethod", "LongParameterList") // the entries, poll, schedule line, quote and policy pill
private fun ComposerBody(
    state: ComposerUiState,
    urls: Map<String, String>,
    viewModel: ComposerViewModel,
    onPick: (Int) -> Unit,
    onPolicy: () -> Unit,
    onSchedule: () -> Unit,
) {
    LazyColumn(
        modifier = Modifier.fillMaxSize().testTag("echo_composer"),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(CARD_GAP),
    ) {
        state.target?.takeIf { state.mode == ComposeMode.REPLY }?.let { parent ->
            item(key = "replying") {
                Column {
                    val who = parent.author?.handle?.takeIf { it.isNotBlank() } ?: parent.author?.name.orEmpty()
                    Text(
                        text = "Replying to $who",
                        style = MaterialTheme.typography.labelMedium,
                        color = UsTheme.extended.echo.text,
                        modifier = Modifier.padding(bottom = UsTheme.spacing.s),
                    )
                    QuotedNote(note = parent, mediaUrls = urls, now = Instant.now(), onOpen = {})
                }
            }
        }
        itemsIndexed(state.entries, key = { _, e -> e.key }) { index, entry ->
            EntryCard(
                index = index,
                entry = entry,
                total = state.entries.size,
                mode = state.mode,
                locked = state.textLocked,
                canAddMedia = state.canAddMedia,
                urls = urls,
                policy = state.replyPolicy.takeIf { index == 0 && state.showsReplyPolicy },
                onPolicy = onPolicy,
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
                QuotedNote(note = quoted, mediaUrls = urls, now = Instant.now(), onOpen = {})
            }
        }
        state.scheduledAt?.let { at ->
            item(key = "schedule") {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(UsTheme.radii.full))
                        .background(UsTheme.extended.echo.tint)
                        .clickable(onClick = onSchedule)
                        .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                        .testTag("echo_composer_schedule_line"),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(
                        UsIcons.Clock,
                        contentDescription = null,
                        tint = UsTheme.extended.echo.text,
                        modifier = Modifier.size(SMALL_ICON)
                    )
                    Text(
                        text = scheduleLabel(at),
                        style = MaterialTheme.typography.labelMedium,
                        color = UsTheme.extended.echo.text,
                        modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.s),
                    )
                    Icon(
                        UsIcons.Close,
                        contentDescription = "Don't schedule",
                        tint = UsTheme.extended.echo.text,
                        modifier = Modifier.clickable(
                            onClick = viewModel::clearSchedule
                        ).size(SMALL_ICON).testTag("echo_composer_unschedule"),
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
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.s).testTag("echo_composer_problem"),
                )
            }
        }
    }
}

/** The poll card: 2 to 4 choices of up to 25 characters, the length, Remove poll. */
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
    val echo = UsTheme.extended.echo
    EchoCard(modifier = Modifier.padding(top = CARD_GAP).testTag("echo_poll_editor")) {
        Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
            poll.options.forEachIndexed { index, option ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(
                        modifier = Modifier
                            .weight(1f)
                            .border(
                                BorderStroke(1.dp, UsTheme.extended.borderMedium),
                                RoundedCornerShape(UsTheme.radii.medium)
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
                            cursorBrush = SolidColor(echo.text),
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
                            Icon(
                                UsIcons.Close,
                                contentDescription = "Remove choice",
                                tint = UsTheme.extended.textTertiary,
                            )
                        }
                    }
                }
            }
            if (poll.canAddOption) {
                TextButton(onClick = onAdd, modifier = Modifier.testTag("echo_poll_add_option")) {
                    Text("Add a choice", color = echo.text)
                }
            }
            Text("Poll length", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.textSecondary)
            FlowRow(
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                EchoPollRules.LENGTHS.forEach { minutes ->
                    EchoPill(
                        icon = null,
                        label = pollLengthLabel(minutes),
                        description = pollLengthLabel(minutes),
                        lit = poll.minutes == minutes,
                        tag = "echo_poll_length:$minutes",
                    ) { onLength(minutes) }
                }
            }
            TextButton(onClick = onRemove, modifier = Modifier.fillMaxWidth().testTag("echo_poll_remove")) {
                Text("Remove poll", color = UsTheme.extended.statusDanger)
            }
        }
    }
}

/**
 * The tools as labelled pills: Photo, GIF, Poll, Later (schedule); then Add
 * to thread and "N left". GIF shows only when this build has a Giphy key,
 * and only while the entry has no media (a GIF stands alone).
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
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCanvas)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.m),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            ToolPill(UsIcons.ImagePlus, "Photo", "Add photos", "echo_tool_photos", state.canAddMedia, onPhotos)
            if (EchoGiphy.available) {
                ToolPill(
                    icon = null,
                    label = "GIF",
                    description = "Add a GIF",
                    tag = "echo_tool_gif",
                    enabled = state.canAddMedia && state.entries.last().media.isEmpty(),
                    onClick = onGif,
                )
            }
            val canPoll = state.canAddPoll && state.poll == null
            ToolPill(UsIcons.Poll, "Poll", "Add a poll", "echo_tool_poll", canPoll, onPoll)
            ToolPill(UsIcons.Clock, "Later", "Schedule", "echo_tool_schedule", state.canSchedule, onSchedule)
        }
        Row(verticalAlignment = Alignment.CenterVertically) {
            if (state.canAddEntry) {
                ToolPill(UsIcons.Create, "Add to thread", "Add to thread", "echo_add_entry", true, onAddEntry)
            }
            Box(modifier = Modifier.weight(1f))
            CharactersLeft(remaining = state.entries.last().remaining)
        }
    }
}

@Composable
@Suppress("LongParameterList")
private fun ToolPill(
    icon: ImageVector?,
    label: String,
    description: String,
    tag: String,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    EchoPill(
        icon = icon,
        label = label,
        description = description,
        tag = tag,
        modifier = Modifier.alpha(if (enabled) 1f else DISABLED_ALPHA),
        onClick = if (enabled) onClick else null,
    )
}

/** "N left" toward 500: amber in the last 20, red past the limit. */
@Composable
internal fun CharactersLeft(remaining: Int, modifier: Modifier = Modifier) {
    val color = when {
        remaining < 0 -> UsTheme.extended.statusDanger
        remaining <= EchoRules.WARN_AT_REMAINING -> UsTheme.extended.statusWarning
        else -> UsTheme.extended.textTertiary
    }
    Text(
        text = charactersLeft(remaining),
        style = MaterialTheme.typography.labelMedium,
        color = color,
        modifier = modifier
            .semantics { contentDescription = "$remaining characters left" }
            .testTag("echo_ring"),
    )
}

/** "500 left", "3 left", "12 over". */
internal fun charactersLeft(remaining: Int): String =
    if (remaining < 0) "${-remaining} over" else "$remaining left"

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ReplyPolicySheet(chosen: ReplyPolicy, onPick: (ReplyPolicy) -> Unit, onDismiss: () -> Unit) {
    ModalBottomSheet(onDismissRequest = onDismiss, containerColor = UsTheme.extended.bgSheet) {
        Column(modifier = Modifier.padding(bottom = UsTheme.spacing.xxl).testTag("echo_reply_policy_sheet")) {
            Text(
                text = "Who can reply?",
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.padding(horizontal = UsTheme.spacing.xxl),
            )
            Text(
                text = "Pick who can reply to this note. Anyone mentioned can always reply.",
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
                    Icon(policyIcon(policy), contentDescription = null, tint = UsTheme.extended.echo.text)
                    Text(
                        text = policy.label,
                        style = MaterialTheme.typography.bodyLarge,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier.weight(1f).padding(start = UsTheme.spacing.l),
                    )
                    if (policy == chosen) {
                        Icon(UsIcons.Check, contentDescription = "Selected", tint = UsTheme.extended.echo.text)
                    }
                }
            }
        }
    }
}

/**
 * One entry of the draft as a serif card: who can reply on top of the first
 * (the existing reply-policy control), the text in the serif, its pictures,
 * and on a thread its own Photo, Remove and "N left".
 */
@Composable
@Suppress("LongParameterList", "LongMethod")
private fun EntryCard(
    index: Int,
    entry: ComposerEntry,
    total: Int,
    mode: ComposeMode,
    locked: Boolean,
    canAddMedia: Boolean,
    urls: Map<String, String>,
    policy: ReplyPolicy?,
    onPolicy: () -> Unit,
    onText: (String) -> Unit,
    onRemove: () -> Unit,
    onPick: () -> Unit,
    onRemoveMedia: (String) -> Unit,
) {
    EchoCard(modifier = Modifier.testTag("echo_entry:$index"), accent = index == 0) {
        if (policy != null || total > 1) {
            Row(
                modifier = Modifier.fillMaxWidth().padding(bottom = UsTheme.spacing.m),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (total > 1) {
                    Text(
                        text = "${index + 1} of $total",
                        style = MaterialTheme.typography.labelMedium,
                        color = UsTheme.extended.echo.text,
                        modifier = Modifier.padding(end = UsTheme.spacing.m),
                    )
                }
                Box(modifier = Modifier.weight(1f))
                policy?.let {
                    EchoPill(
                        icon = policyIcon(it),
                        label = policyChip(it),
                        description = policyChip(it),
                        tag = "echo_reply_policy_chip",
                        onClick = onPolicy,
                    )
                }
            }
        }
        Box {
            if (entry.text.isEmpty()) {
                Text(
                    text = placeholder(mode, index),
                    style = UsEchoType.composer,
                    color = UsTheme.extended.textDim,
                )
            }
            BasicTextField(
                value = entry.text,
                onValueChange = onText,
                readOnly = locked,
                textStyle = UsEchoType.composer.copy(color = UsTheme.extended.textPrimary),
                cursorBrush = SolidColor(UsTheme.extended.echo.text),
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
            Row(
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                if (canAddMedia) {
                    EchoPill(UsIcons.ImagePlus, "Photo", "Add photos", "echo_pick_media:$index", onClick = onPick)
                }
                EchoPill(UsIcons.Trash, "Remove", "Remove this note", "echo_remove_entry:$index", onClick = onRemove)
                Box(modifier = Modifier.weight(1f))
                CharactersLeft(remaining = entry.remaining, modifier = Modifier.testTag("echo_counter:$index"))
            }
        }
    }
}

@Composable
private fun StagedThumb(media: StagedMedia, url: String?, onRemove: () -> Unit) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
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

/** The empty text area's words: "What's worth saying today?", "Your reply…", "Add another note". */
internal fun placeholder(mode: ComposeMode, index: Int): String = when {
    index > 0 -> "Add another note"
    mode == ComposeMode.REPLY -> "Your reply…"
    else -> "What's worth saying today?"
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
    ComposeMode.NEW -> "New note"
    ComposeMode.REPLY -> "Reply"
    ComposeMode.QUOTE -> "Quote"
    ComposeMode.EDIT -> "Edit note"
}

internal fun submitLabel(mode: ComposeMode, thread: Boolean): String = when {
    mode == ComposeMode.EDIT -> "Save"
    mode == ComposeMode.REPLY -> "Reply"
    thread -> "Post thread"
    else -> "Post note"
}

private const val DEFAULT_AHEAD_SECONDS = 60L * 60
private const val DISABLED_ALPHA = 0.4f
private val SPINNER = 18.dp
private val THUMB = 72.dp
private val CLOSE_ICON = 14.dp
private val SMALL_ICON = 16.dp
