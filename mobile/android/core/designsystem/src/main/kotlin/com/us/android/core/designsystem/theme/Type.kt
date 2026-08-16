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
 * Maps the Flutter type ramp onto Material 3 slots.
 *
 * Flutter name -> M3 slot (from app_text_styles.dart):
 *   h1   28/w900 -> headlineMedium      logo 26/w900 -> headlineSmall
 *   h2   17/w700 -> titleLarge          h3   15/w700 -> titleMedium
 *   body 14.5/w400 (1.55 line) -> bodyLarge
 *   bodyMedium 14/w500 -> bodyMedium    bodySmall 13/w500 -> bodySmall
 *   label 13/w600 -> labelLarge         labelSmall 11/w600 -> labelMedium
 *   labelTiny 10/w700 -> labelSmall
 *
 * Colours are NOT baked into these styles. Flutter's text styles carry a
 * colour; in Compose that fights LocalContentColor and makes components
 * un-themeable. Colour comes from the M3 scheme or UsTheme.extended.
 */
val UsTypography = Typography(
    headlineMedium = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Black,
        fontSize = 28.sp,
        lineHeight = 34.sp,
    ),
    headlineSmall = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Black,
        fontSize = 26.sp,
        lineHeight = 32.sp,
    ),
    titleLarge = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Bold,
        fontSize = 17.sp,
        lineHeight = 22.sp,
    ),
    titleMedium = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Bold,
        fontSize = 15.sp,
        lineHeight = 20.sp,
    ),
    bodyLarge = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Normal,
        fontSize = 14.5.sp,
        // Flutter height: 1.55 is a multiplier -> 14.5 * 1.55 ~= 22.5sp
        lineHeight = 22.5.sp,
    ),
    bodyMedium = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Medium,
        fontSize = 14.sp,
        lineHeight = 20.sp,
    ),
    bodySmall = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Medium,
        fontSize = 13.sp,
        lineHeight = 18.sp,
    ),
    labelLarge = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.SemiBold,
        fontSize = 13.sp,
        lineHeight = 17.sp,
    ),
    labelMedium = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.SemiBold,
        fontSize = 11.sp,
        lineHeight = 15.sp,
    ),
    labelSmall = TextStyle(
        fontFamily = OutfitFontFamily,
        fontWeight = FontWeight.Bold,
        fontSize = 10.sp,
        lineHeight = 14.sp,
    ),
)
