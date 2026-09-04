package com.us.android.feature.post.createhub

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.interaction.collectIsFocusedAsState
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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
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
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * A short picker over the form — Audience and Category share it.
 *
 * A sheet rather than a dropdown: the Create sheet set the idiom, and a
 * Material `DropdownMenu` is the one control on this screen that would look
 * like it came from somewhere else. Tapping a row picks and closes.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ReelOptionSheet(
    title: String,
    options: List<ReelOption>,
    selected: String,
    onPick: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    ReelSheet(onDismiss = onDismiss, testTag = "reel-sheet-${title.lowercase()}") {
        SheetTitle(title)
        Spacer(Modifier.height(UsTheme.spacing.m))
        LazyColumn(modifier = Modifier.weight(1f, fill = false)) {
            items(options.size) { index ->
                val option = options[index]
                OptionRow(option = option, selected = option.value == selected, onClick = { onPick(option.value) })
            }
        }
    }
}

@Composable
private fun OptionRow(option: ReelOption, selected: Boolean, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .pressDim(interaction)
            .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.l)
            .semantics {
                role = Role.RadioButton
                this.selected = selected
            }
            .testTag("reel-option-${option.value.ifBlank { "none" }}"),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = option.label,
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
            )
            option.hint?.let {
                Text(text = it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
        if (selected) {
            Icon(
                imageVector = UsIcons.Check,
                contentDescription = null,
                tint = UsTheme.extended.accentSolid,
                modifier = Modifier.size(CHECK_SIZE),
            )
        }
    }
}

/**
 * A typed place name. No maps SDK and no location permission in this pass:
 * the row's value is exactly what the user wrote, and "Remove" clears it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun ReelLocationSheet(
    initial: String,
    onDone: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    var draft by rememberSaveable { mutableStateOf(initial) }
    val focus = remember { FocusRequester() }
    ReelSheet(onDismiss = onDismiss, testTag = "reel-sheet-location") {
        SheetTitle("Add location")
        Spacer(Modifier.height(UsTheme.spacing.l))
        ReelInputField(
            value = draft,
            onValueChange = { draft = it },
            placeholder = "Where was this?",
            icon = UsIcons.MapPin,
            onDone = { onDone(draft.trim()) },
            modifier = Modifier
                .focusRequester(focus)
                .testTag("reel-location-input"),
        )
        Spacer(Modifier.height(UsTheme.spacing.xxl))
        UsButton(
            text = "Done",
            onClick = { onDone(draft.trim()) },
            modifier = Modifier
                .fillMaxWidth()
                .testTag("reel-location-done"),
        )
        if (initial.isNotBlank()) {
            Spacer(Modifier.height(UsTheme.spacing.s))
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(UsTheme.radii.full))
                    .clickable { onDone("") }
                    .padding(vertical = UsTheme.spacing.l)
                    .testTag("reel-location-remove"),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = "Remove location",
                    style = MaterialTheme.typography.labelLarge,
                    color = UsTheme.extended.textMuted,
                )
            }
        }
        LaunchedEffect(Unit) { focus.requestFocus() }
    }
}

/**
 * The form's one text input shape — a pill on the card colour with a
 * hairline that warms to the accent on focus. No Material outline, no
 * floating label: the icon says what the field is for.
 */
@Composable
internal fun ReelInputField(
    value: String,
    onValueChange: (String) -> Unit,
    placeholder: String,
    icon: ImageVector,
    modifier: Modifier = Modifier,
    onDone: () -> Unit = {},
) {
    val interaction = remember { MutableInteractionSource() }
    val focused by interaction.collectIsFocusedAsState()
    val shape = RoundedCornerShape(UsTheme.radii.full)
    val ring = if (focused) UsTheme.extended.accentSolid.copy(alpha = RING_ALPHA) else UsTheme.extended.borderSubtle
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgCard)
            .border(HAIRLINE, ring, shape)
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            imageVector = icon,
            contentDescription = null,
            tint = if (focused) UsTheme.extended.accentSolid else UsTheme.extended.textMuted,
            modifier = Modifier.size(FIELD_ICON),
        )
        Spacer(Modifier.width(UsTheme.spacing.m))
        Box(modifier = Modifier.weight(1f)) {
            if (value.isEmpty()) {
                Text(
                    text = placeholder,
                    style = MaterialTheme.typography.bodyLarge,
                    color = UsTheme.extended.textDim,
                )
            }
            BasicTextField(
                value = value,
                onValueChange = onValueChange,
                singleLine = true,
                interactionSource = interaction,
                textStyle = MaterialTheme.typography.bodyLarge.copy(color = UsTheme.extended.textPrimary),
                cursorBrush = SolidColor(UsTheme.extended.accentSolid),
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                keyboardActions = KeyboardActions(onDone = { onDone() }),
                modifier = modifier
                    .fillMaxWidth()
                    .semantics { contentDescription = placeholder },
            )
        }
    }
}

// ── The sheet chrome ────────────────────────────────────────────────────

/** The Create sheet's shell: solid card colour, 28dp top corners, a grab handle. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ReelSheet(
    onDismiss: () -> Unit,
    testTag: String,
    content: @Composable androidx.compose.foundation.layout.ColumnScope.() -> Unit,
) {
    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
        modifier = Modifier.testTag(testTag),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = UsTheme.spacing.xxl)
                .padding(bottom = UsTheme.spacing.l)
                .navigationBarsPadding()
                .imePadding(),
        ) {
            Box(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = UsTheme.spacing.m, bottom = UsTheme.spacing.l),
                contentAlignment = Alignment.Center,
            ) {
                Box(
                    modifier = Modifier
                        .size(width = HANDLE_WIDTH, height = HANDLE_HEIGHT)
                        .clip(CircleShape)
                        .background(UsTheme.extended.textMuted.copy(alpha = HANDLE_ALPHA)),
                )
            }
            content()
        }
    }
}

@Composable
private fun SheetTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleLarge.copy(fontSize = SHEET_TITLE_SIZE),
        color = UsTheme.extended.textPrimary,
        modifier = Modifier.padding(horizontal = UsTheme.spacing.m),
    )
}

// ── Metrics ─────────────────────────────────────────────────────────────

private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private const val RING_ALPHA = 0.7f

private val SHEET_RADIUS = 28.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val HAIRLINE = 1.dp
private val CHECK_SIZE = 18.dp
private val FIELD_ICON = 18.dp
private val SHEET_TITLE_SIZE = 20.sp
