package com.us.android.feature.tube.ui.home

import com.us.android.core.feed.data.FeedCategory
import com.us.android.core.feed.data.VideoFeedQuery

/**
 * One pill on the rail under Tube's header: "All", "Following", then the
 * server's categories. Single-select; the selection IS the query.
 */
sealed interface TubeChip {
    val label: String

    /** Stable identity for the rail's keys and for restoring a selection. */
    val key: String

    data object All : TubeChip {
        override val label: String = "All"
        override val key: String = "all"
    }

    data object Following : TubeChip {
        override val label: String = "Following"
        override val key: String = "following"
    }

    data class Category(val id: String, override val label: String) : TubeChip {
        override val key: String get() = "category:$id"
    }
}

/** The rail: the two fixed pills first, then the taxonomy in the server's order. */
fun tubeChips(categories: List<FeedCategory>): List<TubeChip> =
    listOf(TubeChip.All, TubeChip.Following) + categories.map { TubeChip.Category(it.id, it.label) }

/** The request a chip stands for. */
fun TubeChip.toQuery(): VideoFeedQuery = when (this) {
    TubeChip.All -> VideoFeedQuery.All
    TubeChip.Following -> VideoFeedQuery.Following
    is TubeChip.Category -> VideoFeedQuery.Category(id)
}

/**
 * Whether a row under this chip reads as a suggestion in the "more" sheet.
 * Following is what the viewer asked for — "Interested" makes no sense
 * there; everything else is the server's pick.
 */
fun TubeChip.isSuggested(): Boolean = this != TubeChip.Following

/** The chip for a stored key, or All when the key is unknown (a category that has since gone). */
fun List<TubeChip>.chipFor(key: String?): TubeChip = firstOrNull { it.key == key } ?: TubeChip.All
