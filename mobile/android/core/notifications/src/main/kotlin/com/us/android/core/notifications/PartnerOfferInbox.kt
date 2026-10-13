package com.us.android.core.notifications

import android.content.Context
import android.content.SharedPreferences
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.time.Instant

/** Process-local delivery hints. Live details and decisions always come from authenticated APIs. */
@Singleton
class PartnerOfferInbox @Inject constructor() {
    private val mutable = MutableStateFlow<List<Map<String, String>>>(emptyList())
    val offers = mutable.asStateFlow()
    @Volatile var visibleHost: Boolean = false
    private var prefs: SharedPreferences? = null
    private val mutableBusy = MutableStateFlow(false to false) // live, practice; never interchangeable
    val busy = mutableBusy.asStateFlow()
    private var generation = 0L

    @Synchronized fun attachStorage(context: Context) {
        if (prefs != null) return
        prefs = context.getSharedPreferences("partner_offer_availability", Context.MODE_PRIVATE)
        mutableBusy.value = (prefs?.getBoolean("live", false) == true) to (prefs?.getBoolean("practice", false) == true)
    }
    @Synchronized fun bindAccount(user: String) {
        val previous = prefs?.getString("account", null)
        if (previous != null && previous != user) { setBusy(false, false); clear() }
        prefs?.edit()?.putString("account", user)?.commit()
    }
    @Synchronized fun isBlocked(testOnly: Boolean): Boolean = if(testOnly) mutableBusy.value.second else mutableBusy.value.first
    @Synchronized fun checkpoint(): Long = generation
    @Synchronized fun accepted(testOnly: Boolean) {
        generation++
        setBusy(if(testOnly) mutableBusy.value.first else true, if(testOnly) true else mutableBusy.value.second)
        mutable.value = mutable.value.filter { (it["test_only"] == "true") != testOnly }
    }
    /** Only a successful authenticated read begun after acceptance may resume live offers. */
    @Synchronized fun reconcileLive(isBusy: Boolean, checkpoint: Long) {
        if (checkpoint != generation) return
        setBusy(isBusy, mutableBusy.value.second)
        if(isBusy) mutable.value = mutable.value.filter { it["test_only"] == "true" }
    }
    @Synchronized fun resetPractice() {
        mutable.value = mutable.value.filter { it["test_only"] != "true" }
        setBusy(mutableBusy.value.first, false)
    }
    private fun setBusy(live: Boolean, practice: Boolean) {
        if(mutableBusy.value == (live to practice)) return
        mutableBusy.value = live to practice
        prefs?.edit()?.putBoolean("live",live)?.putBoolean("practice",practice)?.commit()
    }

    @Synchronized fun receive(data: Map<String, String>, now: Instant = Instant.now()) {
        if (isBlocked(data["test_only"] == "true")) return
        val offer = DoorstepOfferNotification.evaluate(data, now)
        val id = when (offer) {
            is DoorstepOfferNotification.Open -> offer.id
            is DoorstepOfferNotification.Expired -> offer.id
            else -> return
        }
        val current = mutable.value.filter { row ->
            val old = DoorstepOfferNotification.evaluate(row, now)
            old is DoorstepOfferNotification.Open && old.id != id
        }
        mutable.value = if (offer is DoorstepOfferNotification.Open) (current + data.toMap()).takeLast(20) else current
    }
    @Synchronized fun remove(id: String) { mutable.value = mutable.value.filter { (it["offer_id"] ?: it["entity_id"]) != id } }
    @Synchronized fun clear() { mutable.value = emptyList() }
}
