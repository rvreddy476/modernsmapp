package com.us.android.feature.kitchen.ui

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
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.delay

/*
 * The Kitchen's small screen kit, assembled from Momentum design-system
 * primitives and tokens only — no raw hex, no second palette. Cards sit on the
 * solid navy surface, status reads through the shared status colours, and the
 * ember accent is kept for primary actions and the live signal.
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
fun KitchenPill(text: String, tone: PillTone, modifier: Modifier = Modifier) {
    val color = toneColor(tone)
    Text(
        text = text,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.SemiBold,
        color = color,
        maxLines = 1,
        modifier = modifier
            .background(color.copy(alpha = 0.14f), RoundedCornerShape(UsTheme.radii.full))
            .padding(horizontal = 10.dp, vertical = 4.dp),
    )
}

@Composable
fun KitchenCard(
    modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null,
    content: @Composable ColumnScope.() -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    val clickable = if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clip(shape)
            .background(UsTheme.extended.bgCardSolid)
            .border(1.dp, UsTheme.extended.borderSubtle, shape)
            .then(clickable)
            .padding(UsTheme.spacing.xxl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
        content = content,
    )
}

@Composable
fun SectionHeader(
    title: String,
    modifier: Modifier = Modifier,
    trailing: @Composable RowScope.() -> Unit = {},
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(top = UsTheme.spacing.l, bottom = UsTheme.spacing.xs),
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
fun LoadingPane(modifier: Modifier = Modifier) {
    Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator(color = UsTheme.extended.accentSolid, strokeWidth = 3.dp)
    }
}

@Composable
fun MessagePane(
    title: String,
    body: String,
    modifier: Modifier = Modifier,
    icon: ImageVector = UsIcons.Info,
    primaryLabel: String? = null,
    onPrimary: () -> Unit = {},
    secondaryLabel: String? = null,
    onSecondary: () -> Unit = {},
) {
    Column(
        modifier = modifier
            .fillMaxSize()
            .padding(UsTheme.spacing.xxl),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
    ) {
        Box(
            modifier = Modifier
                .size(64.dp)
                .background(UsTheme.extended.bgRaised, CircleShape),
            contentAlignment = Alignment.Center,
        ) {
            Icon(icon, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(28.dp))
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
fun LabeledValue(label: String, value: String, modifier: Modifier = Modifier, emphasise: Boolean = false) {
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
            color = UsTheme.extended.textPrimary,
        )
    }
}

@Composable
fun FieldError(text: String?, modifier: Modifier = Modifier) {
    if (text == null) return
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.statusDanger,
        modifier = modifier.padding(top = UsTheme.spacing.xs),
    )
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
            .border(1.dp, color.copy(alpha = 0.5f), shape)
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

/** The standard Kitchen screen: Momentum scaffold, top bar, content and a bottom message line. */
@Composable
fun KitchenScreen(
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
                MessageBanner(
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

/** List padding under a [KitchenScreen]: clear of the bars, with room for the message line. */
fun listPadding(padding: PaddingValues): PaddingValues = PaddingValues(
    top = padding.calculateTopPadding() + 8.dp,
    bottom = padding.calculateBottomPadding() + 96.dp,
)

private const val MESSAGE_MILLIS = 4_000L
