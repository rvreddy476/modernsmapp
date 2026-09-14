package com.us.android.feature.rider.earnings

import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.feature.rider.money.RiderMoney

/**
 * One past delivery as the earnings list shows it: the restaurant's name, the
 * drop CITY from `drop_summary`, and the pay from paise. Never an address or a
 * phone, even though the history item carries the restaurant's.
 */
data class HistoryRow(
    val id: String,
    val restaurantName: String,
    val orderNumber: String,
    val createdAt: String,
    val status: String,
    val dropCity: String?,
    val pay: String,
) {
    /** Every value the row can put on screen. */
    val lines: List<String> get() = listOfNotNull(restaurantName, orderNumber, dropCity, pay)

    companion object {
        fun of(job: DeliveryAssignmentDto): HistoryRow = HistoryRow(
            id = job.id,
            restaurantName = job.restaurant?.name?.trim()?.takeIf { it.isNotEmpty() } ?: job.restaurantName,
            orderNumber = job.orderNumber,
            createdAt = job.createdAt,
            status = job.status,
            dropCity = job.dropSummary?.city?.trim()?.takeIf { it.isNotEmpty() },
            pay = RiderMoney.text(RiderMoney.jobPay(job)),
        )
    }
}
