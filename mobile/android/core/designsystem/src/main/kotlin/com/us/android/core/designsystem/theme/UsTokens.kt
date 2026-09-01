package com.us.android.core.designsystem.theme

import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/**
 * Tokens Material 3 has no slot for.
 *
 * M3's ColorScheme covers primary/surface/error and so on, but it has no
 * concept of a 7-step text ramp, per-product brand gradients, or a glass
 * layer. Rather than abuse unrelated M3 slots to smuggle these through,
 * they live here and are read via [UsTheme.extended].
 */
@Immutable
data class UsExtendedColors(
    val textPrimary: Color,
    val textSecondary: Color,
    val textTertiary: Color,
    val textMuted: Color,
    val textDim: Color,
    val textDimmest: Color,
    val textGhost: Color,
    val bgCard: Color,
    val bgCardHover: Color,
    /**
     * Figma redesign (2026-08-29): the SOLID card surface (`bg/card`,
     * #1A1A1A) and its canvas (`bg/surface`, #0D0D0D), plus the body-text
     * step (#CCC) the feed card uses between textPrimary and textMuted.
     */
    val bgCardSolid: Color,
    val bgCanvas: Color,
    val textBody: Color,
    val borderSubtle: Color,
    val borderMedium: Color,
    val glassBg: Color,
    val glassBorder: Color,
    val onlineGreen: Color,
    val liveRed: Color,
    val statusWarning: Color,
    val statusSuccess: Color,
    val postbookGradient: Brush,
    val postgramGradient: Brush,
    val posttubeGradient: Brush,
    val storyRingGradient: Brush,
    val ctaGradient: Brush,
)

/** Corner radii, ported from app_spacing.dart. */
@Immutable
data class UsRadii(
    val small: Dp = 8.dp,
    val medium: Dp = 12.dp,
    val large: Dp = 16.dp,
    val extraLarge: Dp = 20.dp,
    val full: Dp = 9999.dp,
)

/**
 * Spacing scale, ported from app_spacing.dart.
 *
 * The Flutter scale is irregular (4/6/8/12/14/16/18/20) rather than a clean
 * 4pt grid. Preserved exactly so ported screens match the reference
 * pixel-for-pixel; normalising it is a design decision, not a port decision.
 */
@Immutable
data class UsSpacing(
    val xs: Dp = 4.dp,
    val s: Dp = 6.dp,
    val m: Dp = 8.dp,
    val l: Dp = 12.dp,
    val xl: Dp = 14.dp,
    val xxl: Dp = 16.dp,
    val xxxl: Dp = 18.dp,
    val xxxxl: Dp = 20.dp,
    /** Horizontal page gutter. app_spacing.dart uses 18dp, not 16dp. */
    val pageHorizontal: Dp = 18.dp,
)

internal val LocalUsExtendedColors = staticCompositionLocalOf<UsExtendedColors> {
    error("UsExtendedColors not provided — wrap the tree in UsTheme { }")
}

internal val LocalUsRadii = staticCompositionLocalOf { UsRadii() }

internal val LocalUsSpacing = staticCompositionLocalOf { UsSpacing() }

internal val DarkExtendedColors = UsExtendedColors(
    textPrimary = UsColorTokens.TextPrimary,
    textSecondary = UsColorTokens.TextSecondary,
    textTertiary = UsColorTokens.TextTertiary,
    textMuted = UsColorTokens.TextMuted,
    textDim = UsColorTokens.TextDim,
    textDimmest = UsColorTokens.TextDimmest,
    textGhost = UsColorTokens.TextGhost,
    bgCard = UsColorTokens.BgCard,
    bgCardHover = UsColorTokens.BgCardHover,
    bgCardSolid = UsColorTokens.FeedCard,
    bgCanvas = UsColorTokens.FeedCanvas,
    textBody = UsColorTokens.TextBody,
    borderSubtle = UsColorTokens.BorderSubtle,
    borderMedium = UsColorTokens.BorderMedium,
    glassBg = UsColorTokens.GlassBg,
    glassBorder = UsColorTokens.GlassBorder,
    onlineGreen = UsColorTokens.OnlineGreen,
    liveRed = UsColorTokens.LiveRed,
    statusWarning = UsColorTokens.StatusWarning,
    statusSuccess = UsColorTokens.StatusSuccess,
    postbookGradient = Brush.horizontalGradient(
        listOf(UsColorTokens.PostbookPrimary, UsColorTokens.PostbookSecondary),
    ),
    postgramGradient = Brush.horizontalGradient(
        listOf(UsColorTokens.PostgramPrimary, UsColorTokens.PostbookPrimary),
    ),
    posttubeGradient = Brush.horizontalGradient(
        listOf(UsColorTokens.PosttubePrimary, UsColorTokens.AccentPurple),
    ),
    storyRingGradient = Brush.sweepGradient(
        listOf(
            UsColorTokens.PostbookPrimary,
            UsColorTokens.PostgramPrimary,
            UsColorTokens.AccentPurple,
            UsColorTokens.PostbookPrimary,
        ),
    ),
    ctaGradient = Brush.horizontalGradient(
        listOf(UsColorTokens.PostbookPrimary, UsColorTokens.PostgramPrimary),
    ),
)
