package com.us.android.core.datastore

import com.us.android.core.model.ActionLinks
import kotlinx.coroutines.flow.first
import javax.inject.Inject

/**
 * The domains this person has already continued to from the Action button's
 * "You're leaving" page (2026-10-23): once per site per person, as the web
 * keeps its localStorage list. Most recent last, capped at
 * [ActionLinks.MAX_SEEN_DOMAINS]. A seam so the opener is tested on the JVM
 * with an in-memory store; the production one is [DataStoreLeavingDomainsStore].
 *
 * A store that cannot be read is an empty list: the page is shown again,
 * which is the safe way to be wrong.
 */
interface LeavingDomainsStore {
    suspend fun seen(): List<String>

    /** Remembers [domain] as the most recent; the oldest drop off past the cap. */
    suspend fun remember(domain: String)
}

class DataStoreLeavingDomainsStore @Inject constructor(
    private val dataStore: SettingsDataStore,
) : LeavingDomainsStore {
    override suspend fun seen(): List<String> = dataStore.leavingSeenDomains.first()

    override suspend fun remember(domain: String) = dataStore.updateLeavingSeenDomains { ActionLinks.remember(it, domain) }
}
