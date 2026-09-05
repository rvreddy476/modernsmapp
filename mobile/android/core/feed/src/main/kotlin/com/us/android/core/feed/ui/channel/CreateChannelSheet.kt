package com.us.android.core.feed.ui.channel

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardCapitalization
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.feed.data.ChannelAbout
import com.us.android.core.feed.data.ChannelName
import com.us.android.core.model.Channel

/**
 * "Create your channel" (founder, 2026-09-05: channel before video). A
 * Momentum sheet over whatever is on screen: the profile photo as the
 * channel's face, the name with its counter, the `@handle` prefilled from a
 * suggestion and checked as it is typed, an optional About, and one
 * primary button. Lives in `:core:feed` so both the Create hub and Tube can
 * open the SAME sheet — features must not depend on each other.
 *
 * [onCreated] fires once with the new channel; the caller decides what
 * continues (the video form, the You page). [onDismiss] is the way out
 * without one.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun CreateChannelSheet(
    onCreated: (Channel) -> Unit,
    onDismiss: () -> Unit,
    viewModel: CreateChannelViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)

    state.created?.let { channel ->
        LaunchedEffect(channel.userId) { onCreated(channel) }
    }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
        modifier = Modifier.testTag("create_channel_sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding()
                .imePadding(),
        ) {
            GrabHandle()
            SheetHeader(onClose = onDismiss)
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            AvatarPreview(state)
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            NameField(state, viewModel::onNameChanged)
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            HandleField(state, viewModel::onHandleChanged, viewModel::useSuggestion)
            Spacer(Modifier.height(UsTheme.spacing.xxl))
            AboutField(state, viewModel::onAboutChanged)
            state.error?.let { message ->
                Spacer(Modifier.height(UsTheme.spacing.l))
                Text(
                    text = message,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.testTag("create_channel_error"),
                )
            }
            Spacer(Modifier.height(UsTheme.spacing.xxxxl))
            UsButton(
                text = "Create channel",
                onClick = viewModel::create,
                enabled = state.canSubmit && !state.prefilling,
                loading = state.submitting,
                modifier = Modifier
                    .fillMaxWidth()
                    .testTag("create_channel_submit"),
            )
        }
    }
}

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

@Composable
private fun SheetHeader(onClose: () -> Unit) {
    Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Column(modifier = Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
            Text(
                text = "Create your channel",
                style = MaterialTheme.typography.titleLarge.copy(fontSize = TITLE_SIZE),
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "Videos post under your channel. You can change this later.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        Box(
            modifier = Modifier
                .size(CLOSE_BUTTON)
                .clip(CircleShape)
                .background(UsTheme.extended.glassBg)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onClose,
                )
                .semantics {
                    contentDescription = "Close"
                    role = Role.Button
                }
                .testTag("create_channel_close"),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = UsIcons.Close,
                contentDescription = null,
                tint = UsTheme.extended.textPrimary,
                modifier = Modifier.size(CLOSE_GLYPH),
            )
        }
    }
}

/** The profile photo, as the channel will wear it. The channel has no photo of its own yet. */
@Composable
private fun AvatarPreview(state: CreateChannelUiState) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        UsAvatar(
            name = state.avatarName,
            seed = state.avatarSeed.ifBlank { state.avatarName },
            size = UsAvatarSize.Chat,
            imageUrl = state.avatarUrl,
            hasRing = true,
            contentDescription = "Your channel's photo",
        )
        Text(
            text = "Your profile photo is your channel's photo.",
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
        )
    }
}

@Composable
private fun NameField(state: CreateChannelUiState, onChange: (String) -> Unit) {
    SheetField(
        spec = FieldSpec(
            label = "Channel name",
            placeholder = "Your channel's name",
            testTag = "create_channel_name",
            keyboard = KeyboardOptions(capitalization = KeyboardCapitalization.Words, imeAction = ImeAction.Next),
            counter = "${state.name.length}/${ChannelName.MAX_LENGTH}",
        ),
        value = state.name,
        onValueChange = onChange,
        enabled = !state.submitting,
        error = state.nameError,
    )
}

/** What the handle field says under itself for a live check, and the server's suggestion when taken. */
private data class HandleHint(val text: String, val color: Color, val suggestion: String? = null)

@Composable
private fun handleHint(state: CreateChannelUiState): HandleHint = when (val check = state.check) {
    HandleCheck.Idle -> HandleHint(
        state.handleProblem ?: "Letters, numbers, dots and underscores",
        UsTheme.extended.textMuted,
    )
    HandleCheck.Checking -> HandleHint("Checking…", UsTheme.extended.textMuted)
    HandleCheck.Available -> HandleHint("Available", UsTheme.extended.statusSuccess)
    is HandleCheck.Taken -> HandleHint("Taken", MaterialTheme.colorScheme.error, check.suggestion)
    HandleCheck.Unreachable -> HandleHint("Couldn't check right now", UsTheme.extended.textMuted)
}

@Composable
private fun HandleField(state: CreateChannelUiState, onChange: (String) -> Unit, onUseSuggestion: () -> Unit) {
    val hint = handleHint(state)
    SheetField(
        spec = FieldSpec(
            label = "Handle",
            placeholder = "yourhandle",
            testTag = "create_channel_handle",
            keyboard = KeyboardOptions(keyboardType = KeyboardType.Ascii, imeAction = ImeAction.Next),
            prefix = "@",
            hint = hint.text,
            hintColor = hint.color,
        ),
        value = state.handle,
        onValueChange = onChange,
        enabled = !state.submitting,
        error = state.handleError,
    )
    hint.suggestion?.let { alternative ->
        Text(
            text = "Try @$alternative",
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier
                .padding(top = UsTheme.spacing.s)
                .clip(RoundedCornerShape(UsTheme.radii.full))
                .background(UsTheme.extended.glassBg)
                .clickable(
                    interactionSource = remember { MutableInteractionSource() },
                    indication = null,
                    onClick = onUseSuggestion,
                )
                .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
                .semantics { role = Role.Button }
                .testTag("create_channel_suggestion"),
        )
    }
}

@Composable
private fun AboutField(state: CreateChannelUiState, onChange: (String) -> Unit) {
    SheetField(
        spec = FieldSpec(
            label = "About (optional)",
            placeholder = "What your channel is about",
            testTag = "create_channel_about",
            keyboard = KeyboardOptions(
                capitalization = KeyboardCapitalization.Sentences,
                imeAction = ImeAction.Default,
            ),
            counter = "${state.about.length}/${ChannelAbout.MAX_LENGTH}",
            singleLine = false,
        ),
        value = state.about,
        onValueChange = onChange,
        enabled = !state.submitting,
        error = state.aboutError,
    )
}

/** The fixed shape of one field: what it is called, what it hints, how it takes input. */
private data class FieldSpec(
    val label: String,
    val placeholder: String,
    val testTag: String,
    val keyboard: KeyboardOptions,
    val prefix: String? = null,
    val counter: String? = null,
    val hint: String? = null,
    val hintColor: Color? = null,
    val singleLine: Boolean = true,
)

/**
 * A bare field under a small label with a hairline beneath — the reel
 * form's field, so the sheet reads as part of the same family — and a
 * counter, a hint or an error under the line.
 */
@Composable
private fun SheetField(
    spec: FieldSpec,
    value: String,
    onValueChange: (String) -> Unit,
    enabled: Boolean,
    error: String?,
) {
    Column(modifier = Modifier.fillMaxWidth()) {
        Text(
            text = spec.label,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textMuted,
        )
        Spacer(Modifier.height(UsTheme.spacing.s))
        FieldInput(spec = spec, value = value, onValueChange = onValueChange, enabled = enabled)
        Spacer(Modifier.height(UsTheme.spacing.s))
        FieldUnderline(spec = spec, error = error)
    }
}

/** The prefix and the bare text field on one line. */
@Composable
private fun FieldInput(spec: FieldSpec, value: String, onValueChange: (String) -> Unit, enabled: Boolean) {
    Row(verticalAlignment = Alignment.CenterVertically) {
        if (spec.prefix != null) {
            Text(
                text = spec.prefix,
                style = MaterialTheme.typography.bodyLarge.copy(fontSize = FIELD_SIZE),
                color = UsTheme.extended.textMuted,
            )
        }
        Box(modifier = Modifier.weight(1f)) {
            if (value.isEmpty()) {
                Text(
                    text = spec.placeholder,
                    style = MaterialTheme.typography.bodyLarge.copy(fontSize = FIELD_SIZE),
                    color = UsTheme.extended.textDim,
                )
            }
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                enabled = enabled,
                singleLine = spec.singleLine,
                maxLines = if (spec.singleLine) 1 else ABOUT_LINES,
                keyboardOptions = spec.keyboard,
                textStyle = MaterialTheme.typography.bodyLarge.copy(
                    fontSize = FIELD_SIZE,
                    color = UsTheme.extended.textPrimary,
                ),
                cursorBrush = SolidColor(UsTheme.extended.accentSolid),
                modifier = Modifier
                    .fillMaxWidth()
                    .semantics { contentDescription = spec.label }
                    .testTag(spec.testTag),
            )
        }
    }
}

/** The hairline, then the hint or error at the left and the counter at the right. */
@Composable
private fun FieldUnderline(spec: FieldSpec, error: String?) {
    val hintColor = spec.hintColor ?: UsTheme.extended.textMuted
    val counter = spec.counter
    val testTag = spec.testTag
    Column(modifier = Modifier.fillMaxWidth()) {
        val hint = spec.hint
        HorizontalDivider(
            color = if (error != null) MaterialTheme.colorScheme.error else UsTheme.extended.borderSubtle,
            thickness = HAIRLINE,
        )
        Spacer(Modifier.height(UsTheme.spacing.xs))
        Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            val line = error ?: hint
            if (line != null) {
                Text(
                    text = line,
                    style = MaterialTheme.typography.labelMedium,
                    color = if (error != null) MaterialTheme.colorScheme.error else hintColor,
                    modifier = Modifier
                        .weight(1f)
                        .testTag("$testTag:hint"),
                )
            } else {
                Spacer(Modifier.weight(1f))
            }
            if (counter != null) {
                Text(
                    text = counter,
                    style = MaterialTheme.typography.labelMedium,
                    color = UsTheme.extended.textDim,
                )
            }
        }
    }
}

private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private const val ABOUT_LINES = 4

private val SHEET_RADIUS = 28.dp
private val HAIRLINE = 1.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HANDLE_TOP = 8.dp
private val HANDLE_BOTTOM = 12.dp
private val CLOSE_BUTTON = 30.dp
private val CLOSE_GLYPH = 14.dp
private val TITLE_SIZE = 20.sp
private val FIELD_SIZE = 16.sp
