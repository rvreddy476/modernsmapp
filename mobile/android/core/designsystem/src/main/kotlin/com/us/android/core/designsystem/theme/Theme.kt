package com.us.android.core.designsystem.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.ColorScheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Shapes
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.ReadOnlyComposable
import androidx.compose.runtime.remember
import androidx.compose.ui.unit.dp

/**
 * Material's roles, filled from the same tokens as [UsExtendedColors] so a
 * Material component (a switch, a text field, a dialog) reads as the same
 * product as a house one.
 *
 * `primary` / `onPrimary` are the web's exact pair in each theme (the ink
 * under a white label on light, the light ink under a near-black label on
 * dark), because Material colours both sides of that pair itself. A house
 * control that paints its own label uses `UsTheme.extended.accentStrong`
 * and `onAccent` instead.
 */
internal val UsDarkColorScheme: ColorScheme = darkColorScheme(
    primary = UsColorTokens.Dark.AccentText,
    onPrimary = UsColorTokens.Dark.OnPrimary,
    primaryContainer = UsColorTokens.Dark.AccentTint,
    onPrimaryContainer = UsColorTokens.Dark.TextPrimary,
    secondary = UsColorTokens.Dark.AccentText,
    onSecondary = UsColorTokens.Dark.OnPrimary,
    secondaryContainer = UsColorTokens.Dark.Sunken,
    onSecondaryContainer = UsColorTokens.Dark.TextPrimary,
    tertiary = UsColorTokens.Dark.Info,
    onTertiary = UsColorTokens.Dark.OnPrimary,
    background = UsColorTokens.Dark.Canvas,
    onBackground = UsColorTokens.Dark.TextPrimary,
    surface = UsColorTokens.Dark.Card,
    onSurface = UsColorTokens.Dark.TextPrimary,
    surfaceVariant = UsColorTokens.Dark.Sunken,
    onSurfaceVariant = UsColorTokens.Dark.TextMuted,
    error = UsColorTokens.Dark.Danger,
    onError = UsColorTokens.Dark.OnPrimary,
    outline = UsColorTokens.Dark.Border,
    outlineVariant = UsColorTokens.Dark.BorderSubtle,
    scrim = UsColorTokens.Stage,
    // What Material makes a sheet, a menu and a dialog of when the caller
    // names no colour (2026-10-02). Left unset these are Material's baseline
    // lavender greys, which are not the web's. The tint is the surface
    // itself, so tonal elevation adds no colour of its own.
    surfaceContainerLowest = UsColorTokens.Dark.Sheet,
    surfaceContainerLow = UsColorTokens.Dark.Sheet,
    surfaceContainer = UsColorTokens.Dark.Sheet,
    surfaceContainerHigh = UsColorTokens.Dark.Sheet,
    surfaceContainerHighest = UsColorTokens.Dark.Sunken,
    surfaceTint = UsColorTokens.Dark.Card,
    inverseSurface = UsColorTokens.Dark.SelectedPill,
    inverseOnSurface = UsColorTokens.Dark.OnSelectedPill,
    inversePrimary = UsColorTokens.Light.AccentText,
)

internal val UsLightColorScheme: ColorScheme = lightColorScheme(
    primary = UsColorTokens.Light.AccentText,
    onPrimary = UsColorTokens.Light.OnPrimary,
    primaryContainer = UsColorTokens.Light.AccentTint,
    onPrimaryContainer = UsColorTokens.Light.TextPrimary,
    secondary = UsColorTokens.Light.AccentText,
    onSecondary = UsColorTokens.Light.OnPrimary,
    secondaryContainer = UsColorTokens.Light.Sunken,
    onSecondaryContainer = UsColorTokens.Light.TextPrimary,
    tertiary = UsColorTokens.Light.Info,
    onTertiary = UsColorTokens.Light.OnPrimary,
    background = UsColorTokens.Light.Canvas,
    onBackground = UsColorTokens.Light.TextPrimary,
    surface = UsColorTokens.Light.Card,
    onSurface = UsColorTokens.Light.TextPrimary,
    surfaceVariant = UsColorTokens.Light.Sunken,
    onSurfaceVariant = UsColorTokens.Light.TextMuted,
    error = UsColorTokens.Light.Danger,
    onError = UsColorTokens.Light.OnPrimary,
    outline = UsColorTokens.Light.Border,
    outlineVariant = UsColorTokens.Light.BorderSubtle,
    scrim = UsColorTokens.Stage,
    // The same roles as the dark scheme, from the light block.
    surfaceContainerLowest = UsColorTokens.Light.Sheet,
    surfaceContainerLow = UsColorTokens.Light.Sheet,
    surfaceContainer = UsColorTokens.Light.Sheet,
    surfaceContainerHigh = UsColorTokens.Light.Sheet,
    surfaceContainerHighest = UsColorTokens.Light.Sunken,
    surfaceTint = UsColorTokens.Light.Card,
    inverseSurface = UsColorTokens.Light.SelectedPill,
    inverseOnSurface = UsColorTokens.Light.OnSelectedPill,
    inversePrimary = UsColorTokens.Dark.AccentText,
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
 * [darkTheme] defaults to the DEVICE's setting (founder, 2026-10-02: the app
 * follows the system light / dark setting on every screen, with the web's two
 * themes). It used to default to `true`: Momentum was a navy-only design and
 * the app wore it whatever the device said, so the light palette was never
 * seen. There is no in-app theme setting; when one is added, the shell passes
 * its answer here and nothing else changes.
 *
 * A surface that is dark ON PURPOSE passes `darkTheme = true` itself: the
 * live screens do, because a live room is a stage. Dynamic colour stays
 * deliberately off: the brand ramp is the identity.
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
