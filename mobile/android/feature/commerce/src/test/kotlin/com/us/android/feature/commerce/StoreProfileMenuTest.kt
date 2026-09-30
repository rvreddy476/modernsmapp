package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.commerce.profile.SellerPresence
import com.us.android.feature.commerce.profile.StoreMenuRow
import com.us.android.feature.commerce.profile.sellingRowDetail
import com.us.android.feature.commerce.profile.storeMenuRows
import org.junit.Test

/**
 * MStore's profile menu.
 *
 * The rows are in ASCENDING ALPHABETICAL order by label (founder,
 * 2026-09-30: every menu the viewer reads as a list), and WHICH selling row
 * appears depends on whether the person already has a shop. Both rules are
 * here rather than inside a composable because getting either wrong is
 * visible to the user: a menu whose order differs from every other menu, or
 * inviting an existing seller to "start selling".
 *
 * 2026-09-30: this test used to pin a hand-picked order (Orders, Favourites,
 * Addresses, ...). It was changed deliberately to the alphabetical rule.
 */
class StoreProfileMenuTest {

    @Test
    fun `the rows are in ascending alphabetical order by label`() {
        for (presence in SellerPresence.entries) {
            val labels = storeMenuRows(presence).map { it.label }
            assertThat(labels).isEqualTo(labels.sortedBy { it.lowercase() })
        }
    }

    @Test
    fun `the six fixed rows are always there`() {
        for (presence in SellerPresence.entries) {
            assertThat(storeMenuRows(presence)).containsAtLeast(
                StoreMenuRow.ADDRESSES,
                StoreMenuRow.FAVOURITES,
                StoreMenuRow.ORDERS,
                StoreMenuRow.PAYMENTS,
                StoreMenuRow.PURCHASE_HISTORY,
                StoreMenuRow.SETTINGS,
            )
            assertThat(storeMenuRows(presence)).hasSize(SEVEN)
        }
    }

    @Test
    fun `the exact order, for someone with a shop`() {
        assertThat(storeMenuRows(SellerPresence.EXISTS)).containsExactly(
            StoreMenuRow.ADDRESSES,
            StoreMenuRow.FAVOURITES,
            StoreMenuRow.ORDERS,
            StoreMenuRow.PAYMENTS,
            StoreMenuRow.PURCHASE_HISTORY,
            StoreMenuRow.SELLER_DASHBOARD,
            StoreMenuRow.SETTINGS,
        ).inOrder()
    }

    @Test
    fun `someone with no shop is invited to start selling`() {
        val rows = storeMenuRows(SellerPresence.NONE)
        assertThat(rows).contains(StoreMenuRow.START_SELLING)
        assertThat(rows).doesNotContain(StoreMenuRow.SELLER_DASHBOARD)
        assertThat(StoreMenuRow.START_SELLING.label).isEqualTo("Start selling")
        assertThat(sellingRowDetail(SellerPresence.NONE)).contains("Open a shop")
        // "Start selling" sorts after "Settings": it is the last row.
        assertThat(rows.last()).isEqualTo(StoreMenuRow.START_SELLING)
    }

    @Test
    fun `someone with a shop is offered the dashboard`() {
        val rows = storeMenuRows(SellerPresence.EXISTS)
        assertThat(rows).contains(StoreMenuRow.SELLER_DASHBOARD)
        assertThat(rows).doesNotContain(StoreMenuRow.START_SELLING)
        assertThat(StoreMenuRow.SELLER_DASHBOARD.label).isEqualTo("Seller dashboard")
    }

    /**
     * The whole reason [SellerPresence] has three values.
     *
     * A lookup that has not answered — still loading, or a timeout — must NOT
     * be read as "no shop". Telling an approved seller to start selling is the
     * version of this mistake they would notice, so the unknown case behaves
     * like the seller case and MSeller's hub shows the real state with Retry.
     */
    @Test
    fun `an unanswered lookup never claims the shop is missing`() {
        assertThat(storeMenuRows(SellerPresence.UNKNOWN)).contains(StoreMenuRow.SELLER_DASHBOARD)
        assertThat(storeMenuRows(SellerPresence.UNKNOWN)).doesNotContain(StoreMenuRow.START_SELLING)
    }

    @Test
    fun `both selling rows open the seller app`() {
        assertThat(StoreMenuRow.START_SELLING.opensSeller).isTrue()
        assertThat(StoreMenuRow.SELLER_DASHBOARD.opensSeller).isTrue()
        assertThat(StoreMenuRow.ORDERS.opensSeller).isFalse()
    }

    /** No row is a placeholder: every one has a label a person can read. */
    @Test
    fun `every row is labelled`() {
        for (row in StoreMenuRow.entries) {
            assertThat(row.label).isNotEmpty()
        }
    }

    private companion object {
        const val SEVEN = 7
    }
}
