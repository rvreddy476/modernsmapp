package com.us.android.feature.chat.ui.community

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsAvatar
import com.us.android.core.designsystem.component.UsAvatarSize
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.chat.ui.home.ChatTogglePill
import com.us.android.feature.chat.ui.home.HeaderGlyph
import com.us.android.feature.chat.ui.home.pressScale

/**
 * The chat forms' text field: a label above, a raised field with a hairline
 * that turns red under a problem, the problem or a counter beneath. One
 * drawing for the group description, the community form and the composer.
 */
@Composable
internal fun ChatFormField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    placeholder: String = "",
    problem: String? = null,
    counter: String? = null,
    singleLine: Boolean = true,
    minLines: Int = 1,
    leading: String? = null,
    tag: String? = null,
    trailing: (@Composable () -> Unit)? = null,
) {
    val shape = RoundedCornerShape(UsTheme.radii.panel)
    val outline = if (problem != null) MaterialTheme.colorScheme.error else UsTheme.extended.borderSubtle
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textSecondary,
        )
        BasicTextField(
            value = value,
            onValueChange = onValueChange,
            singleLine = singleLine,
            minLines = minLines,
            textStyle = MaterialTheme.typography.bodyLarge.copy(color = UsTheme.extended.textPrimary),
            cursorBrush = SolidColor(UsTheme.extended.accentSolid),
            modifier = Modifier
                .fillMaxWidth()
                .background(UsTheme.extended.bgRaised, shape)
                .border(HAIRLINE, outline, shape)
                .padding(horizontal = FIELD_PADDING, vertical = FIELD_VERTICAL)
                .semantics { contentDescription = label }
                .then(if (tag != null) Modifier.testTag(tag) else Modifier),
            decorationBox = { inner ->
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)
                ) {
                    if (leading != null) {
                        Text(
                            text = leading,
                            style = MaterialTheme.typography.bodyLarge,
                            color = UsTheme.extended.textMuted
                        )
                    }
                    Box(modifier = Modifier.weight(1f)) {
                        if (value.isEmpty() && placeholder.isNotEmpty()) {
                            Text(
                                text = placeholder,
                                style = MaterialTheme.typography.bodyLarge,
                                color = UsTheme.extended.textDim,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                        inner()
                    }
                    trailing?.invoke()
                }
            },
        )
        val hint = problem ?: counter
        if (hint != null) {
            Text(
                text = hint,
                style = MaterialTheme.typography.bodySmall,
                color = if (problem != null) MaterialTheme.colorScheme.error else UsTheme.extended.textDim,
            )
        }
    }
}

/** A person in a picker or a roster: avatar, name, @username, and a pill or a glyph on the right. */
@Composable
internal fun PersonRow(
    userId: String,
    name: String,
    subtitle: String?,
    avatarUrl: String?,
    modifier: Modifier = Modifier,
    pillText: String? = null,
    pillSelected: Boolean = false,
    busy: Boolean = false,
    onPill: (() -> Unit)? = null,
    onRemove: (() -> Unit)? = null,
    onClick: (() -> Unit)? = null,
    tag: String? = null,
) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        modifier = modifier
            .fillMaxWidth()
            .then(if (onClick != null) Modifier.pressScale(onClick) else Modifier)
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.m)
            .then(if (tag != null) Modifier.testTag(tag) else Modifier),
    ) {
        UsAvatar(name = name, size = UsAvatarSize.Medium, seed = userId, imageUrl = avatarUrl)
        Column(modifier = Modifier.weight(1f)) {
            Text(
                text = name,
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (!subtitle.isNullOrBlank()) {
                Text(
                    text = subtitle,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        if (pillText != null && onPill != null) {
            ChatTogglePill(text = pillText, selected = pillSelected, onClick = onPill, busy = busy)
        }
        if (onRemove != null) {
            HeaderGlyph(
                icon = UsIcons.UserMinus,
                description = "Remove $name",
                onClick = onRemove,
                size = REMOVE_TARGET,
                glyph = REMOVE_GLYPH,
                tint = UsTheme.extended.textMuted,
            )
        }
    }
}

/** A choice between two words — Public / Private — as two pills, the chosen one white. */
@Composable
internal fun TwoWayChoice(
    label: String,
    options: List<Pair<String, String>>,
    selected: String,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(modifier = modifier, verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textSecondary,
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            options.forEach { (value, text) ->
                ChatTogglePill(
                    text = text,
                    selected = value == selected,
                    onClick = { onSelect(value) },
                    tag = "chat_choice:$value",
                )
            }
        }
    }
}

/** A picture slot: the picked/current image, or a dashed-looking glass square with a camera. */
@Composable
internal fun PictureSlot(
    imageUrl: String?,
    name: String,
    onPick: () -> Unit,
    modifier: Modifier = Modifier,
    busy: Boolean = false,
) {
    Box(
        contentAlignment = Alignment.BottomEnd,
        modifier = modifier
            .size(SLOT_SIZE)
            .pressScale(onPick, enabled = !busy)
            .testTag("chat_picture_slot"),
    ) {
        UsAvatar(name = name, size = UsAvatarSize.Large, seed = name, imageUrl = imageUrl)
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(SLOT_BADGE)
                .background(UsTheme.extended.ctaGradient, RoundedCornerShape(UsTheme.radii.full))
                .border(HAIRLINE, UsTheme.extended.bgCanvas, RoundedCornerShape(UsTheme.radii.full)),
        ) {
            Icon(
                imageVector = if (busy) UsIcons.Clock else UsIcons.Camera,
                contentDescription = null,
                tint = Color.White,
                modifier = Modifier.size(SLOT_GLYPH),
            )
        }
    }
}

/** A short line under a form saying what went wrong, in the error colour. */
@Composable
internal fun FormError(text: String?, modifier: Modifier = Modifier) {
    if (text == null) return
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.error,
        modifier = modifier.padding(vertical = UsTheme.spacing.s).testTag("chat_form_error"),
    )
}

/** The fixed-height rule the forms keep between sections. */
@Composable
internal fun FormGap() = Box(modifier = Modifier.height(UsTheme.spacing.xxl))

private val HAIRLINE = 1.dp
private val FIELD_PADDING = 14.dp
private val FIELD_VERTICAL = 12.dp
private val REMOVE_TARGET = 36.dp
private val REMOVE_GLYPH = 20.dp
private val SLOT_SIZE = 96.dp
private val SLOT_BADGE = 30.dp
private val SLOT_GLYPH = 16.dp
