package com.us.android.core.profile.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.core.profile.data.dto.AboutItemDto
import com.us.android.core.profile.data.dto.AccountSessionDto
import com.us.android.core.profile.data.dto.AccountSummaryDto
import com.us.android.core.profile.data.dto.ChangeHandleRequest
import com.us.android.core.profile.data.dto.CodeRequest
import com.us.android.core.profile.data.dto.DisableTwoFactorRequest
import com.us.android.core.profile.data.dto.KeywordFiltersDto
import com.us.android.core.profile.data.dto.OwnProfileDto
import com.us.android.core.profile.data.dto.PrivacySettingsDto
import com.us.android.core.profile.data.dto.ProfileLinkDto
import com.us.android.core.profile.data.dto.RegionDto
import com.us.android.core.profile.data.dto.SaveProfileLinkRequest
import com.us.android.core.profile.data.dto.ScreenTimeDayDto
import com.us.android.core.profile.data.dto.ScreenTimeReportRequest
import com.us.android.core.profile.data.dto.ScreenTimeWeekDto
import com.us.android.core.profile.data.dto.SecurityEventDto
import com.us.android.core.profile.data.dto.StatusDto
import com.us.android.core.profile.data.dto.TrustedDeviceDto
import com.us.android.core.profile.data.dto.TwoFactorSetupDto
import com.us.android.core.profile.data.dto.UpdateKeywordFiltersRequest
import com.us.android.core.profile.data.dto.UpdatePrivacySettingsRequest
import com.us.android.core.profile.data.dto.UpdateRegionRequest
import com.us.android.core.profile.data.dto.UpdateWellbeingRequest
import com.us.android.core.profile.data.dto.UpsertAboutItemRequest
import com.us.android.core.profile.data.dto.WellbeingDto
import kotlinx.serialization.json.JsonObject
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/** Privacy preferences owned by identity user-service. */
interface PrivacySettingsApi {
    @GET("v1/users/me/settings")
    suspend fun privacy(): ApiEnvelope<PrivacySettingsDto>

    @PUT("v1/users/me/settings")
    suspend fun updatePrivacy(
        @Body body: UpdatePrivacySettingsRequest,
    ): ApiEnvelope<PrivacySettingsDto>
}

/**
 * Notification delivery preferences owned by notification-service.
 *
 * Raw JSON objects on both sides: the category pairs are read and written by
 * key through [NotificationPreferenceCodec], so a new server-side category is
 * one enum entry here rather than three DTO fields.
 */
interface NotificationSettingsApi {
    @GET("v1/notifications/preferences/detailed")
    suspend fun notifications(): ApiEnvelope<JsonObject>

    @PUT("v1/notifications/preferences/detailed")
    suspend fun updateNotifications(@Body body: JsonObject): ApiEnvelope<JsonObject>
}

/** Region, owned by user-service. The identity facts come from [AccountSecurityApi]. */
interface ManageAccountApi {
    @GET("v1/users/me/settings")
    suspend fun region(): ApiEnvelope<RegionDto>

    @PUT("v1/users/me/region")
    suspend fun updateRegion(@Body body: UpdateRegionRequest): ApiEnvelope<RegionDto>
}

/** Screen-time controls and the usage ledger, owned by user-service. */
interface WellbeingApi {
    @GET("v1/users/me/wellbeing")
    suspend fun wellbeing(): ApiEnvelope<WellbeingDto>

    @PUT("v1/users/me/wellbeing")
    suspend fun updateWellbeing(@Body body: UpdateWellbeingRequest): ApiEnvelope<WellbeingDto>

    @POST("v1/users/me/screen-time")
    suspend fun reportScreenTime(@Body body: ScreenTimeReportRequest): ApiEnvelope<ScreenTimeDayDto>

    @GET("v1/users/me/screen-time")
    suspend fun screenTime(@Query("range") range: String): ApiEnvelope<ScreenTimeWeekDto>
}

/** Muted keywords, owned by user-service. */
interface KeywordFiltersApi {
    @GET("v1/users/me/keyword-filters")
    suspend fun keywordFilters(): ApiEnvelope<KeywordFiltersDto>

    @PUT("v1/users/me/keyword-filters")
    suspend fun updateKeywordFilters(@Body body: UpdateKeywordFiltersRequest): ApiEnvelope<KeywordFiltersDto>
}

/** Account identity, security events and 2FA owned by identity auth-service. */
interface AccountSecurityApi {
    @GET("v1/auth/me")
    suspend fun account(): ApiEnvelope<AccountSummaryDto>

    @GET("v1/auth/security/anomalies")
    suspend fun securityEvents(): ApiEnvelope<List<SecurityEventDto>>

    @POST("v1/auth/security/anomalies/{id}/ack")
    suspend fun acknowledgeEvent(@Path("id") id: String): ApiEnvelope<StatusDto>

    @POST("v1/auth/2fa/setup")
    suspend fun setupTwoFactor(): ApiEnvelope<TwoFactorSetupDto>

    @POST("v1/auth/2fa/verify-setup")
    suspend fun verifyTwoFactor(@Body body: CodeRequest): ApiEnvelope<StatusDto>

    @POST("v1/auth/2fa/disable")
    suspend fun disableTwoFactor(
        @Body body: DisableTwoFactorRequest,
    ): ApiEnvelope<StatusDto>
}

/** Session and trusted-device lifecycle owned by identity auth-service. */
interface DeviceSecurityApi {
    @GET("v1/auth/sessions")
    suspend fun sessions(): ApiEnvelope<List<AccountSessionDto>>

    @DELETE("v1/auth/sessions/{id}")
    suspend fun revokeSession(@Path("id") id: String): ApiEnvelope<StatusDto>

    @POST("v1/auth/logout-all")
    suspend fun logoutAll(): ApiEnvelope<StatusDto>

    @GET("v1/auth/trusted-devices")
    suspend fun trustedDevices(): ApiEnvelope<List<TrustedDeviceDto>>

    @DELETE("v1/auth/trusted-devices/{id}")
    suspend fun removeTrustedDevice(@Path("id") id: String): ApiEnvelope<StatusDto>
}

/** Rich profile details owned by identity profile-service. */
interface ProfileDetailsApi {
    @GET("v1/profiles/me/about")
    suspend fun about(): ApiEnvelope<List<AboutItemDto>>

    @PUT("v1/profiles/me/about/{section}")
    suspend fun saveAbout(
        @Path("section") section: String,
        @Body body: UpsertAboutItemRequest,
    ): ApiEnvelope<AboutItemDto>

    @DELETE("v1/profiles/me/about/{section}/{itemId}")
    suspend fun deleteAbout(
        @Path("section") section: String,
        @Path("itemId") itemId: String,
    ): ApiEnvelope<StatusDto>

    @GET("v1/profiles/me/profile-links")
    suspend fun links(): ApiEnvelope<List<ProfileLinkDto>>

    @POST("v1/profiles/me/profile-links")
    suspend fun createLink(@Body body: SaveProfileLinkRequest): ApiEnvelope<ProfileLinkDto>

    @PATCH("v1/profiles/me/profile-links/{id}")
    suspend fun updateLink(
        @Path("id") id: String,
        @Body body: SaveProfileLinkRequest,
    ): ApiEnvelope<ProfileLinkDto>

    @DELETE("v1/profiles/me/profile-links/{id}")
    suspend fun deleteLink(@Path("id") id: String): ApiEnvelope<StatusDto>

    @PUT("v1/profiles/me/handle")
    suspend fun changeHandle(@Body body: ChangeHandleRequest): ApiEnvelope<OwnProfileDto>
}
