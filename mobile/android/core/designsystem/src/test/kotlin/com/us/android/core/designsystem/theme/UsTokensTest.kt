package com.us.android.core.designsystem.theme

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Guards the Momentum token port (Figma YsWb936muw8pwIxgb0je2A, 2026-09-03).
 *
 * These are not tautologies: they pin the exact ARGB values the design
 * specifies. If someone "tidies" a colour, this fails and the divergence
 * from the design is caught before it ships rather than in review-by-eyeball.
 */
class UsTokensTest {

    @Test
    fun `dark surface ramp matches the Momentum frames`() {
        assertThat(UsColorTokens.BgPrimary.hex()).isEqualTo("ff041122")
        assertThat(UsColorTokens.BgSecondary.hex()).isEqualTo("ff0b1b2e")
        assertThat(UsColorTokens.BgTertiary.hex()).isEqualTo("ff071d33")
        assertThat(UsColorTokens.UnreadRow.hex()).isEqualTo("ff072440")
        assertThat(UsColorTokens.BorderMedium.hex()).isEqualTo("ff0e2d4a")
        assertThat(UsColorTokens.TextPrimary.hex()).isEqualTo("ffffffff")
        assertThat(UsColorTokens.TextMuted.hex()).isEqualTo("ff8aa3c2")
    }

    @Test
    fun `light surface ramp is the derived Momentum palette`() {
        assertThat(UsColorTokens.Light.BgPrimary.hex()).isEqualTo("fff7f9fc")
        assertThat(UsColorTokens.Light.BgSecondary.hex()).isEqualTo("ffffffff")
        assertThat(UsColorTokens.Light.BgTertiary.hex()).isEqualTo("ffeef3f9")
        assertThat(UsColorTokens.Light.UnreadRow.hex()).isEqualTo("fffff4ec")
        assertThat(UsColorTokens.Light.BorderMedium.hex()).isEqualTo("ffdce4ef")
        assertThat(UsColorTokens.Light.TextPrimary.hex()).isEqualTo("ff041122")
        assertThat(UsColorTokens.Light.TextMuted.hex()).isEqualTo("ff5b6e88")
    }

    /** The accent is one identity across both themes, never re-derived. */
    @Test
    fun `accent gradient ends are the Momentum orange and red`() {
        assertThat(UsColorTokens.AccentOrange.hex()).isEqualTo("fffb923c")
        assertThat(UsColorTokens.AccentRed.hex()).isEqualTo("ffdc2626")
        assertThat(DarkExtendedColors.accentSolid).isEqualTo(UsColorTokens.AccentOrange)
        assertThat(LightExtendedColors.accentSolid).isEqualTo(UsColorTokens.AccentOrange)
        assertThat(LightExtendedColors.accentDeep).isEqualTo(UsColorTokens.AccentRed)
        assertThat(LightExtendedColors.ctaGradient).isEqualTo(DarkExtendedColors.ctaGradient)
    }

    /** Chat keeps its green on purpose — the brief carves it out of the accent. */
    @Test
    fun `chat accent stays green in both themes`() {
        assertThat(UsColorTokens.ChatAccent.hex()).isEqualTo("ff22c55e")
        assertThat(LightExtendedColors.chatAccent).isEqualTo(DarkExtendedColors.chatAccent)
    }

    @Test
    fun `text ramp has seven distinct steps`() {
        val ramp = listOf(
            UsColorTokens.TextPrimary,
            UsColorTokens.TextSecondary,
            UsColorTokens.TextTertiary,
            UsColorTokens.TextMuted,
            UsColorTokens.TextDim,
            UsColorTokens.TextDimmest,
            UsColorTokens.TextGhost,
        )
        assertThat(ramp).hasSize(7)
        assertThat(ramp.toSet()).hasSize(7)
    }

    @Test
    fun `spacing scale preserves the irregular Flutter values`() {
        // app_spacing.dart is 4/6/8/12/14/16/18/20 — deliberately not a
        // clean 4pt grid. Normalising it would silently reflow every
        // ported screen.
        val spacing = UsSpacing()
        assertThat(spacing.xs.value).isEqualTo(4f)
        assertThat(spacing.s.value).isEqualTo(6f)
        assertThat(spacing.m.value).isEqualTo(8f)
        assertThat(spacing.l.value).isEqualTo(12f)
        assertThat(spacing.xl.value).isEqualTo(14f)
        assertThat(spacing.xxl.value).isEqualTo(16f)
        assertThat(spacing.xxxl.value).isEqualTo(18f)
        assertThat(spacing.xxxxl.value).isEqualTo(20f)
    }

    @Test
    fun `page gutter is 18dp not the Material default 16dp`() {
        assertThat(UsSpacing().pageHorizontal.value).isEqualTo(18f)
    }

    @Test
    fun `radii match the Momentum scale`() {
        val radii = UsRadii()
        assertThat(radii.card.value).isEqualTo(24f)
        assertThat(radii.media.value).isEqualTo(16f)
        assertThat(radii.panel.value).isEqualTo(14f)
        assertThat(radii.pill.value).isEqualTo(6f)
        // The legacy Flutter steps stay for the screens still on them.
        assertThat(radii.small.value).isEqualTo(8f)
        assertThat(radii.medium.value).isEqualTo(12f)
        assertThat(radii.large.value).isEqualTo(16f)
        assertThat(radii.extraLarge.value).isEqualTo(20f)
    }

    private fun androidx.compose.ui.graphics.Color.hex(): String =
        "%08x".format(value.shr(32).toLong() and 0xFFFFFFFFL)
}
