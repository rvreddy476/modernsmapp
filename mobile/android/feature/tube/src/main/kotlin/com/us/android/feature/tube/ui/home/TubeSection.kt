package com.us.android.feature.tube.ui.home

/**
 * What Tube home is made of, top to bottom (founder, 2026-09-05: "long
 * videos, full width of the screen, like YouTube"; the chips "at the top
 * only, filter like YouTube"): the channels strip, the chip rail — which
 * sticks under the header once it reaches it — the first [LEAD_COUNT]
 * ranked videos full width, then "Continue watching", then the "Reels"
 * panel, then the rest of the ranked videos full width, paged. Everything
 * is one column; the featured carousel that used to page sideways is gone
 * ("remove that rail with horizontal scrolling, keep vertical only").
 *
 * A strip, a shelf or a panel is present only when it has something in
 * it. [Video] carries the index into the ranked page, so the screen reads
 * the row through Paging — which is what asks for the next page — rather
 * than from a copied snapshot.
 */
sealed interface TubeSection {
    val key: String

    /** Circular avatars of the channels the viewer follows, and the viewer's own. */
    data object Channels : TubeSection {
        override val key: String = "channels"
    }

    /** The chip rail — All, Following, the categories. Sticky. */
    data object Chips : TubeSection {
        override val key: String = "chips"
    }

    /** A ranked video as one full-width card. */
    data class Video(val index: Int) : TubeSection {
        override val key: String get() = "video:$index"
    }

    data object ContinueWatching : TubeSection {
        override val key: String = "continue"
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
 * empty. The chips are always there, and always above the first video, so
 * the filter is where the eye lands. The shelf and the panel come after
 * the first [LEAD_COUNT] videos, or straight after the chips when there
 * are fewer.
 */
fun tubeSections(
    videoCount: Int,
    hasChannels: Boolean,
    hasContinueWatching: Boolean,
    hasReels: Boolean,
): List<TubeSection> = buildList {
    if (hasChannels) add(TubeSection.Channels)
    add(TubeSection.Chips)
    for (index in 0 until minOf(videoCount, LEAD_COUNT)) add(TubeSection.Video(index))
    if (hasContinueWatching) add(TubeSection.ContinueWatching)
    if (hasReels) add(TubeSection.Reels)
    for (index in LEAD_COUNT until videoCount) add(TubeSection.Video(index))
}

/** How many ranked videos lead the page before the shelf and the panel. */
const val LEAD_COUNT = 2
