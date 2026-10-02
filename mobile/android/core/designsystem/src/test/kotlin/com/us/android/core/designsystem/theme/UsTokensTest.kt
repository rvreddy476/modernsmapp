package com.us.android.core.designsystem.theme

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Guards the tokens that are NOT part of either theme, and the spacing and
 * radii scales.
 *
 * The themed colours (ground, text, accent, status) are pinned to the web's
 * values by `UsWebThemeParityTest`. Until 2026-10-02 this file pinned
 * Momentum's navy ramp and ember accent (Figma YsWb936…, 2026-09-03); those
 * tests went with the palette, deliberately, when the app took the web's
 * two themes.
 */
class UsTokensTest {

    /**
     * The Create sheet's per-type gradients (founder render, 2026-09-04):
     * light → deep pairs, pinned so a "tidy" cannot drift them from the design.
     * Ember is Text's and Go Live's own colour; it is no longer the app's accent.
     */
    @Test
    fun `create tile colours are the founder's gradient pairs`() {
        assertThat(UsColorTokens.CreateEmberLight.hex()).isEqualTo("fffb923c")
        assertThat(UsColorTokens.CreateEmberDeep.hex()).isEqualTo("ffdc2626")
        assertThat(UsColorTokens.CreatePhotoLight.hex()).isEqualTo("ff34d399")
        assertThat(UsColorTokens.CreatePhotoDeep.hex()).isEqualTo("ff059669")
        assertThat(UsColorTokens.CreateReelLight.hex()).isEqualTo("fffb7185")
        assertThat(UsColorTokens.CreateReelDeep.hex()).isEqualTo("ffe11d48")
        assertThat(UsColorTokens.CreateAudioLight.hex()).isEqualTo("ffc084fc")
        assertThat(UsColorTokens.CreateAudioDeep.hex()).isEqualTo("ff7c3aed")
        assertThat(UsColorTokens.CreatePollLight.hex()).isEqualTo("fffcd34d")
        assertThat(UsColorTokens.CreatePollDeep.hex()).isEqualTo("ffd97706")
        assertThat(UsColorTokens.CreateArticleLight.hex()).isEqualTo("ff22d3ee")
        assertThat(UsColorTokens.CreateArticleDeep.hex()).isEqualTo("ff0891b2")
    }

    /** Text and Go Live share the ember tile; the set is the same in both themes. */
    @Test
    fun `text and live create tiles are ember and the set does not invert`() {
        val create = DarkExtendedColors.create
        assertThat(create.text).isEqualTo(create.live)
        assertThat(LightExtendedColors.create).isEqualTo(create)
        assertThat(LightExtendedColors.launcher).isEqualTo(DarkExtendedColors.launcher)
        val distinct = setOf(create.text, create.photo, create.reel, create.audio, create.poll, create.article)
        assertThat(distinct).hasSize(6)
    }

    /** Every tile glows in the DEEP end of its own ramp, never a borrowed colour. */
    @Test
    fun `each create tile glows in its own deep colour`() {
        val create = DarkExtendedColors.create
        assertThat(create.text.glow).isEqualTo(UsColorTokens.CreateEmberDeep)
        assertThat(create.live.glow).isEqualTo(UsColorTokens.CreateEmberDeep)
        assertThat(create.photo.glow).isEqualTo(UsColorTokens.CreatePhotoDeep)
        assertThat(create.reel.glow).isEqualTo(UsColorTokens.CreateReelDeep)
        assertThat(create.audio.glow).isEqualTo(UsColorTokens.CreateAudioDeep)
        assertThat(create.poll.glow).isEqualTo(UsColorTokens.CreatePollDeep)
        assertThat(create.article.glow).isEqualTo(UsColorTokens.CreateArticleDeep)
    }

    /**
     * Danger is a token, not an M3 slot: a feature asking for "the
     * destructive colour" gets the web's danger for the theme it is in, and
     * never the accent.
     */
    @Test
    fun `danger is its own colour in each theme and never the accent`() {
        for (theme in listOf(DarkExtendedColors, LightExtendedColors)) {
            assertThat(theme.statusDanger).isNotEqualTo(theme.accent)
            assertThat(theme.statusDanger).isNotEqualTo(theme.accentSolid)
            assertThat(theme.statusDanger).isNotEqualTo(theme.accentStrong)
            // A lit like and a destructive row are the same red as a failure line.
            assertThat(theme.liveRed).isEqualTo(theme.statusDanger)
        }
    }

    /** Chat keeps its green on purpose — the brief carves it out of the accent. */
    @Test
    fun `chat accent stays green in both themes`() {
        assertThat(UsColorTokens.ChatAccent.hex()).isEqualTo("ff22c55e")
        assertThat(LightExtendedColors.chatAccent).isEqualTo(DarkExtendedColors.chatAccent)
    }

    @Test
    fun `the text ramp has seven distinct steps in each theme, darkest to faintest`() {
        for (theme in listOf(DarkExtendedColors, LightExtendedColors)) {
            val ramp = listOf(
                theme.textPrimary,
                theme.textSecondary,
                theme.textTertiary,
                theme.textMuted,
                theme.textDim,
                theme.textDimmest,
                theme.textGhost,
            )
            assertThat(ramp.toSet()).hasSize(7)
        }
    }

    /** The primary button's fill is flat: the fill token, as a brush. */
    @Test
    fun `the primary button's brush is the flat fill of its theme`() {
        assertThat(LightExtendedColors.ctaGradient)
            .isEqualTo(androidx.compose.ui.graphics.SolidColor(LightExtendedColors.accentStrong))
        assertThat(DarkExtendedColors.ctaGradient)
            .isEqualTo(androidx.compose.ui.graphics.SolidColor(DarkExtendedColors.accentStrong))
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
