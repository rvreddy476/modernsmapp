package com.us.android.feature.tube.data.hub

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.pagedApiCall
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.JsonElement
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The Creator Hub's reads, normalised (T-31). Read-only: nothing here edits,
 * deletes or replies — Reply happens in the shared comments sheet, through
 * the comments seam it already owns.
 */
@Singleton
class CreatorHubRepository @Inject constructor(
    private val api: CreatorHubApi,
    private val errorMapper: ErrorMapper,
) {

    /** The channel's totals and daily views for [period]. */
    @Suppress("TooGenericExceptionCaught") // mapped, exactly as apiCall does; cancellation rethrown
    suspend fun insights(period: HubPeriod): AppResult<CreatorInsights> = try {
        val body = api.creatorInsights(period.wire)
        AppResult.Success(normalizeCreatorInsights(unwrapInsightsBody(body), period))
    } catch (e: CancellationException) {
        throw e
    } catch (e: Throwable) {
        AppResult.Failure(errorMapper.map(e))
    }

    /** One page of the creator's own videos or reels, newest first. */
    suspend fun library(kind: HubLibraryKind, cursor: String? = null): AppResult<Paged<HubLibraryRow>> =
        pagedApiCall(errorMapper) { api.uploads(kind.wire, HUB_LIBRARY_PAGE, cursor) }
            .mapRows(::normalizeLibraryRow)

    /** One page of the creator's saved drafts. */
    suspend fun drafts(cursor: String? = null): AppResult<Paged<HubDraft>> =
        pagedApiCall(errorMapper) { api.drafts(draftsLimit(), cursor) }
            .mapRows(::normalizeDraft)

    /** One page of the newest comments on the creator's posts. */
    suspend fun inbox(cursor: String? = null): AppResult<Paged<HubInboxRow>> =
        pagedApiCall(errorMapper) {
            api.inbox(HubInboxQuery.STATUS, HubInboxQuery.CONTENT, HubInboxQuery.SORT, HubInboxQuery.LIMIT, cursor)
        }.mapRows(::normalizeInboxRow)

    private inline fun <R> AppResult<Paged<JsonElement>>.mapRows(normalize: (JsonElement) -> R?): AppResult<Paged<R>> =
        map { page -> Paged(page.items.mapNotNull(normalize), page.nextCursor) }
}
