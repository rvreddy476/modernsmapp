package com.us.android.core.designsystem.theme

import androidx.compose.ui.graphics.Color
import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File
import kotlin.math.pow

/**
 * The app's two themes are the web's two themes (founder, 2026-10-02).
 *
 * Three guards, because drift can come from either side:
 *
 *  1. every themed token is pinned to the web's value, light and dark, by
 *     the web token's own name, so "tidying" a colour here fails;
 *  2. when the web checkout is beside this one, the same table is checked
 *     against `postbook-ui/src/ui/theme.css` itself, so a change made on the
 *     web and not here fails too;
 *  3. the pairs people read are measured: text and muted text on the ground
 *     and on a card, the accent as text, and a white label on a filled
 *     button, each at 4.5:1 or better in BOTH themes.
 */
class UsWebThemeParityTest {

    /** One web token and what carries it in the app, per theme. */
    private data class Token(
        val web: String,
        val light: Color,
        val dark: Color,
        val lightRgb: String,
        val darkRgb: String,
    )

    private val light = LightExtendedColors
    private val dark = DarkExtendedColors

    /** The web's `--theme-*` tokens that have a counterpart here, with the web's values as written there. */
    private val pairs = listOf(
        Token("canvas", light.bgCanvas, dark.bgCanvas, "243 246 250", "0 0 0"),
        Token("brand-card", light.bgCardSolid, dark.bgCardSolid, "255 255 255", "0 0 0"),
        Token("brand-bg", light.bgCardSolid, dark.bgCardSolid, "255 255 255", "0 0 0"),
        Token("brand-secondary", light.bgRaised, dark.bgRaised, "247 249 249", "22 24 28"),
        Token("brand-text", light.textPrimary, dark.textPrimary, "15 20 25", "247 249 249"),
        Token("text-muted", light.textMuted, dark.textMuted, "83 100 113", "139 152 165"),
        Token("brand-accent", light.accent, dark.accent, "59 130 196", "90 155 216"),
        Token("brand-ink", light.accentSolid, dark.accentSolid, "47 107 163", "90 155 216"),
        Token("brand-ink-hover", light.accentDeep, dark.accentDeep, "40 90 138", "106 168 224"),
        Token("brand-tint", light.unreadRow, dark.unreadRow, "234 242 250", "18 36 58"),
        Token("brand-outline", light.accentOutline, dark.accentOutline, "185 212 236", "44 74 104"),
        Token("success", light.statusSuccess, dark.statusSuccess, "27 127 75", "74 222 128"),
        Token("warning", light.statusWarning, dark.statusWarning, "138 83 0", "251 191 36"),
        Token("danger", light.statusDanger, dark.statusDanger, "198 40 40", "248 113 113"),
        Token("info", light.statusInfo, dark.statusInfo, "47 107 163", "106 168 224"),
    )

    @Test
    fun `every themed token is the web's value in both themes`() {
        for (pair in pairs) {
            assertWithMessage("light --theme-${pair.web}").that(pair.light.rgb()).isEqualTo(pair.lightRgb)
            assertWithMessage("dark --theme-${pair.web}").that(pair.dark.rgb()).isEqualTo(pair.darkRgb)
        }
    }

    /** The web's one border is its text colour at 10% (light) and 15% (dark). */
    @Test
    fun `the border is the web's divider, the text colour at the web's alpha`() {
        assertThat(light.borderMedium.rgb()).isEqualTo("15 20 25")
        assertThat(light.borderMedium.alpha).isWithin(ALPHA_STEP).of(0.10f)
        assertThat(dark.borderMedium.rgb()).isEqualTo("247 249 249")
        assertThat(dark.borderMedium.alpha).isWithin(ALPHA_STEP).of(0.15f)
    }

    @Test
    fun `Material's roles are filled from the same web values`() {
        assertThat(UsLightColorScheme.background.rgb()).isEqualTo("243 246 250")
        assertThat(UsLightColorScheme.surface.rgb()).isEqualTo("255 255 255")
        assertThat(UsLightColorScheme.onSurface.rgb()).isEqualTo("15 20 25")
        assertThat(UsLightColorScheme.primary.rgb()).isEqualTo("47 107 163")
        assertThat(UsLightColorScheme.onPrimary.rgb()).isEqualTo("255 255 255")
        assertThat(UsLightColorScheme.error.rgb()).isEqualTo("198 40 40")

        assertThat(UsDarkColorScheme.background.rgb()).isEqualTo("0 0 0")
        assertThat(UsDarkColorScheme.surface.rgb()).isEqualTo("0 0 0")
        assertThat(UsDarkColorScheme.onSurface.rgb()).isEqualTo("247 249 249")
        // The web's exact dark pairing: the light ink under a near-black label.
        assertThat(UsDarkColorScheme.primary.rgb()).isEqualTo("90 155 216")
        assertThat(UsDarkColorScheme.onPrimary.rgb()).isEqualTo("15 20 25")
        assertThat(UsDarkColorScheme.error.rgb()).isEqualTo("248 113 113")
    }

    /**
     * What a filled button sits on. Light is the web's ink. Dark is the
     * web's deeper stop (`--theme-brand-deep`), NOT its ink: the app's
     * filled controls paint a white label, and white on the dark ink is
     * 2.95:1. The label is white in both.
     */
    @Test
    fun `a filled button's fill is a web value that carries a white label`() {
        assertThat(light.accentStrong.rgb()).isEqualTo("47 107 163")
        assertThat(dark.accentStrong.rgb()).isEqualTo("58 116 172")
        assertThat(light.onAccent.rgb()).isEqualTo("255 255 255")
        assertThat(dark.onAccent.rgb()).isEqualTo("255 255 255")
    }

    /** What is behind a video and what is drawn over it do not belong to a theme. */
    @Test
    fun `the stage and what is drawn over media are the same in both themes`() {
        assertThat(light.stage.rgb()).isEqualTo("8 12 18")
        assertThat(dark.stage).isEqualTo(light.stage)
        assertThat(light.onMedia.rgb()).isEqualTo("255 255 255")
        assertThat(dark.onMedia).isEqualTo(light.onMedia)
        assertThat(dark.scrim).isEqualTo(light.scrim)
    }

    /** A value left to inherit from the other theme silently reads wrong on this ground. */
    @Test
    fun `no themed colour is shared between the two themes`() {
        val themed = listOf<(UsExtendedColors) -> Color>(
            { it.textPrimary }, { it.textSecondary }, { it.textTertiary }, { it.textMuted },
            { it.textDim }, { it.textDimmest }, { it.textGhost }, { it.textBody },
            { it.bgCard }, { it.bgCardHover }, { it.bgCardSolid }, { it.bgCanvas }, { it.bgRaised },
            { it.borderSubtle }, { it.borderMedium }, { it.glassBg }, { it.glassBorder },
            { it.fillSubtle }, { it.fillStrong }, { it.unreadRow },
            { it.accent }, { it.accentSolid }, { it.accentDeep }, { it.accentStrong }, { it.accentOutline },
            { it.focusRing }, { it.statusSuccess }, { it.statusWarning }, { it.statusDanger }, { it.statusInfo },
            { it.liveRed }, { it.onlineGreen },
        )
        themed.forEachIndexed { index, read ->
            assertWithMessage("themed colour #$index").that(read(light)).isNotEqualTo(read(dark))
        }
    }

    // ── Against the web's file itself ───────────────────────────────────

    @Test
    fun `the table matches the web's theme file when the web checkout is present`() {
        val css = webThemeFile()
        assumeTrue("postbook-ui is not checked out beside this repository", css != null)
        val text = css!!.readText()
        val lightBlock = text.substringAfter(":root {").substringBefore("\n}")
        val darkBlock = text.substringAfter(".dark {").substringBefore("\n}")

        for (pair in pairs) {
            assertWithMessage("web light --theme-${pair.web}")
                .that(webValue(lightBlock, pair.web)).isEqualTo(pair.lightRgb)
            assertWithMessage("web dark --theme-${pair.web}")
                .that(webValue(darkBlock, pair.web)).isEqualTo(pair.darkRgb)
        }
        assertThat(webValue(lightBlock, "brand-deep")).isEqualTo("40 90 138")
        assertThat(webValue(darkBlock, "brand-deep")).isEqualTo(dark.accentStrong.rgb())
        assertThat(webValue(lightBlock, "on-primary")).isEqualTo(UsLightColorScheme.onPrimary.rgb())
        assertThat(webValue(darkBlock, "on-primary")).isEqualTo(UsDarkColorScheme.onPrimary.rgb())
        assertThat(text).contains("--reel-stage: ${light.stage.rgb()};")
        assertThat(text).contains("--reel-on-stage: ${light.onMedia.rgb()};")
    }

    /** `--theme-<name>: r g b;` inside one block, as written. */
    private fun webValue(block: String, name: String): String? =
        Regex("""--theme-${Regex.escape(name)}:\s*([^;]+);""").find(block)?.groupValues?.get(1)?.trim()

    /** The web's theme file, found by walking up from the module to the folder both checkouts sit in. */
    private fun webThemeFile(): File? =
        generateSequence(File("").absoluteFile) { it.parentFile }
            .map { File(it, "postbook-ui/src/ui/theme.css") }
            .firstOrNull { it.isFile }

    // ── Contrast ────────────────────────────────────────────────────────

    @Test
    fun `text and muted text clear 4_5 to 1 on the ground and on a card in both themes`() {
        for ((name, theme) in listOf("light" to light, "dark" to dark)) {
            for ((ground, surface) in listOf("ground" to theme.bgCanvas, "card" to theme.bgCardSolid)) {
                assertContrast("$name text on the $ground", theme.textPrimary, surface)
                assertContrast("$name secondary text on the $ground", theme.textSecondary, surface)
                assertContrast("$name tertiary text on the $ground", theme.textTertiary, surface)
                assertContrast("$name muted text on the $ground", theme.textMuted, surface)
            }
        }
    }

    @Test
    fun `a white label on a filled button clears 4_5 to 1 in both themes`() {
        assertContrast("light label on the fill", light.onAccent, light.accentStrong)
        assertContrast("dark label on the fill", dark.onAccent, dark.accentStrong)
        // The pressed fill on light is darker still; it must not lose the label.
        assertContrast("light label on the pressed fill", light.onAccent, light.accentDeep)
        // Material's own pair, which Material components colour both sides of.
        assertContrast("light onPrimary on primary", UsLightColorScheme.onPrimary, UsLightColorScheme.primary)
        assertContrast("dark onPrimary on primary", UsDarkColorScheme.onPrimary, UsDarkColorScheme.primary)
        assertContrast("light onError on error", UsLightColorScheme.onError, UsLightColorScheme.error)
        assertContrast("dark onError on error", UsDarkColorScheme.onError, UsDarkColorScheme.error)
    }

    /**
     * Why the 4.06:1 brand accent is never a button's fill or a link's
     * colour on the light theme: it does not carry text. If this ever
     * passes, the two-accent rule can go.
     */
    @Test
    fun `the light brand accent does not carry text, which is why the ink exists`() {
        assertThat(contrast(light.onAccent, light.accent)).isLessThan(MIN_TEXT_CONTRAST)
        assertThat(contrast(light.accent, light.bgCardSolid)).isAtLeast(MIN_GLYPH_CONTRAST)
        assertThat(contrast(dark.accent, dark.bgCardSolid)).isAtLeast(MIN_GLYPH_CONTRAST)
    }

    @Test
    fun `the accent as text and the status colours clear 4_5 to 1 on a card in both themes`() {
        for ((name, theme) in listOf("light" to light, "dark" to dark)) {
            assertContrast("$name accent text", theme.accentSolid, theme.bgCardSolid)
            assertContrast("$name danger", theme.statusDanger, theme.bgCardSolid)
            assertContrast("$name success", theme.statusSuccess, theme.bgCardSolid)
            assertContrast("$name warning", theme.statusWarning, theme.bgCardSolid)
            assertContrast("$name info", theme.statusInfo, theme.bgCardSolid)
        }
    }

    @Test
    fun `white over the stage clears 4_5 to 1`() {
        assertContrast("on media", light.onMedia, light.stage)
    }

    private fun assertContrast(what: String, foreground: Color, background: Color) {
        assertWithMessage("$what (${foreground.rgb()} on ${background.rgb()})")
            .that(contrast(foreground, background))
            .isAtLeast(MIN_TEXT_CONTRAST)
    }

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

    /** "r g b", the way the web's theme file writes a colour. */
    private fun Color.rgb(): String {
        val argb = value.shr(ARGB_SHIFT).toLong()
        return "${argb.shr(RED_SHIFT) and BYTE} ${argb.shr(GREEN_SHIFT) and BYTE} ${argb and BYTE}"
    }

    private companion object {
        const val MIN_TEXT_CONTRAST = 4.5
        const val MIN_GLYPH_CONTRAST = 3.0
        const val ALPHA_STEP = 0.005f
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
        const val RED_SHIFT = 16
        const val GREEN_SHIFT = 8
        const val BYTE = 0xFFL
    }
}
