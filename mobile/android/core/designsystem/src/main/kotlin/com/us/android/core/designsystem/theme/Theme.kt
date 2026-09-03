package com.us.android.core.designsystem.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.remember
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp

private val UsDarkColorScheme = darkColorScheme(
    primary = UsColorTokens.AccentOrange,
    onPrimary = Color.White,
    primaryContainer = UsColorTokens.AccentRed,
    onPrimaryContainer = Color.White,
    secondary = UsColorTokens.PostgramPrimary,
    onSecondary = Color.White,
    secondaryContainer = UsColorTokens.PostgramSecondary,
    onSecondaryContainer = Color.White,
    tertiary = UsColorTokens.PosttubePrimary,
    onTertiary = Color.Black,
    background = UsColorTokens.BgPrimary,
    onBackground = UsColorTokens.TextPrimary,
    surface = UsColorTokens.BgSecondary,
    onSurface = UsColorTokens.TextPrimary,
    surfaceVariant = UsColorTokens.BgTertiary,
    onSurfaceVariant = UsColorTokens.TextMuted,
    error = UsColorTokens.StatusError,
    onError = Color.White,
    outline = UsColorTokens.BorderMedium,
    outlineVariant = UsColorTokens.BorderSubtle,
    scrim = Color.Black,
)

private val UsLightColorScheme = lightColorScheme(
    primary = UsColorTokens.AccentOrange,
    onPrimary = Color.White,
    primaryContainer = UsColorTokens.AccentRed,
    onPrimaryContainer = Color.White,
    secondary = UsColorTokens.PostgramPrimary,
    onSecondary = Color.White,
    secondaryContainer = UsColorTokens.PostgramSecondary,
    onSecondaryContainer = Color.White,
    tertiary = UsColorTokens.PosttubePrimary,
    onTertiary = Color.Black,
    background = UsColorTokens.Light.BgPrimary,
    onBackground = UsColorTokens.Light.TextPrimary,
    surface = UsColorTokens.Light.BgSecondary,
    onSurface = UsColorTokens.Light.TextPrimary,
    surfaceVariant = UsColorTokens.Light.BgTertiary,
    onSurfaceVariant = UsColorTokens.Light.TextMuted,
    error = UsColorTokens.StatusError,
    onError = Color.White,
    outline = UsColorTokens.Light.BorderMedium,
    outlineVariant = UsColorTokens.Light.BorderSubtle,
    scrim = Color.Black,
)

// Radii ported from app_spacing.dart: 8 / 12 / 16 / 20.
private val UsShapes = Shapes(
    extraSmall = RoundedCornerShape(4.dp),
    small = RoundedCornerShape(8.dp),
    medium = RoundedCornerShape(12.dp),
    large = RoundedCornerShape(16.dp),
    extraLarge = RoundedCornerShape(20.dp),
)

/**
 * The single theme entry point. Every screen and every @Preview wraps in this.
 *
 * [darkTheme] defaults to the system setting — the Figma light frames (81:*,
 * 2026-09-01) are the sign-off the old dark-only rule was waiting for.
 * Dynamic colour stays deliberately off — the brand ramp is the identity.
 */
@Composable
fun UsTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    val extendedColors = remember(darkTheme) {
        if (darkTheme) DarkExtendedColors else LightExtendedColors
    }
    val radii = remember { UsRadii() }
    val spacing = remember { UsSpacing() }

    CompositionLocalProvider(
        LocalUsExtendedColors provides extendedColors,
        LocalUsRadii provides radii,
        LocalUsSpacing provides spacing,
    ) {
        MaterialTheme(
            colorScheme = if (darkTheme) UsDarkColorScheme else UsLightColorScheme,
            typography = UsTypography,
            shapes = UsShapes,
            content = content,
        )
    }
}

/** Accessors for tokens Material 3 has no slot for. */
object UsTheme {
    val extended: UsExtendedColors
        @Composable
        @ReadOnlyComposable
        get() = LocalUsExtendedColors.current

    val radii: UsRadii
        @Composable
        @ReadOnlyComposable
        get() = LocalUsRadii.current

    val spacing: UsSpacing
        @Composable
        @ReadOnlyComposable
        get() = LocalUsSpacing.current
}
