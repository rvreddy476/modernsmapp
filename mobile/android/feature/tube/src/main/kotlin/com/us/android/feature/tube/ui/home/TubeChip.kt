package com.us.android.feature.tube.ui.home

import com.us.android.core.feed.data.FeedCategory
import com.us.android.core.feed.data.VideoFeedQuery

/**
 * One pill on the rail under Tube's header: "All", "Subscriptions", then
 * the server's categories. Single-select; the selection IS the query.
 */
sealed interface TubeChip {
    val label: String

    /** Stable identity for the rail's keys and for restoring a selection. */
    val key: String

    data object All : TubeChip {
        override val label: String = "All"
        override val key: String = "all"
    }

    /**
     * Long videos from the channels the viewer SUBSCRIBED to (2026-09-12).
     * Was "Following": a subscribe is follow plus notify, so a followed
     * author who was never subscribed to is not a channel the viewer chose
     * to see here, and the chip says what it filters by. The key changed
     * with it; a stored "following" resolves to All, which is the fallback
     * for any key the rail no longer has.
     */
    data object Subscriptions : TubeChip {
        override val label: String = "Subscriptions"
        override val key: String = "subscriptions"
    }

    data class Category(val id: String, override val label: String) : TubeChip {
        override val key: String get() = "category:$id"
    }
}

/** The rail: the two fixed pills first, then the taxonomy in the server's order. */
fun tubeChips(categories: List<FeedCategory>): List<TubeChip> =
    listOf(TubeChip.All, TubeChip.Subscriptions) + categories.map { TubeChip.Category(it.id, it.label) }

/** The request a chip stands for. */
fun TubeChip.toQuery(): VideoFeedQuery = when (this) {
    TubeChip.All -> VideoFeedQuery.All
    TubeChip.Subscriptions -> VideoFeedQuery.Subscribed
    is TubeChip.Category -> VideoFeedQuery.Category(id)
}

/**
 * Whether a row under this chip reads as a suggestion in the "more" sheet.
 * Subscriptions is what the viewer asked for, so "Interested" makes no
 * sense there; everything else is the server's pick.
 */
fun TubeChip.isSuggested(): Boolean = this != TubeChip.Subscriptions

/** The chip for a stored key, or All when the key is unknown (a category that has since gone). */
fun List<TubeChip>.chipFor(key: String?): TubeChip = firstOrNull { it.key == key } ?: TubeChip.All
