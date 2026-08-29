package com.us.android.feature.mopedu.rider

/**
 * MopeduFeatureGate controls the availability of the Mopedu mobility slice.
 *
 * PRODUCTION SAFETY: Defaults to FALSE. It must remain disabled in production builds
 * until all 22 frozen launch criteria have been independently proven and accepted.
 */
object MopeduFeatureGate {
    @Volatile
    var isEnabled: Boolean = false
}
