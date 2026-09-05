package com.us.android.feature.post.createhub

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import java.time.Instant
import java.time.ZoneId

/**
 * The HASHTAGS field (2026-09-05): the chips already added, the input
 * that turns `#a b` or `a, b` into chips as they are typed, and the
 * server's suggestions under it. Thirty at most; the counter says so.
 */
@Composable
internal fun HashtagsField(
    hashtags: List<String>,
    input: String,
    suggestions: List<String>,
    enabled: Boolean,
    actions: HashtagActions,
) {
    Column(modifier = Modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = "Hashtags",
                style = MaterialTheme.typography.labelLarge,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = "${hashtags.size}/${Hashtags.MAX_HASHTAGS}",
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.textDim,
                modifier = Modifier.testTag("reel-hashtag-count"),
            )
        }
        if (hashtags.isNotEmpty()) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState())
                    .testTag("reel-hashtag-chips"),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                hashtags.forEach { tag ->
                    RemovableChip(text = "#$tag", onRemove = { actions.onRemove(tag) }, enabled = enabled)
                }
            }
        }
        ReelInputField(
            value = input,
            onValueChange = { if (enabled) actions.onInputChanged(it) },
            placeholder = if (hashtags.size < Hashtags.MAX_HASHTAGS) "Add hashtags" else "That's the limit",
            icon = UsIcons.Hash,
            onDone = actions.onCommit,
            modifier = Modifier.testTag("reel-hashtag-input"),
        )
        if (suggestions.isNotEmpty()) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .horizontalScroll(rememberScrollState())
                    .testTag("reel-hashtag-suggestions"),
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            ) {
                suggestions.forEach { tag ->
                    SuggestionChip(text = "#$tag", onClick = { actions.onPickSuggestion(tag) })
                }
            }
        }
    }
}

/** A glass chip with an × — a hashtag, or a mentioned person. */
@Composable
internal fun RemovableChip(text: String, onRemove: () -> Unit, enabled: Boolean, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Row(
        modifier = modifier
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
            .padding(start = UsTheme.spacing.l, end = UsTheme.spacing.m)
            .padding(vertical = UsTheme.spacing.s),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
        Icon(
            imageVector = UsIcons.Close,
            contentDescription = "Remove $text",
            tint = UsTheme.extended.textMuted,
            modifier = Modifier
                .size(CHIP_CLOSE)
                .clip(CircleShape)
                .clickable(enabled = enabled, onClick = onRemove),
        )
    }
}

/** A suggestion: a quieter chip, no ×; tapping adds it. */
@Composable
private fun SuggestionChip(text: String, onClick: () -> Unit) {
    val interaction = remember { MutableInteractionSource() }
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Text(
        text = text,
        style = MaterialTheme.typography.labelLarge,
        color = UsTheme.extended.accentSolid,
        modifier = Modifier
            .clip(shape)
            .border(HAIRLINE, UsTheme.extended.borderSubtle, shape)
            .clickable(interactionSource = interaction, indication = null, onClick = onClick)
            .pressDim(interaction)
            .padding(horizontal = UsTheme.spacing.l, vertical = UsTheme.spacing.s)
            .semantics { role = Role.Button },
    )
}

/**
 * The bottom bar (2026-09-05): "Schedule" beside the primary "Post". A
 * set schedule turns the left button into "Scheduled for Fri 6 Sep,
 * 18:30" and the primary into "Schedule" — what the tap will do.
 */
@Composable
internal fun PostActions(
    publishAt: Instant?,
    canPost: Boolean,
    busy: Boolean,
    retrying: Boolean,
    onSchedule: () -> Unit,
    onPost: () -> Unit,
    description: String,
) {
    val zone = remember { ZoneId.systemDefault() }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        ScheduleButton(
            label = publishAt?.let { "Scheduled for ${ScheduleWindow.label(it, zone)}" } ?: "Schedule",
            enabled = !busy,
            onClick = onSchedule,
            modifier = Modifier.weight(SCHEDULE_WEIGHT),
        )
        UsButton(
            text = when {
                retrying -> "Try again"
                publishAt != null -> "Schedule"
                else -> "Post"
            },
            onClick = onPost,
            enabled = canPost,
            loading = busy,
            modifier = Modifier
                .weight(1f)
                .testTag("reel-post")
                .semantics { contentDescription = description },
        )
    }
}

/** A capsule with the clock: outlined, so the primary beside it stays the primary. */
@Composable
private fun ScheduleButton(label: String, enabled: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val interaction = remember { MutableInteractionSource() }
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Row(
        modifier = modifier
            .height(BUTTON_HEIGHT)
            .clip(shape)
            .background(UsTheme.extended.glassBg)
            .border(HAIRLINE, UsTheme.extended.borderMedium, shape)
            .clickable(interactionSource = interaction, indication = null, enabled = enabled, onClick = onClick)
            .pressDim(interaction)
            .padding(horizontal = UsTheme.spacing.l)
            .semantics {
                role = Role.Button
                contentDescription = label
            }
            .testTag("reel-schedule"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.Center,
    ) {
        Icon(
            imageVector = UsIcons.Clock,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(CLOCK_GLYPH),
        )
        Spacer(Modifier.width(UsTheme.spacing.s))
        Text(
            text = label,
            style = MaterialTheme.typography.labelLarge,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
            maxLines = 1,
            overflow = TextOverflow.Ellipsis,
        )
    }
}

private const val SCHEDULE_WEIGHT = 1.3f
private val HAIRLINE = 1.dp
private val CHIP_CLOSE = 16.dp
private val CLOCK_GLYPH = 16.dp
private val BUTTON_HEIGHT = 48.dp
