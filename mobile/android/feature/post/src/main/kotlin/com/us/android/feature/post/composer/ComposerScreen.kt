package com.us.android.feature.post.composer

import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Checkbox
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Write a post.
 *
 * Renders [ComposerUiState] and calls back. It performs no network, database or
 * file work and keeps no parallel copy of upload or publish truth — the one
 * state object is the only truth, which is what stops the screen showing
 * "uploading" for an upload that already failed.
 *
 * ## AUDIENCE IS SHOWN, NOT CHOSEN
 *
 * There is a visible `Audience: Public` row and no way to change it. Hiding the
 * audience would be dishonest; offering `followers` or `private` would be
 * worse, because the platform does not enforce those on the post read path,
 * the profile list or feed fan-out. A privacy control that records a choice
 * nothing honours is a false promise, and people decide what to post based on
 * it.
 */
@Composable
fun ComposerScreen(
    onClose: () -> Unit,
    onPublished: (postId: String) -> Unit,
    viewModel: ComposerViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    val pickImage = rememberLauncherForActivityResult(
        // The system Photo Picker: a per-URI read grant, so no storage
        // permission is requested and no dialog appears for choosing one photo.
        ActivityResultContracts.PickVisualMedia(),
    ) { uri -> uri?.let { viewModel.onImagePicked(it.toString()) } }

    // Navigation happens on the SERVER's id, once, after the create returned.
    LaunchedEffect(state.phase) {
        (state.phase as? ComposerPhase.Published)?.let { onPublished(it.postId) }
    }

    // LEAVE ONLY ONCE THE DRAFT IS DURABLY GONE (C-CLB-2).
    //
    // The confirm button used to call `onDiscardConfirmed()` and `onClose()`
    // on the same tap. Popping the destination clears the navigation-owned
    // ViewModel and cancels its scope, so the Room delete raced the pop and
    // often lost — content the user explicitly discarded came back the next
    // time they opened the composer.
    //
    // `discarded` is set only after `drafts.clear()` returns, so navigating on
    // it makes the pop a consequence of the delete instead of a sibling of it.
    LaunchedEffect(state.discarded) {
        if (state.discarded) onClose()
    }

    // SYSTEM BACK GOES THROUGH THE SAME DISCARD DECISION (C-P0-3).
    //
    // Only the top-bar arrow was handled, so the back gesture and the hardware
    // key popped the destination directly — no confirmation, and the draft
    // silently gone along with the creation key that stops a retry publishing
    // twice. Two ways out of a screen must not have two different meanings.
    //
    // Enabled only while there is something to lose; an empty composer should
    // just close.
    BackHandler(enabled = state.hasContent && !state.confirmingDiscard) {
        viewModel.onDiscardRequested()
    }

    UsScaffold(
        topBar = {
            UsTopBar(
                title = "New post",
                onBack = { viewModel.onDiscardRequested() },
            )
        },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            UsTextField(
                value = state.text,
                onValueChange = viewModel::onTextChanged,
                label = "Post",
                placeholder = "What's happening?",
                singleLine = false,
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { contentDescription = "Post text" },
            )

            // The counter appears only as the limit approaches. A permanent
            // counter reads as a target; one that appears near the ceiling
            // reads as a warning.
            if (state.textCodePoints > MAX_TEXT_CODE_POINTS - COUNTER_VISIBLE_WITHIN) {
                Text(
                    text = "${state.textCodePoints} / $MAX_TEXT_CODE_POINTS",
                    style = MaterialTheme.typography.bodySmall,
                    color = if (state.textTooLong) {
                        MaterialTheme.colorScheme.error
                    } else {
                        UsTheme.extended.textMuted
                    },
                )
            }

            ImageSection(
                state = state,
                onPick = { pickImage.launch(PickVisualMediaRequest()) },
                onRemove = viewModel::onImageRemoved,
                onAltTextChanged = viewModel::onAltTextChanged,
                onDecorativeChanged = viewModel::onDecorativeChanged,
            )

            AudienceRow()

            LanguageRow(language = state.language, onChange = viewModel::onLanguageChanged)

            ProgressAndErrors(state = state, onRetry = viewModel::onRetry)

            PostAction(state = state, onPost = viewModel::onPostPressed)

            if (state.confirmingDiscard) {
                DiscardConfirmation(
                    // NOT onClose() here. See the discarded LaunchedEffect above.
                    onConfirm = viewModel::onDiscardConfirmed,
                    onCancel = viewModel::onDiscardCancelled,
                )
            }
        }
    }
}

@Composable
private fun ImageSection(
    state: ComposerUiState,
    onPick: () -> Unit,
    onRemove: () -> Unit,
    onAltTextChanged: (String) -> Unit,
    onDecorativeChanged: (Boolean) -> Unit,
) {
    if (!state.hasImage) {
        UsSecondaryButton(
            text = "Add photo",
            onClick = onPick,
            enabled = !state.isBusy,
            modifier = Modifier.fillMaxWidth(),
        )
        return
    }

    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Text(
                text = "Photo attached",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            UsSecondaryButton(text = "Remove", onClick = onRemove, enabled = !state.isBusy)
        }

        // Accessibility is REQUIRED, not suggested: an image with neither a
        // description nor a decorative mark cannot be posted. The two are
        // mutually exclusive and the reducer enforces that.
        UsTextField(
            value = state.altText,
            onValueChange = onAltTextChanged,
            label = "Describe this photo",
            placeholder = "For people using a screen reader",
            singleLine = false,
            enabled = !state.decorative && !state.isBusy,
            modifier = Modifier
                .fillMaxWidth()
                .semantics { contentDescription = "Photo description" },
        )

        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            Checkbox(
                checked = state.decorative,
                onCheckedChange = onDecorativeChanged,
                enabled = !state.isBusy,
                modifier = Modifier.semantics {
                    contentDescription = "This photo is decorative"
                },
            )
            Text(
                text = "This photo is decorative",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
        }

        // STATED AS SOON AS IT APPLIES, not only after a rejected attempt.
        //
        // This was gated on `showValidationErrors`, which the reducer sets when
        // Post is pressed while the post is not valid — but Post is DISABLED
        // while the decision is missing, so `onPostPressed` can never fire from
        // this screen and the message could never render. The person was left
        // with a greyed-out button and no visible reason. The reducer keeps its
        // own guard as defence in depth for any other caller.
        //
        // Muted and polite rather than red and assertive: at this point nothing
        // has gone wrong, the requirement simply has not been met yet.
        if (!state.altDecisionMade) {
            RequirementText("Add a description, or mark the photo as decorative.")
        }
    }
}

/** Non-interactive by design. See the screen KDoc. */
@Composable
private fun AudienceRow() {
    Text(
        text = "Audience: Public",
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textMuted,
        modifier = Modifier
            .fillMaxWidth()
            // One node to the screen reader, announced as a fact rather than
            // read as a control someone can try to activate.
            .clearAndSetSemantics {
                contentDescription = "Audience: Public. Everyone can see this post."
            },
    )
}

@Composable
private fun LanguageRow(language: String, onChange: (String) -> Unit) {
    UsTextField(
        value = language,
        onValueChange = onChange,
        label = "Language",
        placeholder = "en",
        singleLine = true,
        modifier = Modifier
            .fillMaxWidth()
            .semantics { contentDescription = "Post language" },
    )
}

@Composable
private fun ProgressAndErrors(state: ComposerUiState, onRetry: () -> Unit) {
    when (val phase = state.phase) {
        is ComposerPhase.PreparingImage -> StatusText("Preparing photo…")

        is ComposerPhase.Uploading -> Column(
            modifier = Modifier
                .fillMaxWidth()
                .semantics(mergeDescendants = true) {
                    liveRegion = LiveRegionMode.Polite
                    contentDescription = "Uploading photo, ${(phase.fraction * PERCENT).toInt()} percent"
                },
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        ) {
            LinearProgressIndicator(
                progress = { phase.fraction },
                modifier = Modifier.fillMaxWidth(),
            )
            Text(
                text = "Uploading photo… ${(phase.fraction * PERCENT).toInt()}%",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }

        is ComposerPhase.Confirming -> StatusText("Finishing upload…")
        is ComposerPhase.Publishing -> StatusText("Posting…")

        // No success text here. The screen navigates away on Published, and a
        // "Posted!" message rendered before that would be claiming success the
        // server has not confirmed.
        is ComposerPhase.Published -> Unit

        is ComposerPhase.RetryableFailure -> Column(
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        ) {
            ErrorText(phase.message)
            // Retry sends the IDENTICAL bytes under the IDENTICAL creation key,
            // so a request that did reach the server replays rather than
            // posting twice.
            UsSecondaryButton(
                text = "Retry",
                onClick = onRetry,
                modifier = Modifier.fillMaxWidth(),
            )
        }

        // NO Retry control: this failure cannot succeed on a repeat, and a
        // button that is guaranteed to fail reads as a broken app.
        is ComposerPhase.TerminalFailure -> ErrorText(phase.message)

        is ComposerPhase.Editing -> Unit
    }
}

@Composable
private fun PostAction(state: ComposerUiState, onPost: () -> Unit) {
    UsButton(
        text = "Post",
        onClick = onPost,
        enabled = state.canPost,
        modifier = Modifier
            .fillMaxWidth()
            .semantics {
                // A disabled control always states its reason. "Post, disabled"
                // with no explanation is the most common accessibility failure
                // in a composer.
                contentDescription = when (state.blockedReason) {
                    PostBlockedReason.Empty -> "Post. Unavailable: add text or a photo first."
                    PostBlockedReason.TextTooLong -> "Post. Unavailable: your post is too long."
                    PostBlockedReason.MissingAltDecision ->
                        "Post. Unavailable: describe the photo or mark it decorative."

                    PostBlockedReason.MediaNotReady -> "Post. Unavailable: the photo is still uploading."
                    PostBlockedReason.Busy -> "Post. In progress."
                    null -> "Post"
                }
            },
    )
}

@Composable
private fun DiscardConfirmation(onConfirm: () -> Unit, onCancel: () -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        Text(
            text = "Discard this post?",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            UsSecondaryButton(text = "Keep editing", onClick = onCancel, modifier = Modifier.weight(1f))
            UsButton(text = "Discard", onClick = onConfirm, modifier = Modifier.weight(1f))
        }
    }
}

@Composable
private fun StatusText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.semantics { liveRegion = LiveRegionMode.Polite },
    )
}

/** An unmet requirement. Not a failure, so not styled as one. */
@Composable
private fun RequirementText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier
            .fillMaxWidth()
            .semantics { liveRegion = LiveRegionMode.Polite },
    )
}

@Composable
private fun ErrorText(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier
            .fillMaxWidth()
            // Assertive: an error the user must act on should interrupt, not
            // wait for the next natural pause.
            .semantics { liveRegion = LiveRegionMode.Assertive },
    )
}

/** How close to the ceiling the counter appears. */
private const val COUNTER_VISIBLE_WITHIN = 200

/** Fraction-to-percentage conversion for the upload readout. */
private const val PERCENT = 100
