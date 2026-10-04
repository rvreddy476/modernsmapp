package com.us.android.feature.doorstep.ui

import com.us.android.feature.doorstep.domain.BookingStatus

/** How a booking status is coloured: progress is the accent, done is success, off-ramps are danger. */
fun BookingStatus.tone(): Tone = when (this) {
    BookingStatus.COMPLETED -> Tone.Positive
    BookingStatus.CANCELLED, BookingStatus.EXPIRED, BookingStatus.CUSTOMER_NO_SHOW, BookingStatus.PRO_NO_SHOW -> Tone.Danger
    BookingStatus.PENDING_PAYMENT, BookingStatus.AWAITING_EXTRAS_PAYMENT, BookingStatus.PRO_UNAVAILABLE -> Tone.Warning
    BookingStatus.UNKNOWN -> Tone.Neutral
    else -> Tone.Accent
}
