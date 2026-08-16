package com.us.android.navigation

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.BuildConfig
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Phase 0's only real screen: a live gallery of the design system.
 *
 * It exists so the token port from the Flutter reference can be reviewed on a
 * real device at real density, rather than trusted from a diff. It is not a
 * product screen and will be deleted when Phase 2 brings the auth flow and
 * the tab shell.
 */
@Composable
fun DesignSystemGalleryScreen() {
    val scroll = rememberScrollState()
    var email by remember { mutableStateOf("") }
    var password by remember { mutableStateOf("short") }

    UsScaffold(applyPageGutter = false) {
        Column(
            modifier = Modifier
                .verticalScroll(scroll)
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = 48.dp),
        ) {
            Header()

            SectionTitle("Brand gradients")
            GradientRow("CTA", UsTheme.extended.ctaGradient)
            GradientRow("Postbook", UsTheme.extended.postbookGradient)
            GradientRow("Postgram", UsTheme.extended.postgramGradient)
            GradientRow("Posttube", UsTheme.extended.posttubeGradient)

            SectionTitle("Story ring")
            Box(
                modifier = Modifier
                    .size(72.dp)
                    .clip(CircleShape)
                    .background(UsTheme.extended.storyRingGradient)
                    .padding(3.dp)
                    .clip(CircleShape)
                    .background(MaterialTheme.colorScheme.background),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = "US",
                    style = MaterialTheme.typography.titleLarge,
                    color = UsTheme.extended.textPrimary,
                )
            }

            SectionTitle("Text ramp (7 steps)")
            TextRamp()

            SectionTitle("Type scale")
            TypeScale()

            SectionTitle("Status colours")
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                Swatch("error", MaterialTheme.colorScheme.error)
                Swatch("warn", UsTheme.extended.statusWarning)
                Swatch("ok", UsTheme.extended.statusSuccess)
                Swatch("live", UsTheme.extended.liveRed)
                Swatch("online", UsTheme.extended.onlineGreen)
            }

            SectionTitle("Surfaces & radii")
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                SurfaceChip("8dp", UsTheme.radii.small)
                SurfaceChip("12dp", UsTheme.radii.medium)
                SurfaceChip("16dp", UsTheme.radii.large)
                SurfaceChip("20dp", UsTheme.radii.extraLarge)
            }

            SectionTitle("Buttons")
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                UsButton("Continue", onClick = {}, modifier = Modifier.fillMaxWidth())
                UsButton("Loading", onClick = {}, modifier = Modifier.fillMaxWidth(), loading = true)
                UsButton("Disabled", onClick = {}, modifier = Modifier.fillMaxWidth(), enabled = false)
                UsSecondaryButton("Not now", onClick = {}, modifier = Modifier.fillMaxWidth())
            }

            SectionTitle("Text fields")
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl)) {
                UsTextField(
                    value = email,
                    onValueChange = { email = it },
                    label = "Email",
                    placeholder = "you@example.com",
                )
                UsTextField(
                    value = password,
                    onValueChange = { password = it },
                    label = "Password",
                    isPassword = true,
                    errorText = "Password must be at least 8 characters",
                )
            }
        }
    }
}

@Composable
private fun Header() {
    Spacer(Modifier.height(UsTheme.spacing.xxl))
    Text(
        text = "US",
        style = MaterialTheme.typography.headlineMedium,
        color = UsTheme.extended.textPrimary,
    )
    Text(
        text = "Unified Services · design system",
        style = MaterialTheme.typography.bodyLarge,
        color = UsTheme.extended.textMuted,
    )
    Text(
        text = "Phase 0 scaffold · ${BuildConfig.ENVIRONMENT} · ${BuildConfig.API_BASE_URL}",
        style = MaterialTheme.typography.labelMedium,
        color = UsTheme.extended.textDim,
    )
}

@Composable
private fun SectionTitle(text: String) {
    Spacer(Modifier.height(UsTheme.spacing.xxxxl))
    Text(
        text = text.uppercase(),
        style = MaterialTheme.typography.labelSmall,
        color = UsTheme.extended.textMuted,
    )
    Spacer(Modifier.height(UsTheme.spacing.l))
}

@Composable
private fun GradientRow(label: String, brush: Brush) {
    Row(
        modifier = Modifier.padding(bottom = UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(
            modifier = Modifier
                .weight(1f)
                .height(36.dp)
                .clip(RoundedCornerShape(UsTheme.radii.small))
                .background(brush),
        )
        Spacer(Modifier.size(UsTheme.spacing.l))
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textTertiary,
        )
    }
}

@Composable
private fun TextRamp() {
    val steps = listOf(
        "textPrimary" to UsTheme.extended.textPrimary,
        "textSecondary" to UsTheme.extended.textSecondary,
        "textTertiary" to UsTheme.extended.textTertiary,
        "textMuted" to UsTheme.extended.textMuted,
        "textDim" to UsTheme.extended.textDim,
        "textDimmest" to UsTheme.extended.textDimmest,
        "textGhost" to UsTheme.extended.textGhost,
    )
    steps.forEach { (name, color) ->
        Text(
            text = "$name — the quick brown fox",
            style = MaterialTheme.typography.bodyMedium,
            color = color,
            modifier = Modifier.padding(bottom = 2.dp),
        )
    }
}

@Composable
private fun TypeScale() {
    val t = MaterialTheme.typography
    val rows = listOf(
        "headlineMedium 28/900" to t.headlineMedium,
        "headlineSmall 26/900" to t.headlineSmall,
        "titleLarge 17/700" to t.titleLarge,
        "titleMedium 15/700" to t.titleMedium,
        "bodyLarge 14.5/400" to t.bodyLarge,
        "bodyMedium 14/500" to t.bodyMedium,
        "bodySmall 13/500" to t.bodySmall,
        "labelLarge 13/600" to t.labelLarge,
        "labelMedium 11/600" to t.labelMedium,
        "labelSmall 10/700" to t.labelSmall,
    )
    rows.forEach { (label, style) ->
        Text(
            text = label,
            style = style,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.padding(bottom = 4.dp),
        )
    }
}

@Composable
private fun Swatch(label: String, color: Color) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Box(
            modifier = Modifier
                .size(44.dp)
                .clip(RoundedCornerShape(UsTheme.radii.small))
                .background(color),
        )
        Spacer(Modifier.height(UsTheme.spacing.xs))
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textMuted,
            textAlign = TextAlign.Center,
        )
    }
}

@Composable
private fun SurfaceChip(label: String, radius: androidx.compose.ui.unit.Dp) {
    Box(
        modifier = Modifier
            .size(64.dp)
            .clip(RoundedCornerShape(radius))
            .background(UsTheme.extended.bgCard)
            .border(1.dp, UsTheme.extended.borderMedium, RoundedCornerShape(radius)),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = UsTheme.extended.textTertiary,
        )
    }
}

@Preview(name = "Design system gallery", showBackground = true, backgroundColor = 0xFF000000, heightDp = 1600)
@Composable
private fun DesignSystemGalleryPreview() {
    UsTheme { DesignSystemGalleryScreen() }
}
