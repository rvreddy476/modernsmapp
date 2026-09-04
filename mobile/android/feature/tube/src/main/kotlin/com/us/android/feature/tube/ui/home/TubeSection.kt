package com.us.android.feature.tube.ui.home

/**
 * What Tube home's list is made of, top to bottom (founder, 2026-09-05,
 * from YouTube's home): the viewer's own pending or just-posted video if
 * there is one, the first ranked video drawn large, the "Continue watching"
 * shelf, one more video, the "Shorts" shelf, then the rest of the ranked
 * videos as cards. A shelf is present only when it has something in it.
 *
 * [Hero] and [Video] carry the index into the ranked page, so the screen
 * reads the row through Paging — which is what asks for the next page —
 * rather than from a copied snapshot.
 */
sealed interface TubeSection {
    val key: String

    /** The pending card or the live post, above everything. */
    data object Head : TubeSection {
        override val key: String = "head"
    }

    /** The first ranked video, large. */
    data class Hero(val index: Int) : TubeSection {
        override val key: String get() = "hero:$index"
    }

    data object ContinueWatching : TubeSection {
        override val key: String = "continue"
    }

    data object Shorts : TubeSection {
        override val key: String = "shorts"
    }

    /** A ranked video as a full-width card. */
    data class Video(val index: Int) : TubeSection {
        override val key: String get() = "video:$index"
    }
}

/**
 * The section list for one state of the page. Pure: the counts are all it
 * needs, so the layout rule is a table test.
 *
 * With no ranked videos the shelves still show — a viewer with nothing in
 * the ranked feed but a video half-watched should still see it — and the
 * screen decides separately whether the whole page reads as empty.
 */
fun tubeSections(
    videoCount: Int,
    hasHead: Boolean,
    hasContinueWatching: Boolean,
    hasShorts: Boolean,
): List<TubeSection> = buildList {
    if (hasHead) add(TubeSection.Head)
    if (videoCount > 0) add(TubeSection.Hero(0))
    if (hasContinueWatching) add(TubeSection.ContinueWatching)
    if (videoCount > 1) add(TubeSection.Video(1))
    if (hasShorts) add(TubeSection.Shorts)
    for (index in 2 until videoCount) add(TubeSection.Video(index))
}
