package com.us.android.feature.tube.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.credits.CreditRequest
import com.us.android.core.network.Paged

/**
 * What the credit requests screen reads and answers through (video credits,
 * 2026-10-23). A seam over `CreditsRepository` so the screen's ViewModel is
 * tested with a fake, the way the History page is.
 */
interface CreditRequestsSource {
    suspend fun requests(cursor: String?): AppResult<Paged<CreditRequest>>

    suspend fun accept(creditId: String): AppResult<Unit>

    suspend fun decline(creditId: String): AppResult<Unit>

    suspend fun removeMe(creditId: String): AppResult<Unit>
}
