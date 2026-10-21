package com.us.android.feature.auth.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * The head of every sign-in security screen: a tinted icon square, a small
 * tracked eyebrow, the title and one paragraph. Mirrors the web's auth cards
 * (`/auth/2fa`, `/auth/step-up`) so the two surfaces read as one product.
 *
 * [tint] is a status token — success for a routine second step, warning for
 * an unusual sign-in — never a raw colour.
 */
@Composable
fun AuthHeader(
    icon: ImageVector,
    eyebrow: String,
    title: String,
    body: String,
    modifier: Modifier = Modifier,
    tint: Color = MaterialTheme.colorScheme.primary,
) {
    Column(modifier = modifier) {
        Box(
            modifier = Modifier
                .size(ICON_SQUARE)
                .background(UsTheme.extended.bgCard, RoundedCornerShape(UsTheme.radii.large)),
            contentAlignment = Alignment.Center,
        ) {
            // Decorative: the title below carries the meaning.
            Icon(imageVector = icon, contentDescription = null, tint = tint, modifier = Modifier.size(ICON_GLYPH))
        }
        Spacer(Modifier.height(UsTheme.spacing.l))
        Text(
            text = eyebrow.uppercase(),
            style = MaterialTheme.typography.labelSmall,
            color = tint,
        )
        Spacer(Modifier.height(UsTheme.spacing.xs))
        Text(
            text = title,
            style = MaterialTheme.typography.headlineSmall,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.semantics { heading() },
        )
        Spacer(Modifier.height(UsTheme.spacing.m))
        Text(
            text = body,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textMuted,
        )
    }
}

private val ICON_SQUARE = 44.dp
private val ICON_GLYPH = 24.dp
