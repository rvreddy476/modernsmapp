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
 * The rows are fixed; the last one is the switch into MSeller, and WHICH
 * switch depends on whether the person already has a shop. The rule is here
 * rather than inside a composable because getting it wrong in either direction
 * is visible to the user: inviting an existing seller to "start selling", or
 * sending someone with no shop to a dashboard of nothing.
 */
class StoreProfileMenuTest {

    @Test
    fun `the fixed rows are always there, in order`() {
        for (presence in SellerPresence.entries) {
            assertThat(storeMenuRows(presence).take(SIX)).containsExactly(
                StoreMenuRow.ORDERS,
                StoreMenuRow.FAVOURITES,
                StoreMenuRow.ADDRESSES,
                StoreMenuRow.PAYMENTS,
                StoreMenuRow.PURCHASE_HISTORY,
                StoreMenuRow.SETTINGS,
            ).inOrder()
        }
    }

    @Test
    fun `someone with no shop is invited to start selling`() {
        val rows = storeMenuRows(SellerPresence.NONE)
        assertThat(rows.last()).isEqualTo(StoreMenuRow.START_SELLING)
        assertThat(rows.last().label).isEqualTo("Start selling")
        assertThat(sellingRowDetail(SellerPresence.NONE)).contains("Open a shop")
    }

    @Test
    fun `someone with a shop is offered the dashboard`() {
        val rows = storeMenuRows(SellerPresence.EXISTS)
        assertThat(rows.last()).isEqualTo(StoreMenuRow.SELLER_DASHBOARD)
        assertThat(rows.last().label).isEqualTo("Seller dashboard")
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
        assertThat(storeMenuRows(SellerPresence.UNKNOWN).last())
            .isEqualTo(StoreMenuRow.SELLER_DASHBOARD)
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
        const val SIX = 6
    }
}
