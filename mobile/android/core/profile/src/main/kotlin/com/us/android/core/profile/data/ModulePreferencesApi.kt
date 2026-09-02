package com.us.android.core.profile.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.core.profile.data.dto.ModulePreferencesDto
import com.us.android.core.profile.data.dto.UpdateModulePreferencesRequest
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.PUT

/** Module choices and home page, owned by identity user-service. */
interface ModulePreferencesApi {
    @GET("v1/users/me/modules")
    suspend fun modules(): ApiEnvelope<ModulePreferencesDto>

    @PUT("v1/users/me/modules")
    suspend fun updateModules(
        @Body body: UpdateModulePreferencesRequest,
    ): ApiEnvelope<ModulePreferencesDto>
}
