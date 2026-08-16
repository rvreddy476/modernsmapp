package com.us.android.core.designsystem.theme

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Guards the token port from the Flutter reference.
 *
 * These are not tautologies: they pin the exact ARGB values from
 * mobile/atpost_app/lib/core/theme/app_colors.dart. If someone "tidies" a
 * colour, this fails and the divergence from the reference app is caught
 * before it ships rather than in review-by-eyeball.
 */
class UsTokensTest {

    @Test
    fun `background ramp matches the Flutter reference`() {
        assertThat(UsColorTokens.BgPrimary.value.toString(16)).startsWith("ff000000")
        assertThat(UsColorTokens.BgSecondary.value.toString(16)).startsWith("ff121212")
        assertThat(UsColorTokens.BgTertiary.value.toString(16)).startsWith("ff1c1c1e")
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
    fun `radii match the Flutter scale`() {
        val radii = UsRadii()
        assertThat(radii.small.value).isEqualTo(8f)
        assertThat(radii.medium.value).isEqualTo(12f)
        assertThat(radii.large.value).isEqualTo(16f)
        assertThat(radii.extraLarge.value).isEqualTo(20f)
    }
}
