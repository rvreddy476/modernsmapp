package com.us.android.core.designsystem.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.ExperimentalTextApi
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.Font
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontVariation
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.R

/**
 * Outfit, bundled as a variable font asset.
 *
 * The Flutter reference pulls Outfit at runtime through google_fonts, which
 * costs a network round trip on first paint and a fallback flash if it
 * fails. Bundling one 110 KB variable font covers all four weights we use
 * and removes that failure mode entirely (PHASE_0_1_PLAN §D.7).
 *
 * FontVariation requires API 26 — which is exactly our minSdk.
 */
@OptIn(ExperimentalTextApi::class)
private fun outfit(weight: Int) = Font(
    resId = R.font.outfit_variable,
    weight = FontWeight(weight),
    // Selects the weight axis of the variable font. Still marked
    // experimental in Compose, but it is the only way to get all four
    // weights out of one file rather than shipping four static TTFs.
    variationSettings = FontVariation.Settings(FontVariation.weight(weight)),
)

val OutfitFontFamily = FontFamily(
    outfit(400),
    outfit(500),
    outfit(600),
    outfit(700),
    outfit(900),
)

/**
 * Bodoni Moda, bundled as a variable font asset — the Momentum wordmark's
 * typeface. Only the Black (900) instance is ever drawn: [UsWordmark] is the
 * one call site, so this stays a single weight rather than the full family
 * [OutfitFontFamily] and [FigtreeFontFamily] carry.
 */
@OptIn(ExperimentalTextApi::class)
val MomentumWordmarkFontFamily = FontFamily(
    Font(
        resId = R.font.bodoni_moda_variable,
        weight = FontWeight.Black,
        variationSettings = FontVariation.Settings(FontVariation.weight(900)),
    ),
)

/**
 * Bodoni Moda at reading weights, from the same variable font as the
 * wordmark: Echo's note text (look v3, 14 Nov 2026). Regular to Bold, so a
 * note can be set long without the wordmark's Black.
 */
@OptIn(ExperimentalTextApi::class)
private fun bodoni(weight: Int) = Font(
    resId = R.font.bodoni_moda_variable,
    weight = FontWeight(weight),
    variationSettings = FontVariation.Settings(FontVariation.weight(weight)),
)

val EchoSerifFontFamily = FontFamily(
    bodoni(400),
    bodoni(500),
    bodoni(600),
    bodoni(700),
)

/**
 * Echo's serif text styles (docs/designs/echo-look-v3.md): a note is set in
 * Bodoni Moda, larger than body text, at a line height of about 1.4. Names,
 * buttons and counts stay in the UI faces of [UsTypography].
 */
object UsEchoType {
    /** A note on a timeline card: 18 sp. */
    val note = serifStyle(18, 25)

    /** A long note (over ~280 characters) on a timeline card: 16 sp. */
    val noteLong = serifStyle(16, 22)

    /** The note on its own page: 22 sp. */
    val notePage = serifStyle(22, 31)

    /** The composer's text area: 20 sp. */
    val composer = serifStyle(20, 28)

    /** A quoted note nested inside a card, and a reply-mode parent: 15 sp. */
    val quoted = serifStyle(15, 21)

    /** The "Echo" header and wordmark inside Echo. */
    val title = TextStyle(
        fontFamily = EchoSerifFontFamily,
        fontWeight = FontWeight.Bold,
        fontSize = 26.sp,
        lineHeight = 32.sp,
    )

    private fun serifStyle(size: Int, line: Int) = TextStyle(
        fontFamily = EchoSerifFontFamily,
        fontWeight = FontWeight.Normal,
        fontSize = size.sp,
        lineHeight = line.sp,
    )
}

/**
 * Figtree, bundled as a variable font asset — Momentum's body/label
 * typeface. Outfit remains the heading face (see [UsTypography]); Figtree is
 * what a reader spends the most time actually reading, and the Momentum
 * frames draw it noticeably rounder and quieter than Outfit's geometric
 * headings.
 */
@OptIn(ExperimentalTextApi::class)
private fun figtree(weight: Int) = Font(
    resId = R.font.figtree_variable,
    weight = FontWeight(weight),
    variationSettings = FontVariation.Settings(FontVariation.weight(weight)),
)

val FigtreeFontFamily = FontFamily(
    figtree(400),
    figtree(500),
    figtree(600),
    figtree(700),
)

/**
 * Maps the type ramp onto Material 3 slots.
 *
 * All Material slots are defined: a dialog or a price must not quietly fall
 * back to Roboto. Outfit supplies calm, semibold headings; Figtree supplies
 * readable body copy. Sizes remain in sp so Android's font scaling works.
 *
 * Colours are NOT baked into these styles. Flutter's text styles carry a
 * colour; in Compose that fights LocalContentColor and makes components
 * un-themeable. Colour comes from the M3 scheme or UsTheme.extended.
 */
val UsTypography = Typography(
    displayLarge = headingStyle(40, 48),
    displayMedium = headingStyle(36, 44),
    displaySmall = headingStyle(32, 40),
    headlineLarge = headingStyle(30, 38),
    headlineMedium = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Bold,
        fontSize = 28.sp,
        lineHeight = 34.sp,
    ),
    headlineSmall = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Bold,
        fontSize = 24.sp,
        lineHeight = 32.sp,
    ),
    titleLarge = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.SemiBold,
        fontSize = 22.sp,
        lineHeight = 28.sp,
    ),
    titleMedium = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.SemiBold,
        fontSize = 17.sp,
        lineHeight = 24.sp,
    ),
    titleSmall = headingStyle(15, 20),
    bodyLarge = TextStyle(
        fontFamily = FigtreeFontFamily,
        fontWeight = FontWeight.Normal,
        fontSize = 16.sp,
        lineHeight = 24.sp,
    ),
    bodyMedium = TextStyle(
        fontFamily = FigtreeFontFamily,
        fontWeight = FontWeight.Normal,
        fontSize = 14.sp,
        lineHeight = 20.sp,
    ),
    bodySmall = TextStyle(
        fontFamily = FigtreeFontFamily,
        fontWeight = FontWeight.Normal,
        fontSize = 12.sp,
        lineHeight = 18.sp,
    ),
    labelLarge = TextStyle(
        fontFamily = FigtreeFontFamily,
        fontWeight = FontWeight.SemiBold,
        fontSize = 14.sp,
        lineHeight = 20.sp,
    ),
    labelMedium = TextStyle(
        fontFamily = FigtreeFontFamily,
        fontWeight = FontWeight.SemiBold,
        fontSize = 12.sp,
        lineHeight = 16.sp,
    ),
    labelSmall = TextStyle(
        fontFamily = FigtreeFontFamily,
        fontWeight = FontWeight.Medium,
        fontSize = 11.sp,
        lineHeight = 16.sp,
    ),
)

private fun headingStyle(size: Int, line: Int) = TextStyle(
    fontFamily = OutfitFontFamily,
    fontWeight = FontWeight.SemiBold,
    fontSize = size.sp,
    lineHeight = line.sp,
)
