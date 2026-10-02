package com.us.android.feature.dating.home

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.ui.DatingPhoto
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.Tone

/**
 * A mutual spark, on a screen of its own: who it is, and the two things to do
 * next. It stands in for Dating's home while it is up — Back, like "Keep
 * browsing", returns to wherever the match was made.
 *
 * The photo is whichever the view model has: the card's own at first, then the
 * variant the server allows a match once `GET /matches/:id` has answered.
 */
@Composable
internal fun MatchCelebrationScreen(
    match: MatchCelebration,
    onSayHello: () -> Unit,
    onKeepBrowsing: () -> Unit,
) {
    BackHandler(onBack = onKeepBrowsing)
    val named = match.name.isNotBlank()
    UsScaffold { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .padding(vertical = UsTheme.spacing.xxxxl),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Spacer(Modifier.weight(1f))
            Pill("New match", Tone.Accent)
            Text(
                text = MatchCopy.TITLE,
                style = MaterialTheme.typography.headlineMedium,
                color = UsTheme.extended.textPrimary,
                textAlign = TextAlign.Center,
                modifier = Modifier.semantics { heading() },
            )
            Text(
                text = MatchCopy.body(match.name),
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textSecondary,
                textAlign = TextAlign.Center,
            )
            val shape = RoundedCornerShape(UsTheme.radii.card)
            Box(Modifier.padding(top = UsTheme.spacing.xxl).fillMaxWidth(PHOTO_WIDTH).aspectRatio(PHOTO_RATIO)) {
                DatingPhoto(
                    url = match.photoUrl,
                    contentDescription = if (named) "${match.name}'s photo" else "Their photo",
                    modifier = Modifier
                        .fillMaxSize()
                        .clip(shape)
                        .border(BORDER, UsTheme.extended.ctaGradient, shape),
                )
                Box(
                    modifier = Modifier
                        .align(Alignment.BottomEnd)
                        .padding(UsTheme.spacing.l)
                        .size(BADGE)
                        .background(UsTheme.extended.bgCanvas, CircleShape)
                        .border(BORDER, UsTheme.extended.ctaGradient, CircleShape),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(UsIcons.HeartHandshake, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(22.dp))
                }
            }
            if (named) {
                Text(match.name, style = MaterialTheme.typography.titleLarge, color = UsTheme.extended.textPrimary)
            }
            Spacer(Modifier.weight(1f))
            UsButton(text = MatchCopy.SAY_HELLO, loading = match.opening, onClick = onSayHello, modifier = Modifier.fillMaxWidth())
            UsSecondaryButton(text = MatchCopy.KEEP_BROWSING, enabled = !match.opening, onClick = onKeepBrowsing, modifier = Modifier.fillMaxWidth())
        }
    }
}

/** The match screen's words. */
object MatchCopy {
    const val TITLE = "You both sparked"
    const val SAY_HELLO = "Say hello"
    const val KEEP_BROWSING = "Keep browsing"

    fun body(name: String): String =
        if (name.isBlank()) "The spark went both ways. Start the conversation." else "You and $name sparked each other. Start the conversation."
}

private const val PHOTO_WIDTH = 0.62f
private val BORDER = 2.dp
private val BADGE = 44.dp
