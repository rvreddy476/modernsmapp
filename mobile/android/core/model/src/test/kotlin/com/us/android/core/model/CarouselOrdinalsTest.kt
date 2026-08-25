package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * CS-LB-3P — the client half of the ordered-carousel contract.
 *
 * ## WHY THESE CASES AND NOT OTHERS
 *
 * The server sends an explicit ordinal on every media object and orders the
 * slice in SQL. This rule exists for what happens AFTER that: feed hydration
 * accumulates media out of a batch response, and a response cache sits in front
 * of both. Either could reorder a slice, and a carousel rendered A,C,B is not a
 * crash — it is silently the wrong post, forever.
 *
 * The three branches mirror the server's `normalizePostMediaPositions` exactly.
 * If the two ever disagreed, the difference would only show up in production.
 */
class CarouselOrdinalsTest {

    private data class Item(val id: String, val position: Int)

    private fun order(vararg items: Item) =
        CarouselOrdinals.order(items.toList()) { it.position }

    private fun ids(result: CarouselOrdinals.Result<Item>) =
        (result as CarouselOrdinals.Result.Ordered).items.map { it.id }

    /** The ordinary case: the ordinals are authoritative, even out of array order. */
    @Test
    fun `a fully ordinal-bearing slice is sorted by ordinal`() {
        val result = order(
            Item("b", 1),
            Item("c", 2),
            Item("a", 0),
        )

        assertThat(ids(result)).containsExactly("a", "b", "c").inOrder()
    }

    /**
     * The C,A,B proof.
     *
     * The author published pages in the order C,A,B. If the wire delivers them
     * in any other order, the ordinals must put them back — this is the exact
     * scenario the acceptance criterion names.
     */
    @Test
    fun `the author's C,A,B order survives a shuffled payload`() {
        val result = order(
            Item("A", 1),
            Item("B", 2),
            Item("C", 0),
        )

        assertThat(ids(result)).containsExactly("C", "A", "B").inOrder()
    }

    /**
     * A payload cached before `position` existed.
     *
     * Array order is all there is, and it is what the server would have
     * produced anyway, so it is used as-is rather than rejected.
     */
    @Test
    fun `a slice with no ordinals at all falls back to array order`() {
        val result = order(
            Item("a", CarouselOrdinals.ABSENT),
            Item("b", CarouselOrdinals.ABSENT),
        )

        assertThat(ids(result)).containsExactly("a", "b").inOrder()
    }

    /**
     * THE LOAD-BEARING ONE.
     *
     * No legitimate writer produces a half-numbered slice — a create
     * transaction writes every ordinal or none. Guessing would render a
     * carousel in an order the author never chose.
     */
    @Test
    fun `a mixed slice is rejected rather than guessed`() {
        val result = order(
            Item("a", 0),
            Item("b", CarouselOrdinals.ABSENT),
        )

        assertThat(result).isInstanceOf(CarouselOrdinals.Result.Rejected::class.java)
        assertThat((result as CarouselOrdinals.Result.Rejected).reason).contains("mixed")
    }

    /** Two pages cannot claim the same slot. */
    @Test
    fun `duplicate ordinals are rejected`() {
        val result = order(Item("a", 0), Item("b", 0))

        assertThat(result).isInstanceOf(CarouselOrdinals.Result.Rejected::class.java)
    }

    /**
     * Gap-free `0..N-1` is a create-time invariant. An ordinal outside it means
     * something upstream lost a page, and rendering the survivors in a made-up
     * order would hide that.
     */
    @Test
    fun `a non-contiguous ordinal is rejected`() {
        val result = order(Item("a", 0), Item("b", 7))

        assertThat(result).isInstanceOf(CarouselOrdinals.Result.Rejected::class.java)
    }

    /**
     * The reason ABSENT is -1 and not 0.
     *
     * If a missing ordinal defaulted to 0, every item in a stale cached post
     * would claim to be first; the duplicate check would then reject the slice
     * and the reader would lose the whole post's images. -1 routes it to the
     * fallback branch instead, where it renders correctly.
     */
    @Test
    fun `the absent sentinel is not zero`() {
        assertThat(CarouselOrdinals.ABSENT).isEqualTo(-1)

        val stale = order(
            Item("a", CarouselOrdinals.ABSENT),
            Item("b", CarouselOrdinals.ABSENT),
            Item("c", CarouselOrdinals.ABSENT),
        )

        assertThat(stale).isInstanceOf(CarouselOrdinals.Result.Ordered::class.java)
        assertThat(ids(stale)).hasSize(3)
    }

    @Test
    fun `an empty slice is ordinary, not an error`() {
        val result = CarouselOrdinals.order(emptyList<Item>()) { it.position }

        assertThat(ids(result)).isEmpty()
    }

    @Test
    fun `a single ordinal-bearing item passes through`() {
        assertThat(ids(order(Item("only", 0)))).containsExactly("only")
    }
}
