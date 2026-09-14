package com.us.android.feature.rider.money

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryEarningsDto
import com.us.android.core.food.network.ItemsDto
import com.us.android.feature.rider.RiderFixtures
import com.us.android.feature.rider.earnings.HistoryRow
import org.junit.Test

class RiderMoneyTest {

    private val earnings = RiderFixtures.success("delivery_earnings_get_200.json", DeliveryEarningsDto.serializer()).value
    private val history = RiderFixtures.success("delivery_history_get_200.json", ItemsDto.serializer(DeliveryAssignmentDto.serializer())).value.items
    private val accepted = RiderFixtures.success("delivery_assignment_current_get_200_accepted.json", DeliveryAssignmentDto.serializer()).value

    @Test
    fun `earnings display the paise totals`() {
        assertThat(RiderMoney.text(RiderMoney.earnedTotal(earnings))).isEqualTo("₹1,234.56")
        assertThat(RiderMoney.text(RiderMoney.earnedToday(earnings))).isEqualTo("₹51.60")
    }

    @Test
    fun `earnings ignore the float rupee keys`() {
        val diverged = earnings.copy(totalEarnings = Paise(1), earningsToday = Paise(1))

        assertThat(RiderMoney.text(RiderMoney.earnedTotal(diverged))).isEqualTo("₹1,234.56")
        assertThat(RiderMoney.text(RiderMoney.earnedToday(diverged))).isEqualTo("₹51.60")
        assertThat(RiderMoney.text(RiderMoney.earnedTotal(earnings.copy(totalEarningsPaise = null)))).isEqualTo("—")
    }

    @Test
    fun `job pay is payout_paise, never the float payout`() {
        val diverged = accepted.copy(deliveryPartnerPayout = Paise(9_999))

        assertThat(RiderMoney.text(RiderMoney.jobPay(diverged))).isEqualTo("₹23.20")
        assertThat(RiderMoney.jobPay(diverged.copy(payoutPaise = null))).isEqualTo(Paise(2_320))
        assertThat(RiderMoney.jobPay(diverged.copy(payoutPaise = null, deliveryPartnerPayoutPaise = null))).isNull()
    }

    @Test
    fun `history rows show the restaurant, the drop city and paise pay, never an address`() {
        val rows = history.map { HistoryRow.of(it.copy(deliveryPartnerPayout = Paise(1))) }

        assertThat(rows.map { it.pay }).containsExactly("₹23.20", "₹28.40").inOrder()
        assertThat(rows.map { it.restaurantName }).containsExactly("Test Kitchen", "Test Kitchen")
        assertThat(rows.map { it.dropCity }).containsExactly("Bengaluru", "Bengaluru")
        val shown = rows.flatMap { it.lines }.joinToString("\n")
        for (banned in listOf("1 Test Lane", "Near Test Park", "08040000000", "12.9716")) {
            assertThat(shown).doesNotContain(banned)
        }
    }
}
