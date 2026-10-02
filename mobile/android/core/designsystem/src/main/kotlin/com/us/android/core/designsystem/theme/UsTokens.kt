package com.us.android.core.designsystem.theme

import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

/**
 * Tokens Material 3 has no slot for.
 *
 * M3's ColorScheme covers primary/surface/error and so on, but it has no
 * concept of a 7-step text ramp, per-product brand gradients, or a glass
 * layer. Rather than abuse unrelated M3 slots to smuggle these through,
 * they live here and are read via [UsTheme.extended].
 */
@Immutable
data class UsExtendedColors(
    val textPrimary: Color,
    val textSecondary: Color,
    val textTertiary: Color,
    val textMuted: Color,
    val textDim: Color,
    val textDimmest: Color,
    val textGhost: Color,
    /** The text colour at 4% and 6%: a quiet wash under a tile or a pressed row. */
    val bgCard: Color,
    val bgCardHover: Color,
    /**
     * The SOLID card surface (the web's `brand-card`: white, black on dark)
     * and the page behind it (the web's `canvas`), plus the body-text step
     * the feed card uses between textPrimary and textMuted.
     */
    val bgCardSolid: Color,
    val bgCanvas: Color,
    val textBody: Color,
    val borderSubtle: Color,
    /** The web's one border (`brand-divider`). */
    val borderMedium: Color,
    val glassBg: Color,
    val glassBorder: Color,
    /** The legacy "at" logo square and its glyph. */
    val brandChip: Color,
    val onBrandChip: Color,
    /** Chat's green identity: outgoing bubbles, send, unread badges. */
    val chatAccent: Color,
    val chatOnline: Color,
    /** Presence: the web's success. */
    val onlineGreen: Color,
    /** A lit like, a live mark and a destructive row: the web's danger. */
    val liveRed: Color,
    val statusWarning: Color,
    val statusSuccess: Color,
    /**
     * Destructive and blocking copy: "Remove", "Cancel order", a form's
     * failure line, a stock warning that stops a sale. The web's danger.
     *
     * Its own token rather than `MaterialTheme.colorScheme.error`, because a
     * feature reaching into an M3 slot for a brand colour is how the palette
     * drifts.
     */
    val statusDanger: Color,
    /** The web's info: a neutral notice. */
    val statusInfo: Color,
    /**
     * A label or glyph ON a status fill (a success pill, a danger badge): the
     * web's `on-primary`, white on light and ink on dark, where the status
     * colours are bright.
     */
    val onStatus: Color,
    val postbookGradient: Brush,
    val postgramGradient: Brush,
    val posttubeGradient: Brush,
    val storyRingGradient: Brush,
    /**
     * The fill of a primary button, as a brush: [accentStrong], flat.
     *
     * It was Momentum's orange to red gradient. The web's primary button is a
     * flat fill of the ink; its one gradient (`bg-primary-grad`, lift to
     * deep) is NOT ported, because white on its light stop is 2.6:1. The name
     * and the type are kept so every caller keeps compiling.
     */
    val ctaGradient: Brush,
    /** An unread or selected row: the web's `brand-tint`. */
    val unreadRow: Color,
    /**
     * The accent where it is READ: a lit tab's label and glyph, a text link
     * like "Mark all read". The web's `brand-ink`, which clears 4.5:1 on the
     * ground in both themes. (Named "solid" from when it was the flat end of
     * the ember gradient.)
     */
    val accentSolid: Color,
    /** The fill while pressed, and the deeper stop where one is wanted: the web's `brand-ink-hover`. */
    val accentDeep: Color,
    /**
     * The brand accent itself (the web's `brand-accent`): a ring, a border, a
     * glyph, a focus outline. NOT for text: it is 4.06:1 on the light ground.
     */
    val accent: Color,
    /**
     * What a FILLED control sits on, under [onAccent]. White on it clears
     * 4.5:1 in both themes; see `UsColorTokens` for why the dark value is the
     * web's deeper stop rather than its ink.
     */
    val accentStrong: Color,
    /** The label on [accentStrong]: white, in both themes. */
    val onAccent: Color,
    /** An accent-coloured outline: the web's `brand-outline`. */
    val accentOutline: Color,
    /** The keyboard-focus ring: the web outlines focus in `brand-accent`. */
    val focusRing: Color,
    /**
     * Ink for text ON A WHITE control, and the deep fill some tiles use: the
     * web's deep cerulean (`brand-ink-hover` on light), a visible blue rather
     * than black, 7:1 against white. Identical in both themes, because the
     * white control it sits on is white in both. (It was Momentum's navy,
     * and keeps the name for its callers.)
     */
    val brandNavy: Color,
    /** The sunken or raised inline surface: the web's `brand-secondary`. */
    val bgRaised: Color,
    /** The text colour at 6% and 12%: a pill's fill, and the same pill lit or pressed (the web's `/ .06`, `/ .12`). */
    val fillSubtle: Color,
    val fillStrong: Color,
    /** What a sheet or a dialog dims the page with. The same in both themes. */
    val scrim: Color,
    /**
     * What is behind a video (the web's `reel-stage`), the same in BOTH
     * themes: a player's letterbox, the reel's page, the fullscreen ground.
     */
    val stage: Color,
    /**
     * Text and glyphs drawn OVER media, on its scrim: a reel's title, its
     * hashtags and its sound line (2026-09-30). White in both themes — what is
     * under them is the video and the scrim, never the theme's surface, so
     * they must not invert with the text ramp, which is ink on the light
     * theme. [onMediaMuted] is the quieter step: the same white at 70%.
     */
    val onMedia: Color,
    val onMediaMuted: Color,
    /**
     * Over media, like [onMedia], and fixed in both themes for the same
     * reason (2026-10-02): the dark plate behind a glyph or a short label on
     * a photo, the hairline round it, a progress track, and the dimmed white
     * of a disabled or unselected label on the stage.
     */
    val mediaPlate: Color,
    val mediaRim: Color,
    val mediaTrack: Color,
    val onMediaDim: Color,
    /** The glyph on a per-type tile gradient (Create, the launcher): white in both themes, as the tile is. */
    val onTile: Color,
    /**
     * A SELECTED pill and its label: the web's inverse highlight
     * (`brand-highlight` under `brand-bg`), ink on the light theme and
     * near-white on the dark one. It replaces the white pill with
     * [brandNavy] text, which was white on white on the light theme.
     */
    val selectedPill: Color,
    val onSelectedPill: Color,
    /**
     * What a sheet, a dialog or a menu is made of: the card on light, the
     * RAISED surface on dark, where the card is as black as the page.
     * Material's `surfaceContainer*` roles carry the same value, so a
     * Material sheet that names no colour gets it too.
     */
    val bgSheet: Color,
    /** Chat's green where it is READ (an unread time, a link): 4.5:1 on the ground in each theme. */
    val chatAccentText: Color,
    /** The glyph or count drawn ON [chatAccent]: dark ink, in both themes. */
    val onChatAccent: Color,
    /** The reel studio's text pill, as it is burned into the video: see [UsContentColors]. Not themed. */
    val pillNavy: Color,
    val pillWhite: Color,
    /** Your own chat bubble and its ink: a brand identity, the same in both themes. */
    val chatBubbleOwn: Color,
    val onChatBubbleOwn: Color,
    /** Per-sender name colours in a group thread, readable on the incoming bubble of each theme. */
    val chatSenders: List<Color>,
    /** The Create sheet's per-type circle gradients. See [UsCreateColors]. */
    val create: UsCreateColors,
    /** The Explore launcher's per-app tile gradients. See [UsLauncherColors]. */
    val launcher: UsLauncherColors,
)

/**
 * The Explore launcher's tiles — one gradient per mini-app (founder,
 * 2026-09-05). Six reuse a create ramp so the two grids read as one family;
 * Ask, Feast and Tube have no create twin and carry their own pairs from
 * [UsColorTokens]. Shared across themes, like [UsCreateColors].
 */
@Immutable
data class UsLauncherColors(
    val chat: UsCreateSwatch,
    val friends: UsCreateSwatch,
    val alerts: UsCreateSwatch,
    val live: UsCreateSwatch,
    val shop: UsCreateSwatch,
    val match: UsCreateSwatch,
    val ask: UsCreateSwatch,
    val feast: UsCreateSwatch,
    val tube: UsCreateSwatch,
)

/**
 * The Create sheet's tile circles — one gradient per thing you can make.
 *
 * Named tokens rather than colours inside `:feature:post`, because feature
 * modules never carry raw hex: the design owns these pairs (founder render,
 * 2026-09-04) and a screen only asks for "the audio gradient". [text] and
 * [live] are the ember accent itself — Text is the default create and ember
 * is the live colour — so both are the same brush as
 * [UsExtendedColors.ctaGradient]. The other five are vertical light → deep
 * pairs that read well on the navy ground. Shared across themes: the identity
 * does not invert.
 */
@Immutable
data class UsCreateColors(
    val text: UsCreateSwatch,
    val photo: UsCreateSwatch,
    val reel: UsCreateSwatch,
    val audio: UsCreateSwatch,
    val poll: UsCreateSwatch,
    val article: UsCreateSwatch,
    val live: UsCreateSwatch,
)

/**
 * One create type's colour: the fill [brush] for its icon tile and the solid
 * [glow] the tile casts beneath itself — the deep end of the same ramp, so a
 * coloured shadow reads as light from the tile rather than a second colour.
 */
@Immutable
data class UsCreateSwatch(
    val brush: Brush,
    val glow: Color,
)

/** Corner radii, ported from app_spacing.dart. */
@Immutable
data class UsRadii(
    val small: Dp = 8.dp,
    val medium: Dp = 12.dp,
    val large: Dp = 16.dp,
    val extraLarge: Dp = 20.dp,
    val full: Dp = 9999.dp,
    /** Momentum card radius — the feed card and other primary surfaces. */
    val card: Dp = 24.dp,
    /** Momentum media radius — image/video attachments inside a card. */
    val media: Dp = 16.dp,
    /** Momentum panel radius — raised inline panels (the requests panel). */
    val panel: Dp = 14.dp,
    /** Momentum pill radius — small chips like "Follow back". */
    val pill: Dp = 6.dp,
)

/**
 * Spacing scale, ported from app_spacing.dart.
 *
 * The Flutter scale is irregular (4/6/8/12/14/16/18/20) rather than a clean
 * 4pt grid. Preserved exactly so ported screens match the reference
 * pixel-for-pixel; normalising it is a design decision, not a port decision.
 */
@Immutable
data class UsSpacing(
    val xs: Dp = 4.dp,
    val s: Dp = 6.dp,
    val m: Dp = 8.dp,
    val l: Dp = 12.dp,
    val xl: Dp = 14.dp,
    val xxl: Dp = 16.dp,
    val xxxl: Dp = 18.dp,
    val xxxxl: Dp = 20.dp,
    /** Horizontal page gutter. app_spacing.dart uses 18dp, not 16dp. */
    val pageHorizontal: Dp = 18.dp,
)

internal val LocalUsExtendedColors = staticCompositionLocalOf<UsExtendedColors> {
    error("UsExtendedColors not provided — wrap the tree in UsTheme { }")
}

internal val LocalUsRadii = staticCompositionLocalOf { UsRadii() }

internal val LocalUsSpacing = staticCompositionLocalOf { UsSpacing() }

/**
 * Ember, orange to red: the colour of the Text and Go Live create tiles and
 * of the launcher's Live tile. It was the whole app's accent under Momentum;
 * it is now only these tiles' own identity.
 */
private val EmberGradient: Brush = Brush.horizontalGradient(
    listOf(UsColorTokens.CreateEmberLight, UsColorTokens.CreateEmberDeep),
)

/** A create tile: light at the top, deep at the bottom, glowing in the deep. */
private fun createSwatch(light: Color, deep: Color): UsCreateSwatch =
    UsCreateSwatch(brush = Brush.verticalGradient(listOf(light, deep)), glow = deep)

/** Ember for Text and Live, glowing in its deep end. */
private val EmberSwatch = UsCreateSwatch(brush = EmberGradient, glow = UsColorTokens.CreateEmberDeep)

/** The per-type tiles: identities of their own, the same in both themes. */
private val CreateColors = UsCreateColors(
    text = EmberSwatch,
    photo = createSwatch(UsColorTokens.CreatePhotoLight, UsColorTokens.CreatePhotoDeep),
    reel = createSwatch(UsColorTokens.CreateReelLight, UsColorTokens.CreateReelDeep),
    audio = createSwatch(UsColorTokens.CreateAudioLight, UsColorTokens.CreateAudioDeep),
    poll = createSwatch(UsColorTokens.CreatePollLight, UsColorTokens.CreatePollDeep),
    article = createSwatch(UsColorTokens.CreateArticleLight, UsColorTokens.CreateArticleDeep),
    live = EmberSwatch,
)

private val LauncherColors = UsLauncherColors(
    chat = createSwatch(UsColorTokens.CreateAudioLight, UsColorTokens.CreateAudioDeep),
    friends = createSwatch(UsColorTokens.CreatePhotoLight, UsColorTokens.CreatePhotoDeep),
    alerts = createSwatch(UsColorTokens.CreatePollLight, UsColorTokens.CreatePollDeep),
    live = EmberSwatch,
    shop = createSwatch(UsColorTokens.CreateArticleLight, UsColorTokens.CreateArticleDeep),
    match = createSwatch(UsColorTokens.CreateReelLight, UsColorTokens.CreateReelDeep),
    ask = createSwatch(UsColorTokens.LaunchAskLight, UsColorTokens.LaunchAskDeep),
    feast = createSwatch(UsColorTokens.LaunchFeastLight, UsColorTokens.LaunchFeastDeep),
    tube = createSwatch(UsColorTokens.LaunchTubeLight, UsColorTokens.LaunchTubeDeep),
)

/** A flat fill, as the brush the primary button's `background` takes. */
private fun flat(color: Color): Brush = SolidColor(color)

/** The dark theme: the web's `.dark` block. */
internal val DarkExtendedColors = UsExtendedColors(
    textPrimary = UsColorTokens.Dark.TextPrimary,
    textSecondary = UsColorTokens.Dark.TextSecondary,
    textTertiary = UsColorTokens.Dark.TextTertiary,
    textMuted = UsColorTokens.Dark.TextMuted,
    textDim = UsColorTokens.Dark.TextDim,
    textDimmest = UsColorTokens.Dark.TextDimmest,
    textGhost = UsColorTokens.Dark.TextGhost,
    bgCard = UsColorTokens.Dark.Wash,
    bgCardHover = UsColorTokens.Dark.FillSubtle,
    bgCardSolid = UsColorTokens.Dark.Card,
    bgCanvas = UsColorTokens.Dark.Canvas,
    textBody = UsColorTokens.Dark.TextSecondary,
    borderSubtle = UsColorTokens.Dark.BorderSubtle,
    borderMedium = UsColorTokens.Dark.Border,
    glassBg = UsColorTokens.Dark.GlassBg,
    glassBorder = UsColorTokens.Dark.GlassBorder,
    brandChip = UsColorTokens.BrandChip,
    onBrandChip = UsColorTokens.OnBrandChip,
    chatAccent = UsColorTokens.ChatAccent,
    chatOnline = UsColorTokens.ChatOnline,
    onlineGreen = UsColorTokens.Dark.Success,
    liveRed = UsColorTokens.Dark.Danger,
    statusWarning = UsColorTokens.Dark.Warning,
    statusSuccess = UsColorTokens.Dark.Success,
    statusDanger = UsColorTokens.Dark.Danger,
    statusInfo = UsColorTokens.Dark.Info,
    onStatus = UsColorTokens.Dark.OnPrimary,
    postbookGradient = Brush.horizontalGradient(
        listOf(UsColorTokens.PostbookPrimary, UsColorTokens.PostbookSecondary),
    ),
    postgramGradient = Brush.horizontalGradient(
        listOf(UsColorTokens.PostgramPrimary, UsColorTokens.PostbookPrimary),
    ),
    posttubeGradient = Brush.horizontalGradient(
        listOf(UsColorTokens.PosttubePrimary, UsColorTokens.AccentPurple),
    ),
    storyRingGradient = Brush.sweepGradient(
        listOf(
            UsColorTokens.PostbookPrimary,
            UsColorTokens.PostgramPrimary,
            UsColorTokens.AccentPurple,
            UsColorTokens.PostbookPrimary,
        ),
    ),
    ctaGradient = flat(UsColorTokens.Dark.AccentFill),
    unreadRow = UsColorTokens.Dark.AccentTint,
    accentSolid = UsColorTokens.Dark.AccentText,
    accentDeep = UsColorTokens.Dark.AccentPressed,
    accent = UsColorTokens.Dark.Accent,
    accentStrong = UsColorTokens.Dark.AccentFill,
    onAccent = UsColorTokens.OnAccentFill,
    accentOutline = UsColorTokens.Dark.AccentOutline,
    focusRing = UsColorTokens.Dark.Accent,
    brandNavy = UsColorTokens.InkOnWhite,
    bgRaised = UsColorTokens.Dark.Sunken,
    fillSubtle = UsColorTokens.Dark.FillSubtle,
    fillStrong = UsColorTokens.Dark.FillStrong,
    scrim = UsColorTokens.Scrim,
    stage = UsColorTokens.Stage,
    onMedia = UsColorTokens.OnMedia,
    onMediaMuted = UsColorTokens.OnMediaMuted,
    mediaPlate = UsColorTokens.MediaPlate,
    mediaRim = UsColorTokens.MediaRim,
    mediaTrack = UsColorTokens.MediaTrack,
    onMediaDim = UsColorTokens.OnMediaDim,
    onTile = UsColorTokens.OnTile,
    selectedPill = UsColorTokens.Dark.SelectedPill,
    onSelectedPill = UsColorTokens.Dark.OnSelectedPill,
    bgSheet = UsColorTokens.Dark.Sheet,
    chatAccentText = UsColorTokens.Dark.ChatAccentText,
    onChatAccent = UsColorTokens.OnChatAccent,
    pillNavy = UsColorTokens.PillNavy,
    pillWhite = UsColorTokens.PillWhite,
    chatBubbleOwn = UsColorTokens.ChatBubbleOwn,
    onChatBubbleOwn = UsColorTokens.OnChatBubbleOwn,
    chatSenders = UsColorTokens.Dark.ChatSenders,
    create = CreateColors,
    launcher = LauncherColors,
)

/**
 * The light theme: the web's `:root` block. Every themed value is restated
 * (the web's own rule for its dark block, for the same reason: a value left
 * to inherit silently reads wrong on the other ground). What is shared is
 * what does not belong to a theme: the scrim, the stage, the over-media
 * whites and the per-type tile identities.
 */
internal val LightExtendedColors = DarkExtendedColors.copy(
    textPrimary = UsColorTokens.Light.TextPrimary,
    textSecondary = UsColorTokens.Light.TextSecondary,
    textTertiary = UsColorTokens.Light.TextTertiary,
    textMuted = UsColorTokens.Light.TextMuted,
    textDim = UsColorTokens.Light.TextDim,
    textDimmest = UsColorTokens.Light.TextDimmest,
    textGhost = UsColorTokens.Light.TextGhost,
    bgCard = UsColorTokens.Light.Wash,
    bgCardHover = UsColorTokens.Light.FillSubtle,
    bgCardSolid = UsColorTokens.Light.Card,
    bgCanvas = UsColorTokens.Light.Canvas,
    textBody = UsColorTokens.Light.TextSecondary,
    borderSubtle = UsColorTokens.Light.BorderSubtle,
    borderMedium = UsColorTokens.Light.Border,
    glassBg = UsColorTokens.Light.GlassBg,
    glassBorder = UsColorTokens.Light.GlassBorder,
    brandChip = UsColorTokens.BrandChipLight,
    onBrandChip = UsColorTokens.OnBrandChip,
    onlineGreen = UsColorTokens.Light.Success,
    liveRed = UsColorTokens.Light.Danger,
    statusWarning = UsColorTokens.Light.Warning,
    statusSuccess = UsColorTokens.Light.Success,
    statusDanger = UsColorTokens.Light.Danger,
    statusInfo = UsColorTokens.Light.Info,
    onStatus = UsColorTokens.Light.OnPrimary,
    ctaGradient = flat(UsColorTokens.Light.AccentFill),
    unreadRow = UsColorTokens.Light.AccentTint,
    accentSolid = UsColorTokens.Light.AccentText,
    accentDeep = UsColorTokens.Light.AccentPressed,
    accent = UsColorTokens.Light.Accent,
    accentStrong = UsColorTokens.Light.AccentFill,
    accentOutline = UsColorTokens.Light.AccentOutline,
    focusRing = UsColorTokens.Light.Accent,
    bgRaised = UsColorTokens.Light.Sunken,
    fillSubtle = UsColorTokens.Light.FillSubtle,
    fillStrong = UsColorTokens.Light.FillStrong,
    selectedPill = UsColorTokens.Light.SelectedPill,
    onSelectedPill = UsColorTokens.Light.OnSelectedPill,
    bgSheet = UsColorTokens.Light.Sheet,
    chatAccentText = UsColorTokens.Light.ChatAccentText,
    chatSenders = UsColorTokens.Light.ChatSenders,
)
