package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.profile.data.dto.UpdateRegionRequest
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The "Manage account" page: identity facts from auth-service and the
 * region from user-service. Two owners, one screen, so one repository.
 */
@Singleton
class ManageAccountRepository @Inject constructor(
    private val accountApi: AccountSecurityApi,
    private val regionApi: ManageAccountApi,
    private val errorMapper: ErrorMapper,
) {
    suspend fun account(): AppResult<AccountSummary> =
        apiCall(errorMapper) { accountApi.account() }.map { it.toAccountSummary() }

    /** ISO 3166-1 alpha-2, upper case, or blank when the server has none. */
    suspend fun region(): AppResult<String> =
        apiCall(errorMapper) { regionApi.region() }.map { it.region.uppercase() }

    suspend fun updateRegion(countryCode: String): AppResult<String> =
        apiCall(errorMapper) {
            regionApi.updateRegion(UpdateRegionRequest(countryCode.uppercase()))
        }.map { it.region.uppercase() }
}
