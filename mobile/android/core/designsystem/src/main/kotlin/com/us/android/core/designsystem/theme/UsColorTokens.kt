package com.us.android.core.designsystem.theme

import androidx.compose.ui.graphics.Color

/**
 * Colour tokens. The dark ramp is ported 1:1 from the Flutter reference at
 * mobile/atpost_app/lib/core/theme/app_colors.dart; the light ramp arrived
 * with the Figma light frames (81:*, 2026-09-01) — canvas #FAFAFA, card
 * #F0F0F0, ink #0D0D0D, muted #666.
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

    // ── Brand chip (the "at" logo square) ──────────────────────────────
    val BrandChip = Color(0xFFFFFFFF)
    val OnBrandChip = Color(0xFF000000)

    /**
     * The light palette, from the Figma light frames (81:*).
     *
     * Brand, presence and status colours are shared with dark — the ramp
     * inverts, the identity does not. LightTextBody deviates from the
     * frame's literal #CCC on purpose: that value is the dark theme's body
     * step left unswapped in the design file, and it is illegible on
     * #F0F0F0.
     */
    object Light {
        val BgPrimary = Color(0xFFFAFAFA)
        val BgSecondary = Color(0xFFFFFFFF)
        val BgTertiary = Color(0xFFF0F0F0)
        val BgCard = Color(0x0A000000)
        val BgCardHover = Color(0x0F000000)

        val FeedCanvas = Color(0xFFFAFAFA)
        val FeedCard = Color(0xFFF0F0F0)
        val TextBody = Color(0xFF444444)

        val BorderSubtle = Color(0x0F000000)
        val BorderMedium = Color(0x14000000)

        val TextPrimary = Color(0xFF0D0D0D)
        val TextSecondary = Color(0xFF3A3A3C)
        val TextTertiary = Color(0xFF48484A)
        val TextMuted = Color(0xFF666666)
        val TextDim = Color(0xFF8E8E93)
        val TextDimmest = Color(0xFFAEAEB2)
        val TextGhost = Color(0xFFD1D1D6)

        val GlassBg = Color(0x1A000000)
        val GlassBorder = Color(0x14000000)

        /** Figma light brand chip: cream, per the 81:2 logo-icon. */
        val BrandChip = Color(0xFFF5EEE4)
        val OnBrandChip = Color(0xFF0D0D0D)
    }
}
