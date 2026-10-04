package com.us.android.feature.doorstep.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.CircularProgressIndicator
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
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.model.toRupeeText
import com.us.android.feature.doorstep.model.toShortRupeeText
import kotlinx.coroutines.delay

/*
 * Doorstep's screen kit, built only from Momentum design-system primitives and
 * tokens, so it reads right in the light and the dark theme: solid cards,
 * shared status colours, the ember gradient kept for the one primary action on
 * a screen. No raw colours (NoRawColourGuardTest). Feast's kit, copied —
 * features may not share code; lift both into :core:ui when a third needs it.
 */

enum class Tone { Neutral, Positive, Warning, Danger, Accent }

@Composable
fun toneColor(tone: Tone): Color = when (tone) {
    Tone.Neutral -> UsTheme.extended.textMuted
    Tone.Positive -> UsTheme.extended.statusSuccess
    Tone.Warning -> UsTheme.extended.statusWarning
    Tone.Danger -> UsTheme.extended.statusDanger
    Tone.Accent -> UsTheme.extended.accentSolid
}

/** The standard Doorstep screen: Momentum scaffold, top bar, content, a bottom bar and a message line. */
@Composable
fun DoorstepScreen(
    title: String,
    onBack: (() -> Unit)?,
    modifier: Modifier = Modifier,
    message: UsMessage? = null,
    onDismissMessage: () -> Unit = {},
    actions: @Composable RowScope.() -> Unit = {},
    bottomBar: @Composable () -> Unit = {},
    content: @Composable (PaddingValues) -> Unit,
) {
    UsScaffold(
        modifier = modifier,
        topBar = { UsTopBar(title = title, onBack = onBack, actions = actions) },
        bottomBar = bottomBar,
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize()) {
            content(padding)
            if (message != null) {
                MessageLine(
                    message = message,
                    onDismiss = onDismissMessage,
                    modifier = Modifier
                        .align(Alignment.BottomCenter)
                        .padding(bottom = padding.calculateBottomPadding() + UsTheme.spacing.xxl),
                )
            }
        }
    }
}

/** List padding under a [DoorstepScreen]: clear of the bars, with room for the message line. */
fun listPadding(padding: PaddingValues): PaddingValues = PaddingValues(
    top = padding.calculateTopPadding() + 8.dp,
    bottom = padding.calculateBottomPadding() + 104.dp,
)

@Composable
fun DoorstepCard(
    modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null,
    highlighted: Boolean = false,
    content: @Composable ColumnScope.() -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    val clickable = if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier
    val border = if (highlighted) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgCardSolid)
            .border(if (highlighted) 2.dp else 1.dp, border, shape)
            .then(clickable)
            .padding(UsTheme.spacing.xxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        content = content,
    )
}

/** A selectable row (an option, an add-on, a slot). */
@Composable
fun ChoiceRow(
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    content: @Composable RowScope.() -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    val border = if (selected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle
    val fill = if (selected) UsTheme.extended.accentSolid.copy(alpha = SELECTED_FILL_ALPHA) else UsTheme.extended.bgCard
    Row(
        modifier = modifier
            .clip(shape)
            .background(fill)
            .border(if (selected) 2.dp else 1.dp, border, shape)
            .clickable(enabled = enabled, onClick = onClick)
            .padding(horizontal = UsTheme.spacing.xl, vertical = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
        content = content,
    )
}

@Composable
fun Pill(text: String, tone: Tone, modifier: Modifier = Modifier) {
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

@Composable
fun SectionLabel(title: String, modifier: Modifier = Modifier, trailing: @Composable RowScope.() -> Unit = {}) {
    Row(
        modifier = modifier.fillMaxWidth().padding(top = UsTheme.spacing.xl, bottom = UsTheme.spacing.xs),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = title.uppercase(),
            style = MaterialTheme.typography.labelMedium,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textDim,
            modifier = Modifier.weight(1f),
        )
        trailing()
    }
}

@Composable
fun LoadingPane(modifier: Modifier = Modifier, label: String? = null) {
    Column(
        modifier = modifier.fillMaxSize(),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        CircularProgressIndicator(color = UsTheme.extended.accentSolid, strokeWidth = 3.dp)
        if (label != null) {
            Text(
                text = label,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = UsTheme.spacing.xxl),
            )
        }
    }
}

@Composable
fun MessagePane(
    title: String,
    body: String,
    modifier: Modifier = Modifier,
    icon: ImageVector = UsIcons.Info,
    iconTint: Color = UsTheme.extended.accentSolid,
    primaryLabel: String? = null,
    onPrimary: () -> Unit = {},
    secondaryLabel: String? = null,
    onSecondary: () -> Unit = {},
) {
    Column(
        modifier = modifier.fillMaxSize().padding(UsTheme.spacing.xxl),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Box(
            modifier = Modifier.size(64.dp).background(UsTheme.extended.bgRaised, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(icon, contentDescription = null, tint = iconTint, modifier = Modifier.size(28.dp))
        }
        Text(
            text = title,
            style = MaterialTheme.typography.titleLarge,
            color = UsTheme.extended.textPrimary,
            textAlign = TextAlign.Center,
            modifier = Modifier.padding(top = 20.dp),
        )
        if (body.isNotBlank()) {
            Text(
                text = body,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(top = UsTheme.spacing.m),
            )
        }
        if (primaryLabel != null) {
            UsButton(text = primaryLabel, onClick = onPrimary, modifier = Modifier.fillMaxWidth().padding(top = 28.dp))
        }
        if (secondaryLabel != null) {
            UsSecondaryButton(
                text = secondaryLabel,
                onClick = onSecondary,
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l),
            )
        }
    }
}

@Composable
fun InfoNote(text: String, modifier: Modifier = Modifier, tone: Tone = Tone.Neutral) {
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

/** One line of a bill: a label and a server-stated amount. */
@Composable
fun MoneyRow(
    label: String,
    amount: Paise,
    modifier: Modifier = Modifier,
    emphasise: Boolean = false,
    muted: Boolean = false,
) {
    Row(modifier = modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = label,
            style = if (emphasise) MaterialTheme.typography.titleMedium else MaterialTheme.typography.bodyMedium,
            fontWeight = if (emphasise) FontWeight.SemiBold else FontWeight.Normal,
            color = if (muted) UsTheme.extended.textMuted else UsTheme.extended.textSecondary,
            modifier = Modifier.weight(1f),
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
        )
        Text(
            text = amount.toRupeeText(),
            style = if (emphasise) MaterialTheme.typography.titleMedium else MaterialTheme.typography.bodyMedium,
            fontWeight = if (emphasise) FontWeight.SemiBold else FontWeight.Normal,
            color = UsTheme.extended.textPrimary,
        )
    }
}

/** A catalogue price with its struck-through MRP when the server sent one. */
@Composable
fun PriceText(price: Paise, mrp: Paise?, modifier: Modifier = Modifier) {
    Row(modifier = modifier, verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = price.toShortRupeeText(),
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = UsTheme.extended.textPrimary,
        )
        if (mrp != null && mrp > price) {
            Spacer(Modifier.width(UsTheme.spacing.s))
            Text(
                text = mrp.toShortRupeeText(),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textDim,
                textDecoration = TextDecoration.LineThrough,
            )
        }
    }
}

/** − n + , for an option quantity. */
@Composable
fun QuantityStepper(
    quantity: Int,
    onDecrease: () -> Unit,
    onIncrease: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Row(
        modifier = modifier.border(1.dp, UsTheme.extended.borderMedium, shape).padding(horizontal = 2.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        IconButton(onClick = onDecrease, enabled = enabled, modifier = Modifier.size(36.dp)) {
            Text("−", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
        }
        Text(
            text = quantity.toString(),
            style = MaterialTheme.typography.titleSmall,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.width(24.dp),
            textAlign = TextAlign.Center,
        )
        IconButton(onClick = onIncrease, enabled = enabled, modifier = Modifier.size(36.dp)) {
            Text("+", style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.accentSolid)
        }
    }
}

/** Five tappable stars. */
@Composable
fun StarPicker(stars: Int, onPick: (Int) -> Unit, modifier: Modifier = Modifier) {
    Row(modifier = modifier, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
        (1..MAX_STARS).forEach { n ->
            IconButton(onClick = { onPick(n) }) {
                Icon(
                    imageVector = UsIcons.Star,
                    contentDescription = "$n star" + if (n == 1) "" else "s",
                    tint = if (n <= stars) UsTheme.extended.statusWarning else UsTheme.extended.textGhost,
                )
            }
        }
    }
}

/** A pinned bottom action with an optional summary line above the button. */
@Composable
fun BottomAction(
    label: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    summary: String? = null,
    enabled: Boolean = true,
    loading: Boolean = false,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCanvas)
            .border(1.dp, UsTheme.extended.borderSubtle)
            .navigationBarsPadding()
            .padding(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.l),
    ) {
        if (summary != null) {
            Text(
                text = summary,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(bottom = UsTheme.spacing.m),
            )
        }
        UsButton(text = label, onClick = onClick, enabled = enabled, loading = loading, modifier = Modifier.fillMaxWidth())
    }
}

/** A transient line at the bottom. Errors stay until dismissed; everything else fades after 4 s. */
@Composable
fun MessageLine(message: UsMessage, onDismiss: () -> Unit, modifier: Modifier = Modifier) {
    val tone = when (message.type) {
        UsMessageType.Error -> Tone.Danger
        UsMessageType.Success -> Tone.Positive
        UsMessageType.Warning -> Tone.Warning
        UsMessageType.Info -> Tone.Accent
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
            .padding(horizontal = UsTheme.spacing.pageHorizontal)
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

fun errorMessage(text: String): UsMessage = UsMessage(text = text, type = UsMessageType.Error)

fun infoMessage(text: String): UsMessage = UsMessage(text = text, type = UsMessageType.Info)

fun successMessage(text: String): UsMessage = UsMessage(text = text, type = UsMessageType.Success)

private const val MESSAGE_MILLIS = 4_000L
private const val PILL_ALPHA = 0.14f
private const val SELECTED_FILL_ALPHA = 0.12f
private const val BANNER_BORDER_ALPHA = 0.5f
private const val MAX_STARS = 5
