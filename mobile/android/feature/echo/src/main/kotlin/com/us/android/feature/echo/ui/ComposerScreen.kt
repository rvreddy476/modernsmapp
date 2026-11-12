package com.us.android.feature.echo.ui

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import coil3.compose.AsyncImage
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.echo.data.EchoRules
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.data.StagedMedia
import java.time.Instant

/**
 * The composer: one note, or a thread when "Add to thread" is used; a reply,
 * a quote (the quoted note under the text) or an edit. A viewer the
 * eligibility read refuses sees the sheet's words instead, and nothing can
 * be sent.
 */
@Composable
@Suppress("LongMethod") // one screen: picker, blocked view and body wiring
fun ComposerScreen(
    onClose: () -> Unit,
    onPosted: (noteId: String) -> Unit,
    viewModel: ComposerViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val urls by viewModel.mediaUrls.collectAsStateWithLifecycle()
    var pickingFor by rememberSaveable { mutableIntStateOf(0) }
    val picker =
        rememberLauncherForActivityResult(
            ActivityResultContracts.PickMultipleVisualMedia(EchoRules.MAX_IMAGES)
        ) { uris ->
            viewModel.addMedia(pickingFor, uris.map { it.toString() })
        }

    LaunchedEffect(state.doneNoteId) { state.doneNoteId?.let(onPosted) }

    UsScaffold(
        applyPageGutter = false,
        topBar = {
            UsTopBar(
                title = titleFor(state.mode),
                onBack = onClose,
                actions = {
                    TextButton(
                        onClick = viewModel::submit,
                        enabled = state.canSubmit,
                        modifier = Modifier.testTag("echo_composer_submit"),
                    ) {
                        if (state.submitting) {
                            CircularProgressIndicator(
                                modifier = Modifier.size(SPINNER),
                                color = UsTheme.extended.accent
                            )
                        } else {
                            Text(
                                text = submitLabel(state.mode, state.isThread),
                                color = if (state.canSubmit) UsTheme.extended.accent else UsTheme.extended.textDim,
                            )
                        }
                    }
                },
            )
        },
    ) { padding ->
        Box(modifier = Modifier.padding(padding).fillMaxSize()) {
            val blocked = state.blocked
            when {
                state.checking -> UsLoadingState(label = "Checking")
                blocked != null -> Column(
                    modifier = Modifier.fillMaxSize().padding(
                        UsTheme.spacing.pageHorizontal
                    ).testTag("echo_composer_blocked"),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m, Alignment.CenterVertically),
                ) {
                    Text(
                        text = blocked.title,
                        style = MaterialTheme.typography.titleMedium,
                        color = UsTheme.extended.textPrimary
                    )
                    Text(
                        text = blocked.detail,
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textSecondary
                    )
                    UsButton(text = "OK", onClick = onClose, modifier = Modifier.fillMaxWidth())
                }
                else -> ComposerBody(
                    state = state,
                    urls = urls,
                    onText = viewModel::setText,
                    onRemoveEntry = viewModel::removeEntry,
                    onPick = { index ->
                        pickingFor = index
                        picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                    },
                    onRemoveMedia = viewModel::removeMedia,
                    onAddEntry = viewModel::addEntry,
                    onPolicy = viewModel::setReplyPolicy,
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
}

@Composable
@Suppress("LongParameterList", "LongMethod") // one list of entries, each with its own controls
private fun ComposerBody(
    state: ComposerUiState,
    urls: Map<String, String>,
    onText: (Int, String) -> Unit,
    onRemoveEntry: (Int) -> Unit,
    onPick: (Int) -> Unit,
    onRemoveMedia: (Int, String) -> Unit,
    onAddEntry: () -> Unit,
    onPolicy: (ReplyPolicy) -> Unit,
) {
    LazyColumn(modifier = Modifier.fillMaxSize().testTag("echo_composer")) {
        state.target?.takeIf { state.mode == ComposeMode.REPLY }?.let { parent ->
            item(key = "replying") {
                val who = parent.author?.handle?.takeIf { it.isNotBlank() } ?: parent.author?.name.orEmpty()
                Text(
                    text = "Replying to $who",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textTertiary,
                    modifier = Modifier.padding(
                        horizontal = UsTheme.spacing.pageHorizontal,
                        vertical = UsTheme.spacing.m
                    ),
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
                onText = { onText(index, it) },
                onRemove = { onRemoveEntry(index) },
                onPick = { onPick(index) },
                onRemoveMedia = { key -> onRemoveMedia(index, key) },
            )
        }
        state.target?.takeIf { state.mode == ComposeMode.QUOTE }?.let { quoted ->
            item(key = "quoted") {
                QuotedNote(
                    note = quoted,
                    mediaUrls = urls,
                    now = Instant.now(),
                    onOpen = {},
                    modifier = Modifier.padding(
                        horizontal = UsTheme.spacing.pageHorizontal,
                        vertical = UsTheme.spacing.m
                    ),
                )
            }
        }
        if (state.canAddEntry) {
            item(key = "add") {
                TextButton(
                    onClick = onAddEntry,
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.pageHorizontal).testTag("echo_add_entry"),
                ) {
                    Text("Add to thread", color = UsTheme.extended.accent)
                }
            }
        }
        if (state.showsReplyPolicy) {
            item(key = "policy") {
                Column(
                    modifier = Modifier.padding(
                        horizontal = UsTheme.spacing.pageHorizontal,
                        vertical = UsTheme.spacing.l
                    ),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                ) {
                    Text(
                        text = "Who can reply",
                        style = MaterialTheme.typography.labelLarge,
                        color = UsTheme.extended.textSecondary
                    )
                    ReplyPolicy.entries.forEach { policy ->
                        val chosen = policy == state.replyPolicy
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clip(RoundedCornerShape(UsTheme.radii.small))
                                .clickable { onPolicy(policy) }
                                .padding(vertical = UsTheme.spacing.s)
                                .testTag("echo_reply_policy:${policy.wire}"),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Text(
                                text = policy.label,
                                style = MaterialTheme.typography.bodyMedium,
                                color = if (chosen) UsTheme.extended.textPrimary else UsTheme.extended.textSecondary,
                                modifier = Modifier.weight(1f),
                            )
                            if (chosen) {
                                Icon(
                                    UsIcons.Check,
                                    contentDescription = "Selected",
                                    tint = UsTheme.extended.accent
                                )
                            }
                        }
                    }
                }
            }
        }
        state.problem?.takeIf { state.entries.any { it.text.isNotBlank() || it.media.isNotEmpty() } }?.let { problem ->
            item(key = "problem") {
                Text(
                    text = problem,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.statusDanger,
                    modifier = Modifier.padding(
                        horizontal = UsTheme.spacing.pageHorizontal
                    ).testTag("echo_composer_problem"),
                )
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
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .testTag("echo_entry:$index"),
    ) {
        Box {
            if (entry.text.isEmpty()) {
                Text(
                    text = if (index == 0) "What's happening?" else "Add another note",
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
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
            ) {
                entry.media.forEach { media ->
                    StagedThumb(
                        media = media,
                        url = media.mediaId?.let(urls::get),
                        onRemove = { onRemoveMedia(media.localKey) },
                    )
                }
            }
        }
        Row(
            modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.s),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            if (canAddMedia) {
                IconButton(onClick = onPick, modifier = Modifier.testTag("echo_pick_media:$index")) {
                    Icon(UsIcons.ImagePlus, contentDescription = "Add photos", tint = UsTheme.extended.accent)
                }
            }
            if (total > 1) {
                IconButton(onClick = onRemove, modifier = Modifier.testTag("echo_remove_entry:$index")) {
                    Icon(UsIcons.Trash, contentDescription = "Remove this note", tint = UsTheme.extended.textTertiary)
                }
            }
            Box(modifier = Modifier.weight(1f))
            Text(
                text = entry.remaining.toString(),
                style = MaterialTheme.typography.labelMedium,
                color = when {
                    entry.remaining < 0 -> UsTheme.extended.statusDanger
                    entry.remaining <= EchoRules.WARN_AT_REMAINING -> UsTheme.extended.statusWarning
                    else -> UsTheme.extended.textTertiary
                },
                modifier = Modifier.testTag("echo_counter:$index"),
            )
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
                color = UsTheme.extended.onMedia
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

internal fun titleFor(mode: ComposeMode): String = when (mode) {
    ComposeMode.NEW -> "New note"
    ComposeMode.REPLY -> "Reply"
    ComposeMode.QUOTE -> "Quote"
    ComposeMode.EDIT -> "Edit note"
}

internal fun submitLabel(mode: ComposeMode, thread: Boolean): String = when {
    mode == ComposeMode.EDIT -> "Save"
    mode == ComposeMode.REPLY -> "Reply"
    thread -> "Post all"
    else -> "Post"
}

private val SPINNER = 18.dp
private val THUMB = 72.dp
private val CLOSE_ICON = 14.dp
