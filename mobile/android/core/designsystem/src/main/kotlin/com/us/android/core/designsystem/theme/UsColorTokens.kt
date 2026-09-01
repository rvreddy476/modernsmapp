package com.us.android.core.designsystem.theme

import androidx.compose.ui.graphics.Color

/**
 * Colour tokens, ported 1:1 from the Flutter reference at
 * mobile/atpost_app/lib/core/theme/app_colors.dart.
 *
 * The product is dark-only today. There is deliberately no light palette —
 * inventing one would be design work nobody has signed off (PHASE_0_1_PLAN §D.7).
 */
internal object UsColorTokens {
    // ── Backgrounds ────────────────────────────────────────────────────
    val BgPrimary = Color(0xFF000000)
    val BgSecondary = Color(0xFF121212)
    val BgTertiary = Color(0xFF1C1C1E)
    val BgCard = Color(0x0AFFFFFF)
    val BgCardHover = Color(0x0FFFFFFF)

    // ── Figma redesign tokens (atPost design file, 2026-08-29) ─────────
    // Extracted from the feed-card spec: bg/surface, bg/card, and the body
    // text step that sits between TextPrimary and TextMuted. These are the
    // SOLID card surfaces of the new design language, distinct from the
    // translucent BgCard above which the older ported screens still use.
    val FeedCanvas = Color(0xFF0D0D0D)
    val FeedCard = Color(0xFF1A1A1A)
    val TextBody = Color(0xFFCCCCCC)

    // ── Borders ────────────────────────────────────────────────────────
    val BorderSubtle = Color(0x0FFFFFFF)
    val BorderMedium = Color(0x14FFFFFF)

    // ── Text ramp (7 steps) ────────────────────────────────────────────
    val TextPrimary = Color(0xFFFFFFFF)
    val TextSecondary = Color(0xFFE5E5EA)
    val TextTertiary = Color(0xFFD1D1D6)
    val TextMuted = Color(0xFF8E8E93)
    val TextDim = Color(0xFF636366)
    val TextDimmest = Color(0xFF48484A)
    val TextGhost = Color(0xFF2C2C2E)

    // ── Brand ──────────────────────────────────────────────────────────
    val PostbookPrimary = Color(0xFFFF6B35)
    val PostbookSecondary = Color(0xFFFF8F65)
    val PostgramPrimary = Color(0xFFFF3366)
    val PostgramSecondary = Color(0xFFC850C0)
    val PosttubePrimary = Color(0xFF4ECDC4)
    val PosttubeSecondary = Color(0xFF44B8B0)
    val AccentPurple = Color(0xFF7B68EE)

    // ── Presence ───────────────────────────────────────────────────────
    val OnlineGreen = Color(0xFF4ECDC4)
    val LiveRed = Color(0xFFFF3366)

    // ── Status ─────────────────────────────────────────────────────────
    val StatusError = Color(0xFFFF4757)
    val StatusWarning = Color(0xFFFFAB00)
    val StatusSuccess = Color(0xFF2ED573)

    // ── Glass ──────────────────────────────────────────────────────────
    val GlassBg = Color(0x1AFFFFFF)
    val GlassBorder = Color(0x14FFFFFF)
}
