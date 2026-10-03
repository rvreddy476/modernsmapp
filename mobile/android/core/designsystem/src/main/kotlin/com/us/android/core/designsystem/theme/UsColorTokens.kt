package com.us.android.core.designsystem.theme

import androidx.compose.ui.graphics.Color

/**
 * Colour tokens. THE one place a colour is written (founder's rule: colour
 * and font come from one file).
 *
 * ## THE VALUES ARE THE WEB'S (founder, 2026-10-02)
 *
 * "Android must match the web's themes, light and dark." Every value under
 * [Light] and [Dark] that has a counterpart in the web's one theme file
 * (`postbook-ui/src/ui/theme.css`) IS that value, and the comment beside it
 * names the web token. `UsWebThemeParityTest` pins each pair, so a change on
 * either side that is not made on the other fails a test.
 *
 * This replaces Momentum's navy ground and ember (orange to red) accent
 * (Figma YsWb936muw8pwIxgb0je2A, 2026-09-03), which the web left on
 * 2026-09-19 for a white ground with a cerulean accent.
 *
 * ## TWO ACCENTS, AND WHY
 *
 * The brand accent (`--theme-brand-accent`, #3B82C4 on white) measures
 * 4.06:1, which clears 3:1 for a border, a ring or an icon and NOT 4.5:1 for
 * text. So, as on the web:
 *
 *  - [Light.Accent] / [Dark.Accent] is for rings, borders and glyphs;
 *  - `AccentText` (`--theme-brand-ink`) is the same hue where it has to be
 *    READ: a link, a lit label;
 *  - `AccentFill` is what a filled button sits on. Its label is WHITE in
 *    both themes.
 *
 * The one place this departs from the web: on the dark theme the web fills a
 * button with the light ink (#5A9BD8) and flips the label to near-black
 * (`--theme-on-primary: 15 20 25`). The app's filled controls paint a white
 * label throughout the features, and white on #5A9BD8 is 2.95:1. So
 * the dark fill here is the web's own deeper stop of the same cerulean,
 * `--theme-brand-deep` (#3A74AC, 4.9:1 under white), and the label stays
 * white. Material's `primary` / `onPrimary` pair, which Material components
 * colour BOTH sides of, keeps the web's exact pairing.
 *
 * ## WHAT HAS NO WEB COUNTERPART
 *
 * The web has two text colours (text, muted); the app's ramp has seven
 * steps. The five between and below are derived here, once, by mixing the
 * web's two (the factor is beside each), so no step is a new hue. The Create
 * sheet's and the launcher's per-type gradients, the story ring, chat's
 * green and the avatar palette are identities of their own and are not part
 * of either theme.
 */
internal object UsColorTokens {

    /** The light theme: `:root` in the web's theme.css. */
    object Light {
        // ── Surfaces ───────────────────────────────────────────────────
        /** `--theme-canvas` 243 246 250: the page, behind the cards. */
        val Canvas = Color(0xFFF3F6FA)

        /** `--theme-brand-card` / `--theme-brand-bg` 255 255 255: cards, sheets and chrome. */
        val Card = Color(0xFFFFFFFF)

        /** `--theme-brand-secondary` 247 249 249: the sunken / raised inline surface. */
        val Sunken = Color(0xFFF7F9F9)

        /** `--theme-brand-tint` 234 242 250: an unread or selected row. */
        val AccentTint = Color(0xFFEAF2FA)

        // ── Text ───────────────────────────────────────────────────────
        /** `--theme-brand-text` 15 20 25. */
        val TextPrimary = Color(0xFF0F1419)

        /** Derived: text mixed 1/3 toward muted. */
        val TextSecondary = Color(0xFF262F36)

        /** Derived: text mixed 2/3 toward muted. */
        val TextTertiary = Color(0xFF3C4954)

        /** `--theme-text-muted` 83 100 113. */
        val TextMuted = Color(0xFF536471)

        /** Derived: muted mixed 30% / 50% / 65% toward the card. Hints and disabled text only. */
        val TextDim = Color(0xFF87939C)
        val TextDimmest = Color(0xFFA9B2B8)
        val TextGhost = Color(0xFFC3C9CD)

        // ── Lines and fills made of the text colour ────────────────────
        /** `--theme-brand-divider` rgba(15, 20, 25, 0.1): the web's one border. */
        val Border = Color(0x1A0F1419)

        /** Derived: the divider at half strength. */
        val BorderSubtle = Color(0x0D0F1419)

        /** The text colour at 4%, 6% and 12%: the web's `brand-text / .06` and `/ .12` pill fills. */
        val Wash = Color(0x0A0F1419)
        val FillSubtle = Color(0x0F0F1419)
        val FillStrong = Color(0x1F0F1419)

        /** `--theme-glass-bg` / `--theme-glass-border`, at the alphas the app's glass uses. */
        val GlassBg = Color(0x1A0F1419)
        val GlassBorder = Color(0x140F1419)

        // ── Accent ─────────────────────────────────────────────────────
        /** `--theme-brand-accent` 59 130 196. Rings, borders, glyphs. Not text: 4.06:1. */
        val Accent = Color(0xFF3B82C4)

        /** `--theme-brand-ink` 47 107 163: the accent where it is read. 5.60:1 on white. */
        val AccentText = Color(0xFF2F6BA3)

        /** `--theme-brand-ink` 47 107 163: what a filled button sits on, under a white label. */
        val AccentFill = Color(0xFF2F6BA3)

        /** `--theme-brand-ink-hover` 40 90 138: the fill while pressed. */
        val AccentPressed = Color(0xFF285A8A)

        /** `--theme-brand-outline` 185 212 236: an accent-coloured outline. */
        val AccentOutline = Color(0xFFB9D4EC)

        /** `--theme-on-primary` 255 255 255. */
        val OnPrimary = Color(0xFFFFFFFF)

        // ── Status ─────────────────────────────────────────────────────
        /** `--theme-success` 27 127 75. */
        val Success = Color(0xFF1B7F4B)

        /** `--theme-warning` 138 83 0. */
        val Warning = Color(0xFF8A5300)

        /** `--theme-danger` 198 40 40. */
        val Danger = Color(0xFFC62828)

        /** `--theme-info` 47 107 163. */
        val Info = Color(0xFF2F6BA3)

        // ── Roles added 2026-10-02 (light-mode pass) ───────────────────
        /** `--theme-brand-highlight` 15 20 25: a SELECTED pill's fill (the web's inverse highlight). */
        val SelectedPill = Color(0xFF0F1419)

        /** `--theme-brand-bg` 255 255 255: the label on [SelectedPill]. */
        val OnSelectedPill = Color(0xFFFFFFFF)

        /** `--theme-brand-card` 255 255 255: a sheet, a dialog, a menu. The dimmed page separates it. */
        val Sheet = Color(0xFFFFFFFF)

        /** `--theme-success` 27 127 75: chat's green where it is READ on the light ground. */
        val ChatAccentText = Color(0xFF1B7F4B)

        /**
         * Per-sender name colours in a group thread, each at 4.5:1 or better
         * on the white incoming bubble. The web's own inks where it has one:
         * success, `brand-ink`, warning.
         */
        val ChatSenders = listOf(
            Color(0xFF7B1FA2),
            Color(0xFF1B7F4B),
            Color(0xFFB45309),
            Color(0xFF2F6BA3),
            Color(0xFF0F766E),
            Color(0xFF8A5300),
        )
    }

    /** The dark theme: `.dark` in the web's theme.css. */
    object Dark {
        // ── Surfaces ───────────────────────────────────────────────────
        /** `--theme-canvas` 0 0 0. Dark keeps a black ground; cards are separated by their borders. */
        val Canvas = Color(0xFF000000)

        /** `--theme-brand-card` / `--theme-brand-bg` 0 0 0. */
        val Card = Color(0xFF000000)

        /** `--theme-brand-secondary` 22 24 28. */
        val Sunken = Color(0xFF16181C)

        /** `--theme-brand-tint` 18 36 58. */
        val AccentTint = Color(0xFF12243A)

        // ── Text ───────────────────────────────────────────────────────
        /** `--theme-brand-text` 247 249 249. */
        val TextPrimary = Color(0xFFF7F9F9)

        /** Derived: text mixed 1/3 toward muted. */
        val TextSecondary = Color(0xFFD3D9DD)

        /** Derived: text mixed 2/3 toward muted. */
        val TextTertiary = Color(0xFFAFB8C1)

        /** `--theme-text-muted` 139 152 165. */
        val TextMuted = Color(0xFF8B98A5)

        /** Derived: muted mixed 30% / 50% / 65% toward the card. Hints and disabled text only. */
        val TextDim = Color(0xFF616A74)
        val TextDimmest = Color(0xFF464C53)
        val TextGhost = Color(0xFF31353A)

        // ── Lines and fills made of the text colour ────────────────────
        /** `--theme-brand-divider` rgba(247, 249, 249, 0.15). */
        val Border = Color(0x26F7F9F9)

        /** Derived: the divider at half strength. */
        val BorderSubtle = Color(0x13F7F9F9)

        val Wash = Color(0x0AF7F9F9)
        val FillSubtle = Color(0x0FF7F9F9)
        val FillStrong = Color(0x1FF7F9F9)

        val GlassBg = Color(0x1AF7F9F9)
        val GlassBorder = Color(0x14F7F9F9)

        // ── Accent ─────────────────────────────────────────────────────
        /** `--theme-brand-accent` 90 155 216. 7.11:1 on black. */
        val Accent = Color(0xFF5A9BD8)

        /** `--theme-brand-ink` 90 155 216: the accent where it is read. */
        val AccentText = Color(0xFF5A9BD8)

        /**
         * `--theme-brand-deep` 58 116 172: what a filled button sits on, under
         * a WHITE label (4.9:1). See the class note: the web's dark fill is
         * the light ink with a near-black label, which the app's white labels
         * cannot sit on.
         */
        val AccentFill = Color(0xFF3A74AC)

        /** `--theme-brand-ink-hover` 106 168 224. */
        val AccentPressed = Color(0xFF6AA8E0)

        /** `--theme-brand-outline` 44 74 104. */
        val AccentOutline = Color(0xFF2C4A68)

        /** `--theme-on-primary` 15 20 25: the label on Material's `primary`, which is the light ink here. */
        val OnPrimary = Color(0xFF0F1419)

        // ── Status ─────────────────────────────────────────────────────
        /** `--theme-success` 74 222 128. */
        val Success = Color(0xFF4ADE80)

        /** `--theme-warning` 251 191 36. */
        val Warning = Color(0xFFFBBF24)

        /** `--theme-danger` 248 113 113. */
        val Danger = Color(0xFFF87171)

        /** `--theme-info` 106 168 224. */
        val Info = Color(0xFF6AA8E0)

        // ── Roles added 2026-10-02 (light-mode pass) ───────────────────
        /** `--theme-brand-highlight` 247 249 249: a SELECTED pill's fill. */
        val SelectedPill = Color(0xFFF7F9F9)

        /** `--theme-brand-bg` 0 0 0: the label on [SelectedPill]. */
        val OnSelectedPill = Color(0xFF000000)

        /**
         * `--theme-brand-secondary` 22 24 28: a sheet, a dialog, a menu. The
         * page and the card are both black on dark, so a sheet in the card
         * colour is black on black; the raised surface is what separates it.
         */
        val Sheet = Color(0xFF16181C)

        /** Chat's green where it is read on the dark ground: `--theme-success` 74 222 128. */
        val ChatAccentText = Color(0xFF4ADE80)

        /** Per-sender name colours in a group thread, each at 4.5:1 or better on the black incoming bubble. */
        val ChatSenders = listOf(
            Color(0xFFC084FC),
            Color(0xFF4ADE80),
            Color(0xFFFB923C),
            Color(0xFF6AA8E0),
            Color(0xFF4ECDC4),
            Color(0xFFFBBF24),
        )
    }

    // ── Shared by both themes ──────────────────────────────────────────

    /**
     * Ink for text on a WHITE control (a white chip, a white tile), in both
     * themes: `--theme-brand-ink-hover` 40 90 138 from the light block. A
     * visible blue, not black; 7:1 against white.
     */
    val InkOnWhite = Color(0xFF285A8A)

    /** The label on [Light.AccentFill] and [Dark.AccentFill]: white in both themes. */
    val OnAccentFill = Color(0xFFFFFFFF)

    /**
     * `--reel-stage` 8 12 18: what is behind a video, in BOTH themes. A
     * player's letterbox, the reel's page, the fullscreen ground.
     */
    val Stage = Color(0xFF080C12)

    /**
     * `--reel-on-stage` 255 255 255: text and glyphs drawn over a video, on
     * its scrim. White in BOTH themes: what is under them is the video,
     * never the theme's surface. The quieter step is the same white at 70%.
     */
    val OnMedia = Color(0xFFFFFFFF)
    val OnMediaMuted = Color(0xB3FFFFFF)

    /** What a sheet or a dialog dims the page with: the stage at 55%. */
    val Scrim = Color(0x8C080C12)

    // ── Over media (2026-10-02). Fixed in both themes for the same reason
    // as [OnMedia]: what is under them is a photo or a video, not the theme.
    /** A plate behind a glyph or a short label over media: the stage at 60%. */
    val MediaPlate = Color(0x99080C12)

    /** A hairline around a plate or an avatar over media: white at 20%. */
    val MediaRim = Color(0x33FFFFFF)

    /** A progress track over media: white at 25%. */
    val MediaTrack = Color(0x40FFFFFF)

    /** A disabled or unselected label over the stage: white at 55%. */
    val OnMediaDim = Color(0x8CFFFFFF)

    /**
     * The glyph on a per-type TILE gradient (Create, the launcher, an empty
     * state's icon square): white in both themes, because the tile is its
     * own colour in both.
     */
    val OnTile = Color(0xFFFFFFFF)

    // ── Avatar initials (2026-10-02: moved here from UsAvatar) ──────────
    /** A person's fallback disc: the seed hashes into this palette, under white initials. */
    val AvatarPalette = listOf(
        Color(0xFF1A73E8),
        Color(0xFF7B1FA2),
        Color(0xFFC2185B),
        Color(0xFF00796B),
        Color(0xFFC2410C),
        Color(0xFF455A64),
        Color(0xFF5D4037),
        Color(0xFF283593),
    )
    val OnAvatar = Color(0xFFFFFFFF)

    /** The reel studio's text pill, as Compose colours: see [UsContentColors]. */
    val PillNavy = Color(UsContentColors.PILL_NAVY_ARGB)
    val PillWhite = Color(UsContentColors.PILL_WHITE_ARGB)

    // ── Brand (legacy per-product gradients, kept for the story ring and
    // surfaces that have not moved to the single accent) ─────────────────
    val PostbookPrimary = Color(0xFFFF6B35)
    val PostbookSecondary = Color(0xFFFF8F65)
    val PostgramPrimary = Color(0xFFFF3366)
    val PostgramSecondary = Color(0xFFC850C0)
    val PosttubePrimary = Color(0xFF4ECDC4)
    val AccentPurple = Color(0xFF7B68EE)

    // ── Create sheet (founder render, 2026-09-04) ───────────────────────
    // Each create type's own two-stop gradient, light → deep. Text and Go
    // Live were the ember accent; they keep the ember pair as THEIR colour
    // (ember is still the live colour on the tile), which is why the two
    // stops live on here although the app's accent is no longer ember.
    val CreateEmberLight = Color(0xFFFB923C)
    val CreateEmberDeep = Color(0xFFDC2626)
    val CreatePhotoLight = Color(0xFF34D399)
    val CreatePhotoDeep = Color(0xFF059669)
    val CreateReelLight = Color(0xFFFB7185)
    val CreateReelDeep = Color(0xFFE11D48)
    val CreateAudioLight = Color(0xFFC084FC)
    val CreateAudioDeep = Color(0xFF7C3AED)
    val CreatePollLight = Color(0xFFFCD34D)
    val CreatePollDeep = Color(0xFFD97706)
    val CreateArticleLight = Color(0xFF22D3EE)
    val CreateArticleDeep = Color(0xFF0891B2)

    // ── Explore launcher (founder, 2026-09-05) ─────────────────────────
    val LaunchAskLight = Color(0xFF818CF8)
    val LaunchAskDeep = Color(0xFF4F46E5)
    val LaunchFeastLight = Color(0xFFFBBF24)
    val LaunchFeastDeep = Color(0xFFEA580C)
    val LaunchTubeLight = Color(0xFFF87171)
    val LaunchTubeDeep = Color(0xFFB91C1C)

    // ── Chat (Figma chat tour 98:*) ────────────────────────────────────
    // The chat vertical's own accent: outgoing bubbles, send, unread
    // badges. Shared across themes. NOT yet the web's: the web's chat is
    // black and white with `--theme-send` as its one colour.
    val ChatAccent = Color(0xFF22C55E)
    val ChatOnline = Color(0xFF4ADE80)

    /** The glyph or count ON chat's green (send, a badge): the web's ink, 8:1. White on it is 2.3:1. */
    val OnChatAccent = Color(0xFF0F1419)

    /** Your own bubble: a soft green paper note with dark ink, the same in both themes. */
    val ChatBubbleOwn = Color(0xFFD9FDD3)
    val OnChatBubbleOwn = Color(0xFF10231B)

    // ── Brand chip (legacy "at" logo square) ────────────────────────────
    val BrandChip = Color(0xFFFFFFFF)
    val OnBrandChip = Color(0xFF0F1419)
    val BrandChipLight = Color(0xFFF7F9F9)
}

/**
 * Colours burned INTO a creator's video: the reel studio's text pill
 * (2026-10-02). They are the creator's content, not the app's chrome, so
 * they belong to no theme and never invert.
 *
 * Public and plain ARGB ints because the exporter paints them with
 * `android.graphics.Paint`, outside Compose; the studio's preview reads the
 * same two through `UsTheme.extended.pillNavy` / `pillWhite`, so the preview
 * and the exported file cannot drift. (They had: the preview read `brandNavy`,
 * which moved to the web's cerulean on 2026-10-02 while the export kept
 * Momentum's navy.)
 */
object UsContentColors {
    const val PILL_NAVY_ARGB: Int = 0xFF0F3460.toInt()
    const val PILL_WHITE_ARGB: Int = 0xFFFFFFFF.toInt()
}
