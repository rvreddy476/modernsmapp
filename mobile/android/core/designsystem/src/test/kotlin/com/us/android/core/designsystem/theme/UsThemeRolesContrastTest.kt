package com.us.android.core.designsystem.theme

import androidx.compose.ui.graphics.Color
import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Test
import kotlin.math.pow

/**
 * The roles added on 2026-10-02 so that every screen reads on a LIGHT device
 * as well as a dark one, each measured in BOTH themes.
 *
 * `UsWebThemeParityTest` pins the web's values and the pairs the web has.
 * These are the pairs the app needed on top: a selected pill, a label on a
 * status fill, chat's green where it is read, the sender names in a group,
 * a sheet's surface, and what is drawn over a photo or a video. Text must
 * clear 4.5:1 and a glyph or a control's edge 3:1 (WCAG 2).
 */
class UsThemeRolesContrastTest {

    private val themes = listOf("light" to LightExtendedColors, "dark" to DarkExtendedColors)

    /** The white pill with `brandNavy` text was white on white on the light theme. */
    @Test
    fun `a selected pill carries its label and stands off the ground in both themes`() {
        for ((name, theme) in themes) {
            assertText("$name label on a selected pill", theme.onSelectedPill, theme.selectedPill)
            assertGlyph("$name selected pill on a card", theme.selectedPill, theme.bgCardSolid)
            assertGlyph("$name selected pill on the page", theme.selectedPill, theme.bgCanvas)
        }
        // The web's inverse highlight: the text colour under the card colour.
        assertThat(LightExtendedColors.selectedPill).isEqualTo(LightExtendedColors.textPrimary)
        assertThat(DarkExtendedColors.selectedPill).isEqualTo(DarkExtendedColors.textPrimary)
        assertThat(LightExtendedColors.onSelectedPill).isEqualTo(LightExtendedColors.bgCardSolid)
        assertThat(DarkExtendedColors.onSelectedPill).isEqualTo(DarkExtendedColors.bgCardSolid)
    }

    @Test
    fun `a label on a status fill clears 4_5 to 1 in both themes`() {
        for ((name, theme) in themes) {
            assertText("$name on success", theme.onStatus, theme.statusSuccess)
            assertText("$name on danger", theme.onStatus, theme.statusDanger)
            assertText("$name on warning", theme.onStatus, theme.statusWarning)
            assertText("$name on info", theme.onStatus, theme.statusInfo)
        }
    }

    /** Chat's green is one colour in both themes; as TEXT on white it is 2.2:1, so text takes its own token. */
    @Test
    fun `chat's green is readable where it is text, and carries its glyph where it is a fill`() {
        for ((name, theme) in themes) {
            assertText("$name chat accent text on a card", theme.chatAccentText, theme.bgCardSolid)
            assertText("$name chat accent text on the page", theme.chatAccentText, theme.bgCanvas)
            assertText("$name glyph on the chat accent", theme.onChatAccent, theme.chatAccent)
            assertText("$name ink on your own bubble", theme.onChatBubbleOwn, theme.chatBubbleOwn)
        }
        assertThat(contrast(Color.White, UsColorTokens.ChatAccent)).isLessThan(MIN_GLYPH)
    }

    @Test
    fun `every sender name colour is readable on the incoming bubble of its theme`() {
        for ((name, theme) in themes) {
            assertThat(theme.chatSenders).hasSize(SENDER_COLOURS)
            assertThat(theme.chatSenders.toSet()).hasSize(SENDER_COLOURS)
            theme.chatSenders.forEachIndexed { index, colour ->
                assertText("$name sender #$index on the incoming bubble", colour, theme.bgCardSolid)
            }
        }
    }

    /**
     * On dark the page and the card are both black, so a sheet in the card
     * colour is black on black. A sheet takes the web's raised surface there;
     * on light it is the card, which the dimmed page already separates.
     */
    @Test
    fun `a sheet is separated from the page on dark and carries text in both themes`() {
        assertThat(DarkExtendedColors.bgSheet).isNotEqualTo(DarkExtendedColors.bgCanvas)
        assertThat(DarkExtendedColors.bgSheet).isEqualTo(DarkExtendedColors.bgRaised)
        assertThat(LightExtendedColors.bgSheet).isEqualTo(LightExtendedColors.bgCardSolid)
        for ((name, theme) in themes) {
            assertText("$name text on a sheet", theme.textPrimary, theme.bgSheet)
            assertText("$name muted text on a sheet", theme.textMuted, theme.bgSheet)
            assertText("$name accent text on a sheet", theme.accentSolid, theme.bgSheet)
            assertText("$name danger on a sheet", theme.statusDanger, theme.bgSheet)
        }
    }

    /** A Material sheet, menu or dialog that names no colour gets the same surface, not Material's lavender. */
    @Test
    fun `Material's container roles are the sheet surface`() {
        val schemes = listOf(UsLightColorScheme to LightExtendedColors, UsDarkColorScheme to DarkExtendedColors)
        for ((scheme, theme) in schemes) {
            assertThat(scheme.surfaceContainerLowest).isEqualTo(theme.bgSheet)
            assertThat(scheme.surfaceContainerLow).isEqualTo(theme.bgSheet)
            assertThat(scheme.surfaceContainer).isEqualTo(theme.bgSheet)
            assertThat(scheme.surfaceContainerHigh).isEqualTo(theme.bgSheet)
            assertThat(scheme.surfaceContainerHighest).isEqualTo(theme.bgRaised)
            assertThat(scheme.inverseSurface).isEqualTo(theme.selectedPill)
            assertThat(scheme.inverseOnSurface).isEqualTo(theme.onSelectedPill)
        }
    }

    /**
     * What is under these is a photo or a video, so the worst case is a
     * WHITE frame: the plate has to hold the label on its own.
     */
    @Test
    fun `white on the media plate reads over the brightest frame, and the dimmed white reads on the stage`() {
        val light = LightExtendedColors
        val plateOverWhite = light.mediaPlate.over(Color.White)
        assertText("on-media label on a plate over a white frame", light.onMedia, plateOverWhite)

        assertText("dimmed label on the stage", light.onMediaDim.over(light.stage), light.stage)
        assertText("muted label on the stage", light.onMediaMuted.over(light.stage), light.stage)
    }

    @Test
    fun `what is drawn over media and what is burned into a video do not invert`() {
        val light = LightExtendedColors
        val dark = DarkExtendedColors
        assertThat(light.mediaPlate).isEqualTo(dark.mediaPlate)
        assertThat(light.mediaRim).isEqualTo(dark.mediaRim)
        assertThat(light.mediaTrack).isEqualTo(dark.mediaTrack)
        assertThat(light.onMediaDim).isEqualTo(dark.onMediaDim)
        assertThat(light.onTile).isEqualTo(dark.onTile)
        assertThat(light.pillNavy).isEqualTo(dark.pillNavy)
        assertThat(light.pillWhite).isEqualTo(dark.pillWhite)
        assertThat(light.chatBubbleOwn).isEqualTo(dark.chatBubbleOwn)
        assertThat(light.onChatAccent).isEqualTo(dark.onChatAccent)
    }

    /**
     * The reel studio's text pill is the creator's content. The preview reads
     * the Compose tokens and the exporter paints the ARGB ints; they are the
     * same two colours, and the ink reads on its pill either way round.
     */
    @Test
    fun `the text pill's preview colours are the exporter's, and read on each other`() {
        assertThat(LightExtendedColors.pillNavy.argb()).isEqualTo(UsContentColors.PILL_NAVY_ARGB)
        assertThat(LightExtendedColors.pillWhite.argb()).isEqualTo(UsContentColors.PILL_WHITE_ARGB)
        assertText("navy on the white pill", LightExtendedColors.pillNavy, LightExtendedColors.pillWhite)
    }

    /** A tile's gradient runs light to deep; the glyph has to hold at least on the deep end, which it sits over. */
    @Test
    fun `the white glyph clears 3 to 1 on the deep end of every tile`() {
        val create = LightExtendedColors.create
        val launcher = LightExtendedColors.launcher
        val deeps = listOf(
            create.text, create.photo, create.reel, create.audio, create.poll, create.article, create.live,
            launcher.chat, launcher.friends, launcher.alerts, launcher.live, launcher.shop, launcher.match,
            launcher.ask, launcher.feast, launcher.tube,
        ).map { it.glow }
        deeps.forEachIndexed { index, deep ->
            assertGlyph("glyph on tile #$index", LightExtendedColors.onTile, deep)
        }
    }

    @Test
    fun `white initials clear 4_5 to 1 on every avatar colour`() {
        assertThat(UsColorTokens.AvatarPalette.toSet()).hasSize(UsColorTokens.AvatarPalette.size)
        UsColorTokens.AvatarPalette.forEachIndexed { index, colour ->
            assertText("initials on avatar #$index", UsColorTokens.OnAvatar, colour)
        }
    }

    private fun assertText(what: String, foreground: Color, background: Color) =
        assertAtLeast(what, foreground, background, MIN_TEXT)

    private fun assertGlyph(what: String, foreground: Color, background: Color) =
        assertAtLeast(what, foreground, background, MIN_GLYPH)

    private fun assertAtLeast(what: String, foreground: Color, background: Color, minimum: Double) {
        assertWithMessage("$what must be opaque to be measured").that(foreground.alpha).isEqualTo(1f)
        assertWithMessage("$what must sit on an opaque ground").that(background.alpha).isEqualTo(1f)
        assertWithMessage(what).that(contrast(foreground, background)).isAtLeast(minimum)
    }

    /** This translucent colour laid over an opaque [ground]. */
    private fun Color.over(ground: Color): Color = Color(
        red = red * alpha + ground.red * (1f - alpha),
        green = green * alpha + ground.green * (1f - alpha),
        blue = blue * alpha + ground.blue * (1f - alpha),
        alpha = 1f,
    )

    /** WCAG 2 contrast ratio of two opaque colours. */
    private fun contrast(a: Color, b: Color): Double {
        val la = luminance(a)
        val lb = luminance(b)
        return (maxOf(la, lb) + FLARE) / (minOf(la, lb) + FLARE)
    }

    private fun luminance(color: Color): Double =
        RED_WEIGHT * channel(color.red) + GREEN_WEIGHT * channel(color.green) + BLUE_WEIGHT * channel(color.blue)

    private fun channel(value: Float): Double {
        val v = value.toDouble()
        return if (v <= LINEAR_LIMIT) v / LINEAR_DIVISOR else ((v + GAMMA_OFFSET) / GAMMA_DIVISOR).pow(GAMMA)
    }

    private fun Color.argb(): Int = value.shr(ARGB_SHIFT).toInt()

    private companion object {
        const val MIN_TEXT = 4.5
        const val MIN_GLYPH = 3.0
        const val SENDER_COLOURS = 6
        const val FLARE = 0.05
        const val RED_WEIGHT = 0.2126
        const val GREEN_WEIGHT = 0.7152
        const val BLUE_WEIGHT = 0.0722
        const val LINEAR_LIMIT = 0.04045
        const val LINEAR_DIVISOR = 12.92
        const val GAMMA_OFFSET = 0.055
        const val GAMMA_DIVISOR = 1.055
        const val GAMMA = 2.4
        const val ARGB_SHIFT = 32
    }
}
