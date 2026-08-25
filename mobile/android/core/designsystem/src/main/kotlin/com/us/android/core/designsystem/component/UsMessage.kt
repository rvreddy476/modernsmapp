package com.us.android.core.designsystem.component

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxScope
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.delay

/**
 * The app's one transient-feedback component.
 *
 * Every "something happened" message goes through this: form failures,
 * network problems, confirmations. One component means one visual language
 * and one accessibility behaviour, instead of a different toast per screen.
 *
 * Design notes:
 *  - No icon dependency. The type is carried by a colour accent stripe and a
 *    tinted surface, which reads as clearly as a glyph and keeps the module
 *    free of `material-icons`.
 *  - Colour is never the *only* signal — the message text always states the
 *    problem, so it survives colour-blindness and greyscale.
 *  - Marked as an assertive live region so TalkBack announces it when it
 *    appears. A visual-only error is invisible to a screen reader.
 */
enum class UsMessageType { Error, Success, Warning, Info }

data class UsMessage(
    val text: String,
    val type: UsMessageType = UsMessageType.Error,
    /**
     * Distinguishes two consecutive identical messages. Without it, tapping a
     * failing button twice shows nothing the second time, and the user thinks
     * the app ignored them.
     */
    val id: Long = System.currentTimeMillis(),
)

/**
 * Floating message anchored to the bottom of a [Box].
 *
 * Auto-dismisses after [durationMillis]; errors stay longer than
 * confirmations because they usually have to be read and acted on.
 */
@Composable
fun BoxScope.UsMessageHost(
    message: UsMessage?,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
    durationMillis: Long? = null,
) {
    // Keyed on id so a repeated identical message restarts the timer rather
    // than being swallowed by the previous one.
    LaunchedEffect(message?.id) {
        val current = message ?: return@LaunchedEffect
        val duration = durationMillis ?: when (current.type) {
            UsMessageType.Error, UsMessageType.Warning -> ERROR_DURATION_MILLIS
            else -> DEFAULT_DURATION_MILLIS
        }
        delay(duration)
        onDismiss()
    }

    AnimatedVisibility(
        visible = message != null,
        enter = slideInVertically(animationSpec = tween(ENTER_MILLIS)) { it } +
            fadeIn(tween(ENTER_MILLIS)),
        exit = slideOutVertically(animationSpec = tween(EXIT_MILLIS)) { it } +
            fadeOut(tween(EXIT_MILLIS)),
        modifier = modifier
            .align(Alignment.BottomCenter)
            .padding(bottom = UsTheme.spacing.xxxxl),
    ) {
        message?.let { UsMessageCard(it, onDismiss) }
    }
}

@Composable
private fun UsMessageCard(message: UsMessage, onDismiss: () -> Unit) {
    val accent = message.type.accent()

    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.large))
            .background(MaterialTheme.colorScheme.surface)
            .background(accent.copy(alpha = TINT_ALPHA))
            .clickable(onClick = onDismiss)
            .semantics { liveRegion = LiveRegionMode.Assertive },
        verticalAlignment = Alignment.CenterVertically,
    ) {
        // The accent stripe. Deliberately part of the layout rather than a
        // border, so it cannot be clipped away by the rounded corner.
        Box(
            modifier = Modifier
                .width(ACCENT_WIDTH)
                .align(Alignment.CenterVertically)
                .background(accent)
                .padding(vertical = 22.dp),
        )
        Column(
            modifier = Modifier.padding(
                horizontal = UsTheme.spacing.xxl,
                vertical = UsTheme.spacing.xxl,
            ),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(
                text = message.text,
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textPrimary,
            )
        }
    }
}

@Composable
private fun UsMessageType.accent(): Color = when (this) {
    UsMessageType.Error -> MaterialTheme.colorScheme.error
    UsMessageType.Success -> UsTheme.extended.statusSuccess
    UsMessageType.Warning -> UsTheme.extended.statusWarning
    UsMessageType.Info -> MaterialTheme.colorScheme.primary
}

private const val TINT_ALPHA = 0.14f
private val ACCENT_WIDTH = 4.dp
private const val ENTER_MILLIS = 220
private const val EXIT_MILLIS = 160
private const val DEFAULT_DURATION_MILLIS = 3_000L
private const val ERROR_DURATION_MILLIS = 5_000L

@Preview(name = "Messages", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun UsMessagePreview() {
    UsTheme {
        Column(
            modifier = Modifier
                .background(MaterialTheme.colorScheme.background)
                .padding(UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            UsMessageCard(
                UsMessage("That email is already registered. Try signing in instead.", UsMessageType.Error),
            ) {}
            UsMessageCard(
                UsMessage("Account created — check your inbox to verify it.", UsMessageType.Success),
            ) {}
            UsMessageCard(
                UsMessage("You're offline. We'll retry when you're back.", UsMessageType.Warning),
            ) {}
            UsMessageCard(
                UsMessage("Enter the code from your authenticator app.", UsMessageType.Info),
            ) {}
        }
    }
}
