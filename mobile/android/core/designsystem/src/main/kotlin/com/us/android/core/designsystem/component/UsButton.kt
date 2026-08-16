package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Primary action button.
 *
 * Uses the CTA gradient from the Flutter reference rather than a flat fill,
 * because the gradient is part of the brand identity rather than decoration.
 * Falls back to a flat disabled surface when not enabled.
 */
@Composable
fun UsButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    loading: Boolean = false,
) {
    val shape = androidx.compose.foundation.shape.RoundedCornerShape(UsTheme.radii.full)
    Button(
        onClick = onClick,
        modifier = modifier.defaultMinSize(minHeight = 48.dp),
        enabled = enabled && !loading,
        shape = shape,
        contentPadding = PaddingValues(0.dp),
        colors = ButtonDefaults.buttonColors(
            containerColor = Color.Transparent,
            disabledContainerColor = Color.Transparent,
        ),
    ) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .clip(shape)
                .then(
                    // A loading button keeps the gradient. Only a genuinely
                    // disabled one goes flat — otherwise "submitting" is
                    // indistinguishable from "you can't press this".
                    if (enabled) {
                        Modifier.background(UsTheme.extended.ctaGradient)
                    } else {
                        Modifier.background(UsTheme.extended.bgCardHover)
                    },
                )
                .padding(vertical = 14.dp),
            contentAlignment = Alignment.Center,
        ) {
            if (loading) {
                CircularProgressIndicator(
                    // size(), not defaultMinSize(): the latter only sets a
                    // floor, so the indicator kept its ~40dp default and
                    // rendered as an oversized arc.
                    modifier = Modifier.size(20.dp),
                    strokeWidth = 2.dp,
                    color = Color.White,
                )
            } else {
                Text(
                    text = text,
                    style = MaterialTheme.typography.labelLarge,
                    color = if (enabled) Color.White else UsTheme.extended.textDim,
                )
            }
        }
    }
}

/** Secondary action. Outlined, no gradient — it must not compete with [UsButton]. */
@Composable
fun UsSecondaryButton(
    text: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
) {
    OutlinedButton(
        onClick = onClick,
        modifier = modifier.defaultMinSize(minHeight = 48.dp),
        enabled = enabled,
        shape = androidx.compose.foundation.shape.RoundedCornerShape(UsTheme.radii.full),
        border = androidx.compose.foundation.BorderStroke(1.dp, UsTheme.extended.borderMedium),
    ) {
        Text(
            text = text,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textSecondary,
        )
    }
}

@Preview(name = "Buttons", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun UsButtonPreview() {
    UsTheme {
        Column(
            modifier = Modifier
                .background(MaterialTheme.colorScheme.background)
                .padding(UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            UsButton(text = "Continue", onClick = {}, modifier = Modifier.fillMaxWidth())
            UsButton(
                text = "Loading",
                onClick = {},
                modifier = Modifier.fillMaxWidth(),
                loading = true,
            )
            UsButton(
                text = "Disabled",
                onClick = {},
                modifier = Modifier.fillMaxWidth(),
                enabled = false,
            )
            UsSecondaryButton(text = "Not now", onClick = {}, modifier = Modifier.fillMaxWidth())
        }
    }
}
