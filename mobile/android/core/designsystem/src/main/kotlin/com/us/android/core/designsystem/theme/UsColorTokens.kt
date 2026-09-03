package com.us.android.core.designsystem.theme

import androidx.compose.ui.graphics.Color

/**
 * Colour tokens for Momentum (Figma YsWb936muw8pwIxgb0je2A, 2026-09-03).
 *
 * The dark ramp is the design's native theme, lifted directly from the Figma
 * frames: navy ground, a two-step surface stack, and the orange→red accent
 * gradient. The light ramp has no Figma frame to port — it is this port's own
 * derivation, keeping the same structure (ground / surface / raised /
 * highlight / border / text) at light values, with the identical accent.
 */
internal object UsColorTokens {
    // ── Backgrounds ────────────────────────────────────────────────────
    // Momentum: ground #041122, card/surface #0B1B2E, raised surface #071D33.
    val BgPrimary = Color(0xFF041122)
    val BgSecondary = Color(0xFF0B1B2E)
    val BgTertiary = Color(0xFF071D33)
    val BgCard = Color(0x0AFFFFFF)
    val BgCardHover = Color(0x0FFFFFFF)

    // ── Figma redesign tokens (Momentum feed-card spec) ─────────────────
    // bg/surface (canvas) and bg/card (the solid card surface), plus the
    // body text step that sits between TextPrimary and TextMuted.
    val FeedCanvas = Color(0xFF041122)
    val FeedCard = Color(0xFF0B1B2E)
    val TextBody = Color(0xFFC7D4E4)

    // ── Unread / highlight row ───────────────────────────────────────────
    // The notification-row and message-row highlight surface a row sits on
    // while unread.
    val UnreadRow = Color(0xFF072440)

    // ── Borders ────────────────────────────────────────────────────────
    val BorderSubtle = Color(0x800E2D4A)
    val BorderMedium = Color(0xFF0E2D4A)

    // ── Text ramp (7 steps) ────────────────────────────────────────────
    val TextPrimary = Color(0xFFFFFFFF)
    val TextSecondary = Color(0xFFC7D4E4)
    val TextTertiary = Color(0xFFA9BBD1)
    val TextMuted = Color(0xFF8AA3C2)
    val TextDim = Color(0xFF6C84A0)
    val TextDimmest = Color(0xFF4C6280)
    val TextGhost = Color(0xFF33455E)

    // ── Accent — Momentum's orange→red gradient identity ────────────────
    val AccentOrange = Color(0xFFFB923C)
    val AccentRed = Color(0xFFDC2626)

    // ── Brand (legacy per-product gradients, kept for the story ring and
    // surfaces that have not moved to the single Momentum accent) ───────
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

    // ── Chat (Figma chat tour 98:*) ────────────────────────────────────
    // The chat vertical's own accent: outgoing bubbles, send, unread
    // badges. Shared across themes — green IS the chat identity, and
    // Momentum's brief keeps it untouched on purpose.
    val ChatAccent = Color(0xFF22C55E)
    val ChatOnline = Color(0xFF4ADE80)

    // ── Brand chip (legacy "at" logo square, superseded by the Momentum
    // wordmark but kept so nothing still reading it goes unthemed) ──────
    val BrandChip = Color(0xFFFFFFFF)
    val OnBrandChip = Color(0xFF041122)

    /**
     * The light palette — this port's own derivation, not a Figma frame.
     * Same structure as dark (ground / surface / raised / highlight /
     * border / text), and the identical accent gradient, so a screen never
     * has to special-case which theme it is in beyond reading the token.
     */
    object Light {
        val BgPrimary = Color(0xFFF7F9FC)
        val BgSecondary = Color(0xFFFFFFFF)
        val BgTertiary = Color(0xFFEEF3F9)
        val BgCard = Color(0x0A000000)
        val BgCardHover = Color(0x0F000000)

        val FeedCanvas = Color(0xFFF7F9FC)
        val FeedCard = Color(0xFFFFFFFF)
        val TextBody = Color(0xFF35506F)

        val UnreadRow = Color(0xFFFFF4EC)

        val BorderSubtle = Color(0x80DCE4EF)
        val BorderMedium = Color(0xFFDCE4EF)

        val TextPrimary = Color(0xFF041122)
        val TextSecondary = Color(0xFF1C3350)
        val TextTertiary = Color(0xFF35506F)
        val TextMuted = Color(0xFF5B6E88)
        val TextDim = Color(0xFF7C8CA3)
        val TextDimmest = Color(0xFFA6B2C2)
        val TextGhost = Color(0xFFCBD4E0)

        val GlassBg = Color(0x1A000000)
        val GlassBorder = Color(0x14000000)

        /** Legacy "at" logo chip, light variant — cream on ink. */
        val BrandChip = Color(0xFFF5EEE4)
        val OnBrandChip = Color(0xFF041122)
    }
}
