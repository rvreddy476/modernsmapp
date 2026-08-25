package com.us.android.core.model

/**
 * The client half of the ordered-carousel contract — Creator Studio P0-A, E-2.1.
 *
 * ## WHY THE CLIENT VALIDATES AN ORDINAL THE SERVER SENT
 *
 * The server orders `post_media` in SQL and emits an explicit `position` on
 * every media object. That is not enough on its own: the value travels through
 * feed hydration's batch accumulation and through response caches, and either
 * could reorder a slice without anyone noticing. A carousel silently rendered
 * as A,C,B is not a crash — it is just wrong, forever, in a way no exception
 * ever reports.
 *
 * So the client checks what it was told, using **the same three-way rule as the
 * server**. Keeping the two identical is deliberate: if one side accepted what
 * the other rejected, the difference would only ever surface in production.
 *
 *  - **all present** → validate unique, contiguous `0..N-1`, then order by it;
 *  - **all absent**  → a payload cached before this field existed; array order
 *    is the ordinal;
 *  - **mixed**       → fail closed. No legitimate writer produces it.
 */
object CarouselOrdinals {

    /**
     * Sentinel for "this payload predates `position`".
     *
     * Emphatically not `0`. Defaulting a missing ordinal to zero would make
     * every item in a stale cached post claim to be the first one, and the
     * contiguity check below would then reject the whole post — turning a
     * cosmetic gap into a disappeared post.
     */
    const val ABSENT = -1

    /** The outcome of applying the rule to one post's media slice. */
    sealed interface Result<T> {
        data class Ordered<T>(val items: List<T>) : Result<T>

        /**
         * The slice cannot be trusted. The caller drops the post rather than
         * rendering pages in a made-up order.
         */
        data class Rejected<T>(val reason: String) : Result<T>
    }

    /**
     * @param items one post's media, in the order the payload delivered them.
     * @param positionOf the ordinal each item claims, or [ABSENT].
     */
    fun <T> order(items: List<T>, positionOf: (T) -> Int): Result<T> {
        if (items.isEmpty()) return Result.Ordered(emptyList())

        val positions = items.map(positionOf)
        val absent = positions.count { it == ABSENT }

        if (absent == items.size) {
            // Pre-contract payload: array order is all there is, and it is what
            // the server would have produced anyway.
            return Result.Ordered(items)
        }
        if (absent != 0) {
            return Result.Rejected(
                "mixed ordinals: $absent of ${items.size} media items carry no position",
            )
        }
        if (positions.toSet().size != positions.size) {
            return Result.Rejected("duplicate ordinals: $positions")
        }
        positions.firstOrNull { it < 0 || it >= items.size }?.let {
            return Result.Rejected("ordinal $it outside 0..${items.size - 1}")
        }
        return Result.Ordered(items.sortedBy(positionOf))
    }
}
