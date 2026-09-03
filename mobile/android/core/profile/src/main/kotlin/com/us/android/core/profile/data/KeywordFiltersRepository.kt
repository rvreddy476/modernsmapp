package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.common.result.onSuccess
import com.us.android.core.datastore.SettingsDataStore
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.profile.data.dto.UpdateKeywordFiltersRequest
import kotlinx.coroutines.flow.first
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The last server-confirmed keyword list, kept locally so the feed can apply
 * the filter as a client-side fallback before (or without) the server's own
 * filtering. An interface so the repository is testable without a DataStore.
 */
interface KeywordFiltersCache {
    suspend fun read(): List<String>
    suspend fun write(keywords: List<String>)
}

class DataStoreKeywordFiltersCache @Inject constructor(
    private val dataStore: SettingsDataStore,
) : KeywordFiltersCache {
    override suspend fun read(): List<String> = dataStore.keywordFilters.first()
    override suspend fun write(keywords: List<String>) = dataStore.setKeywordFilters(keywords)
}

/** `/v1/users/me/keyword-filters`: the list is replaced whole on every PUT. */
@Singleton
class KeywordFiltersRepository @Inject constructor(
    private val api: KeywordFiltersApi,
    private val cache: KeywordFiltersCache,
    private val errorMapper: ErrorMapper,
) {
    suspend fun keywords(): AppResult<List<String>> =
        apiCall(errorMapper) { api.keywordFilters() }
            .map { it.keywords }
            .onSuccess { cache.write(it) }

    suspend fun save(keywords: List<String>): AppResult<List<String>> =
        apiCall(errorMapper) { api.updateKeywordFilters(UpdateKeywordFiltersRequest(keywords)) }
            .map { it.keywords }
            .onSuccess { cache.write(it) }
}
