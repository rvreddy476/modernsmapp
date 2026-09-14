package com.us.android.feature.rider.money

import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryEarningsDto
import com.us.android.core.food.network.DeliveryOfferDto

/**
 * Which wire field every rider amount is displayed from: the integer `*_paise`
 * siblings food-service computes from NUMERIC (610a2acd), never the float-rupee
 * keys still on the wire. A missing paise field shows as "—" rather than
 * falling back to the float.
 */
object RiderMoney {

    /** What the rider earns for a job: `payout_paise`, else `delivery_partner_payout_paise`. */
    fun jobPay(assignment: DeliveryAssignmentDto): Paise? = assignment.payoutPaise ?: assignment.deliveryPartnerPayoutPaise

    fun offerPay(offer: DeliveryOfferDto): Paise? = offer.payoutPaise

    fun earnedToday(earnings: DeliveryEarningsDto): Paise? = earnings.earningsTodayPaise

    fun earnedTotal(earnings: DeliveryEarningsDto): Paise? = earnings.totalEarningsPaise

    fun text(amount: Paise?): String = amount?.let(RupeeFormat::format) ?: MISSING

    const val MISSING = "—"
}
