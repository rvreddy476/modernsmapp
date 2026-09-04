package com.us.android.core.designsystem.theme

import androidx.compose.runtime.Immutable
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
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
    val bgCard: Color,
    val bgCardHover: Color,
    /**
     * Momentum: the SOLID card surface (`#0B1B2E`) and its canvas
     * (`#041122`), plus the body-text step the feed card uses between
     * textPrimary and textMuted.
     */
    val bgCardSolid: Color,
    val bgCanvas: Color,
    val textBody: Color,
    val borderSubtle: Color,
    val borderMedium: Color,
    val glassBg: Color,
    val glassBorder: Color,
    /** The "at" logo square and its glyph — white/black dark, cream/ink light. */
    val brandChip: Color,
    val onBrandChip: Color,
    /** Chat's green identity: outgoing bubbles, send, unread badges. */
    val chatAccent: Color,
    val chatOnline: Color,
    val onlineGreen: Color,
    val liveRed: Color,
    val statusWarning: Color,
    val statusSuccess: Color,
    val postbookGradient: Brush,
    val postgramGradient: Brush,
    val posttubeGradient: Brush,
    val storyRingGradient: Brush,
    /** Momentum's orange→red accent gradient. The CTA fill, and every other gradient use. */
    val ctaGradient: Brush,
    /**
     * The unread/highlight row surface (Momentum: `#072440` dark, `#FFF4EC`
     * light) — notification and message rows sit on this while unread.
     */
    val unreadRow: Color,
    /**
     * Momentum's solid accent orange (`#FB923C`), for the places the design
     * uses a flat colour rather than the gradient: the active tab label and
     * icon, and header text links like "Mark all read".
     */
    val accentSolid: Color,
    /**
     * The deep end of the accent gradient (`#DC2626`): the create button's
     * drop shadow and the count inside the header's white badge.
     */
    val accentDeep: Color,
    /** The raised inline-panel surface (`#071D33` dark, `#EEF3F9` light). */
    val bgRaised: Color,
    /** The Create sheet's per-type circle gradients. See [UsCreateColors]. */
    val create: UsCreateColors,
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
 * Momentum's brand identity: the centre create button, primary pills, avatar
 * rings, the unread dot and the Text/Go Live create circles all pull from
 * this one gradient. Declared BEFORE the palettes that read it — top-level
 * properties initialise in file order.
 */
private val EmberGradient: Brush = Brush.horizontalGradient(
    listOf(UsColorTokens.AccentOrange, UsColorTokens.AccentRed),
)

/** A create tile: light at the top, deep at the bottom, glowing in the deep. */
private fun createSwatch(light: Color, deep: Color): UsCreateSwatch =
    UsCreateSwatch(brush = Brush.verticalGradient(listOf(light, deep)), glow = deep)

/** Ember for Text and Live: the accent brush, glowing in the accent red. */
private val EmberSwatch = UsCreateSwatch(brush = EmberGradient, glow = UsColorTokens.AccentRed)

internal val DarkExtendedColors = UsExtendedColors(
    textPrimary = UsColorTokens.TextPrimary,
    textSecondary = UsColorTokens.TextSecondary,
    textTertiary = UsColorTokens.TextTertiary,
    textMuted = UsColorTokens.TextMuted,
    textDim = UsColorTokens.TextDim,
    textDimmest = UsColorTokens.TextDimmest,
    textGhost = UsColorTokens.TextGhost,
    bgCard = UsColorTokens.BgCard,
    bgCardHover = UsColorTokens.BgCardHover,
    bgCardSolid = UsColorTokens.FeedCard,
    bgCanvas = UsColorTokens.FeedCanvas,
    textBody = UsColorTokens.TextBody,
    borderSubtle = UsColorTokens.BorderSubtle,
    borderMedium = UsColorTokens.BorderMedium,
    glassBg = UsColorTokens.GlassBg,
    glassBorder = UsColorTokens.GlassBorder,
    brandChip = UsColorTokens.BrandChip,
    onBrandChip = UsColorTokens.OnBrandChip,
    chatAccent = UsColorTokens.ChatAccent,
    chatOnline = UsColorTokens.ChatOnline,
    onlineGreen = UsColorTokens.OnlineGreen,
    liveRed = UsColorTokens.LiveRed,
    statusWarning = UsColorTokens.StatusWarning,
    statusSuccess = UsColorTokens.StatusSuccess,
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
    ctaGradient = EmberGradient,
    unreadRow = UsColorTokens.UnreadRow,
    accentSolid = UsColorTokens.AccentOrange,
    accentDeep = UsColorTokens.AccentRed,
    bgRaised = UsColorTokens.BgTertiary,
    create = UsCreateColors(
        text = EmberSwatch,
        photo = createSwatch(UsColorTokens.CreatePhotoLight, UsColorTokens.CreatePhotoDeep),
        reel = createSwatch(UsColorTokens.CreateReelLight, UsColorTokens.CreateReelDeep),
        audio = createSwatch(UsColorTokens.CreateAudioLight, UsColorTokens.CreateAudioDeep),
        poll = createSwatch(UsColorTokens.CreatePollLight, UsColorTokens.CreatePollDeep),
        article = createSwatch(UsColorTokens.CreateArticleLight, UsColorTokens.CreateArticleDeep),
        live = EmberSwatch,
    ),
)

/**
 * The light palette — derived, since Momentum's Figma is dark-only. Brand
 * gradients and presence/status colours are shared with dark — the surfaces
 * and text ramp invert, the identity does not.
 */
internal val LightExtendedColors = DarkExtendedColors.copy(
    textPrimary = UsColorTokens.Light.TextPrimary,
    textSecondary = UsColorTokens.Light.TextSecondary,
    textTertiary = UsColorTokens.Light.TextTertiary,
    textMuted = UsColorTokens.Light.TextMuted,
    textDim = UsColorTokens.Light.TextDim,
    textDimmest = UsColorTokens.Light.TextDimmest,
    textGhost = UsColorTokens.Light.TextGhost,
    bgCard = UsColorTokens.Light.BgCard,
    bgCardHover = UsColorTokens.Light.BgCardHover,
    bgCardSolid = UsColorTokens.Light.FeedCard,
    bgCanvas = UsColorTokens.Light.FeedCanvas,
    textBody = UsColorTokens.Light.TextBody,
    borderSubtle = UsColorTokens.Light.BorderSubtle,
    borderMedium = UsColorTokens.Light.BorderMedium,
    glassBg = UsColorTokens.Light.GlassBg,
    glassBorder = UsColorTokens.Light.GlassBorder,
    brandChip = UsColorTokens.Light.BrandChip,
    onBrandChip = UsColorTokens.Light.OnBrandChip,
    unreadRow = UsColorTokens.Light.UnreadRow,
    bgRaised = UsColorTokens.Light.BgTertiary,
)
