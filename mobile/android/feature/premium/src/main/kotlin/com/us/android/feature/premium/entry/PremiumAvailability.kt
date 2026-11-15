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
 * Which ways into Premium may be drawn.
 *
 * [settingsRow] — Settings › Momentum Premium. [canBuy] — Echo's "Get
 * Premium" and the screen's passes.
 */
data class PremiumEntry(val settingsRow: Boolean = false, val canBuy: Boolean = false) {
    companion object {
        val Hidden = PremiumEntry()
    }
}

/**
 * Whether a way into Premium may be drawn, fail closed.
 *
 * Selling ([PremiumEntry.canBuy]): the app's [PremiumPurchaseSwitch] is on AND
 * `GET premium/plans` answered 200 — a 503 `PREMIUM_NOT_ENABLED` (the
 * server's switch), any other error, or no answer yet keeps it hidden.
 *
 * The Settings row ([PremiumEntry.settingsRow]): whenever selling is shown,
 * and also with the app's switch off for a viewer who already holds Premium
 * or has a tax document (15 Nov 2026) — someone who bought on the web must
 * reach their invoices on Android while Android does not sell. With the
 * switch off only `premium/me` and `premium/invoices` are asked; `plans` and
 * `checkout` never are.
 */
@Singleton
class PremiumAvailability @Inject constructor(
    private val switch: PremiumPurchaseSwitch,
    private val repository: PremiumRepository,
) {
    private val _entry = MutableStateFlow(PremiumEntry.Hidden)
    val entry: StateFlow<PremiumEntry> = _entry.asStateFlow()

    /** Asks the server again (sign-in, and each time a way in is about to be drawn). */
    suspend fun refresh() {
        if (switch.enabled && repository.plans() is AppResult.Success) {
            _entry.value = PremiumEntry(settingsRow = true, canBuy = true)
            return
        }
        _entry.value = PremiumEntry(settingsRow = !switch.enabled && ownsSomething(), canBuy = false)
    }

    /** Signed out: hide every way in until the next viewer's read. */
    fun clear() {
        _entry.value = PremiumEntry.Hidden
    }

    /** A live or grace pass, or at least one invoice or credit note. A failed read counts as nothing. */
    private suspend fun ownsSomething(): Boolean {
        val me = repository.me()
        if (me is AppResult.Success && me.data.active) return true
        if (me is AppResult.Failure) return false
        val invoices = repository.invoices()
        return invoices is AppResult.Success && invoices.data.isNotEmpty()
    }
}
