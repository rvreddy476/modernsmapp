package com.us.android.feature.chat.ui.home

/**
 * The two bookkeeping rules of the Suggestions tab, kept pure so a test
 * pins them:
 *
 *  - an IMPRESSION is posted once per shown batch. A batch is identified by
 *    the ids it contains, in order; the same batch re-shown on a tab switch
 *    or a recomposition posts nothing, a refreshed batch posts again;
 *  - a DISMISSED id leaves the list and stays out for the session, even if
 *    the next refresh returns it — the engine learns from the action, but
 *    the row must not come back while the user watches.
 */
internal class SuggestionsTracker {

    private val postedBatches = mutableSetOf<String>()
    private val dismissed = mutableSetOf<String>()

    /** True the FIRST time this exact batch is seen; false on every repeat. */
    fun shouldPostImpression(kind: String, ids: List<String>): Boolean {
        if (ids.isEmpty()) return false
        return postedBatches.add(kind + ":" + ids.joinToString(","))
    }

    fun dismiss(id: String) {
        dismissed += id
    }

    fun isDismissed(id: String): Boolean = id in dismissed

    /** The rows the tab may show: everything not dismissed, in the engine's order. */
    fun <T> visible(items: List<T>, id: (T) -> String): List<T> = items.filterNot { isDismissed(id(it)) }
}
