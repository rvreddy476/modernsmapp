package com.us.android.feature.mopedu.rider.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.delay

/*
 * Mopedu's small screen kit, from Momentum design-system primitives and
 * tokens only. The same shapes as :feature:rider's ui/RiderUi.kt (features may
 * not share code); lift both into :core:ui with the kyc-ui lift.
 */

enum class PillTone { Neutral, Positive, Warning, Danger, Accent }

@Composable
internal fun toneColor(tone: PillTone): Color = when (tone) {
    PillTone.Neutral -> UsTheme.extended.textMuted
    PillTone.Positive -> UsTheme.extended.statusSuccess
    PillTone.Warning -> UsTheme.extended.statusWarning
    PillTone.Danger -> UsTheme.extended.statusDanger
    PillTone.Accent -> UsTheme.extended.accentSolid
}

@Composable
fun MopeduPill(text: String, tone: PillTone, modifier: Modifier = Modifier) {
    val color = toneColor(tone)
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.SemiBold,
        color = color,
        maxLines = 1,
        modifier = modifier
            .background(color.copy(alpha = PILL_ALPHA), RoundedCornerShape(UsTheme.radii.full))
            .padding(horizontal = 10.dp, vertical = 4.dp),
    )
}

/** The floating card over the map: the solid card surface, a subtle border, the card radius. */
@Composable
fun MopeduCard(
    modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null,
    highlighted: Boolean = false,
    content: @Composable ColumnScope.() -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.card)
    val clickable = if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier
    val border = if (highlighted) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgCardSolid)
            .border(if (highlighted) 2.dp else 1.dp, border, shape)
            .then(clickable)
            .padding(UsTheme.spacing.xxxxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        content = content,
    )
}

/** A selectable row inside a card (a vehicle option, a payment method). */
@Composable
fun MopeduChoiceRow(
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable ColumnScope.() -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    val border = if (selected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle
    val fill = if (selected) UsTheme.extended.accentSolid.copy(alpha = SELECTED_FILL_ALPHA) else UsTheme.extended.bgCard
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(fill)
            .border(if (selected) 2.dp else 1.dp, border, shape)
            .clickable(onClick = onClick)
            .padding(UsTheme.spacing.xl),
        content = content,
    )
}

@Composable
fun MopeduDivider(modifier: Modifier = Modifier) {
    HorizontalDivider(modifier = modifier, color = UsTheme.extended.borderSubtle)
}

@Composable
fun CardHeading(title: String, body: String? = null, modifier: Modifier = Modifier, tone: PillTone? = null) {
    Column(modifier = modifier.fillMaxWidth()) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            color = if (tone != null) toneColor(tone) else UsTheme.extended.textPrimary,
        )
        if (!body.isNullOrBlank()) {
            Text(
                text = body,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(top = 2.dp),
            )
        }
    }
}

@Composable
fun Eyebrow(text: String, tone: PillTone = PillTone.Accent, modifier: Modifier = Modifier) {
    Text(
        text = text.uppercase(),
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.Bold,
        color = toneColor(tone),
        modifier = modifier,
    )
}

@Composable
fun LabeledValue(label: String, value: String, modifier: Modifier = Modifier, emphasise: Boolean = false, tone: PillTone? = null) {
    Row(modifier = modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
            modifier = Modifier.weight(1f),
        )
        Text(
            text = value,
            style = if (emphasise) MaterialTheme.typography.titleMedium else MaterialTheme.typography.bodyMedium,
            fontWeight = if (emphasise) FontWeight.SemiBold else FontWeight.Normal,
            color = if (tone != null) toneColor(tone) else UsTheme.extended.textPrimary,
        )
    }
}

/** A pickup or drop line: a coloured dot, a small label and the address. */
@Composable
fun StopRow(label: String, address: String, dotColor: Color, modifier: Modifier = Modifier) {
    Row(modifier = modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Box(modifier = Modifier.size(12.dp).background(dotColor, CircleShape))
        Spacer(Modifier.width(UsTheme.spacing.l))
        Column {
            Text(text = label, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textDim)
            Text(
                text = address.ifBlank { "Not set" },
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.Medium,
                color = UsTheme.extended.textPrimary,
            )
        }
    }
}

@Composable
fun InfoNote(text: String, modifier: Modifier = Modifier, tone: PillTone = PillTone.Neutral) {
    Row(modifier = modifier.fillMaxWidth(), verticalAlignment = Alignment.Top) {
        Icon(
            imageVector = UsIcons.Info,
            contentDescription = null,
            tint = toneColor(tone),
            modifier = Modifier.padding(top = 2.dp).size(16.dp),
        )
        Spacer(Modifier.width(UsTheme.spacing.m))
        Text(text = text, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
    }
}

@Composable
fun LoadingPane(modifier: Modifier = Modifier) {
    Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator(color = UsTheme.extended.accentSolid, strokeWidth = 3.dp)
    }
}

/** A transient line at the bottom of the screen. Errors stay until dismissed; everything else fades after 4 s. */
@Composable
fun MessageBanner(message: UsMessage, onDismiss: () -> Unit, modifier: Modifier = Modifier) {
    val tone = when (message.type) {
        UsMessageType.Error -> PillTone.Danger
        UsMessageType.Success -> PillTone.Positive
        UsMessageType.Warning -> PillTone.Warning
        UsMessageType.Info -> PillTone.Accent
    }
    val color = toneColor(tone)
    val shape = RoundedCornerShape(UsTheme.radii.panel)
    if (message.type != UsMessageType.Error) {
        LaunchedEffect(message) {
            delay(MESSAGE_MILLIS)
            onDismiss()
        }
    }
    Row(
        modifier = modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgRaised, shape)
            .border(1.dp, color.copy(alpha = BANNER_BORDER_ALPHA), shape)
            .padding(start = 14.dp, top = 4.dp, bottom = 4.dp, end = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.size(8.dp).background(color, CircleShape))
        Text(
            text = message.text,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f).padding(horizontal = 10.dp, vertical = 8.dp),
        )
        IconButton(onClick = onDismiss) {
            Icon(UsIcons.Close, contentDescription = "Dismiss", tint = UsTheme.extended.textMuted)
        }
    }
}

private const val PILL_ALPHA = 0.14f
private const val SELECTED_FILL_ALPHA = 0.12f
private const val BANNER_BORDER_ALPHA = 0.5f
private const val MESSAGE_MILLIS = 4_000L
