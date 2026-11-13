package com.us.android.feature.premium.entry

import com.us.android.core.common.result.AppResult
import com.us.android.feature.premium.PremiumPurchaseSwitch
import com.us.android.feature.premium.data.PremiumRepository
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Whether a way into Premium may be drawn: the Settings row "Momentum
 * Premium" and the "Get Premium" button on Echo's premium sheet.
 *
 * Both conditions, fail closed:
 *  1. the app's [PremiumPurchaseSwitch] is on — off, nothing is asked of the
 *     server at all;
 *  2. `GET premium/plans` answered 200 — a 503 `PREMIUM_NOT_ENABLED` (the
 *     server's switch), any other error, or no answer yet keeps it hidden.
 */
@Singleton
class PremiumAvailability @Inject constructor(
    private val switch: PremiumPurchaseSwitch,
    private val repository: PremiumRepository,
) {
    private val _shown = MutableStateFlow(false)
    val shown: StateFlow<Boolean> = _shown.asStateFlow()

    /** Asks the server again (sign-in, and each time a way in is about to be drawn). */
    suspend fun refresh() {
        if (!switch.enabled) {
            _shown.value = false
            return
        }
        _shown.value = repository.plans() is AppResult.Success
    }

    /** Signed out: hide every way in until the next viewer's read. */
    fun clear() {
        _shown.value = false
    }
}
