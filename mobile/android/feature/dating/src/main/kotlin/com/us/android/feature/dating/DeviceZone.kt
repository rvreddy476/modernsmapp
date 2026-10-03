package com.us.android.feature.dating

import java.time.ZoneId
import javax.inject.Inject

/**
 * The device's time zone, as the screens that cut a "day" need it: daily picks
 * (mechanic M7) are chosen for the viewer's local day, and travel (M8) names
 * the day a trip ends. Open so a test can pin a zone.
 */
open class DeviceZone @Inject constructor() {

    open fun zone(): ZoneId = ZoneId.systemDefault()

    /** The IANA name, such as `Asia/Kolkata`. */
    fun id(): String = zone().id
}
