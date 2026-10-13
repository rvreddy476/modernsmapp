package com.us.android.feature.feast.tracking

import java.time.Instant

internal fun freshRider(position: RiderPosition?, now: Instant): RiderPosition? = position?.takeIf {
    val age = java.time.Duration.between(it.recordedAt, now).toMillis()
    age in 0..120_000 && it.latitude.isFinite() && it.longitude.isFinite() && it.latitude in -90.0..90.0 && it.longitude in -180.0..180.0
}
