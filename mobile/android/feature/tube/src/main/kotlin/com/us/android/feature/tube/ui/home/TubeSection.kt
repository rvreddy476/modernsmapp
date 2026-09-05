package com.us.android.feature.tube.ui.home

/**
 * What Tube home is made of, top to bottom (Momentum layout, 2026-09-05):
 * the channels strip, the featured carousel, "Continue watching", the chip
 * rail, then the two-column mosaic of ranked videos with the "Reels" panel
 * cut in after the first four cards. Every section but a [Video] spans
 * both columns of the staggered grid; a [Video] takes one.
 *
 * A strip, a carousel or a panel is present only when it has something in
 * it. [Video] carries the index into the ranked page, so the screen reads
 * the row through Paging — which is what asks for the next page — rather
 * than from a copied snapshot; the carousel reads the same first rows.
 */
sealed interface TubeSection {
    val key: String

    /** Whether the section spans both columns of the mosaic. */
    val fullSpan: Boolean get() = true

    /** Circular avatars of the channels the viewer follows, and the viewer's own. */
    data object Channels : TubeSection {
        override val key: String = "channels"
    }

    /** The paged carousel of the first ranked videos. [count] is how many pages it has. */
    data class Featured(val count: Int) : TubeSection {
        override val key: String = "featured"
    }

    data object ContinueWatching : TubeSection {
        override val key: String = "continue"
    }

    /** The chip rail — All, Following, the categories. */
    data object Chips : TubeSection {
        override val key: String = "chips"
    }

    /** A ranked video as one tile of the mosaic. */
    data class Video(val index: Int) : TubeSection {
        override val key: String get() = "video:$index"
        override val fullSpan: Boolean get() = false
    }

    /** The glass panel of reels. */
    data object Reels : TubeSection {
        override val key: String = "reels"
    }
}

/**
 * The section list for one state of the page. Pure: the counts are all it
 * needs, so the layout rule is a table test.
 *
 * With no ranked videos the strip, the shelf and the panel still show — a
 * viewer with nothing under a chip but a video half-watched should still
 * see it — and the screen decides separately whether the page reads as
 * empty. The Reels panel sits after the first [REELS_AFTER] tiles, or at
 * the end when there are fewer.
 */
fun tubeSections(
    videoCount: Int,
    hasChannels: Boolean,
    hasContinueWatching: Boolean,
    hasReels: Boolean,
): List<TubeSection> = buildList {
    if (hasChannels) add(TubeSection.Channels)
    if (videoCount > 0) add(TubeSection.Featured(minOf(videoCount, FEATURED_COUNT)))
    if (hasContinueWatching) add(TubeSection.ContinueWatching)
    add(TubeSection.Chips)
    for (index in 0 until minOf(videoCount, REELS_AFTER)) add(TubeSection.Video(index))
    if (hasReels) add(TubeSection.Reels)
    for (index in REELS_AFTER until videoCount) add(TubeSection.Video(index))
}

/** How many ranked videos the carousel pages through. */
const val FEATURED_COUNT = 5

/** How many mosaic tiles come before the Reels panel. */
const val REELS_AFTER = 4
