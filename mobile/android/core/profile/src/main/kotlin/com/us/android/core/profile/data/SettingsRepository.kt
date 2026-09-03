package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.profile.data.dto.AboutItemDto
import com.us.android.core.profile.data.dto.AccountSessionDto
import com.us.android.core.profile.data.dto.AccountSummaryDto
import com.us.android.core.profile.data.dto.ChangeHandleRequest
import com.us.android.core.profile.data.dto.CodeRequest
import com.us.android.core.profile.data.dto.DisableTwoFactorRequest
import com.us.android.core.profile.data.dto.PrivacySettingsDto
import com.us.android.core.profile.data.dto.ProfileLinkDto
import com.us.android.core.profile.data.dto.SaveProfileLinkRequest
import com.us.android.core.profile.data.dto.SecurityEventDto
import com.us.android.core.profile.data.dto.TrustedDeviceDto
import com.us.android.core.profile.data.dto.TwoFactorSetupDto
import com.us.android.core.profile.data.dto.UpdatePrivacySettingsRequest
import com.us.android.core.profile.data.dto.UpsertAboutItemRequest
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import javax.inject.Inject
import javax.inject.Singleton

@Singleton
class PrivacySettingsRepository @Inject constructor(
    private val api: PrivacySettingsApi,
    private val errorMapper: ErrorMapper,
) {
    suspend fun get(): AppResult<PrivacySettings> =
        apiCall(errorMapper) { api.privacy() }.map { it.toDomain() }

    suspend fun save(value: PrivacySettings): AppResult<PrivacySettings> =
        apiCall(errorMapper) { api.updatePrivacy(value.toRequest()) }.map { it.toDomain() }
}

@Singleton
class NotificationSettingsRepository @Inject constructor(
    private val api: NotificationSettingsApi,
    private val errorMapper: ErrorMapper,
) {
    suspend fun get(): AppResult<NotificationSettings> =
        apiCall(errorMapper) { api.notifications() }.map(NotificationPreferenceCodec::decode)

    /** Full snapshot of every known key; the server's echo is what comes back. */
    suspend fun save(value: NotificationSettings): AppResult<NotificationSettings> =
        apiCall(errorMapper) { api.updateNotifications(NotificationPreferenceCodec.encode(value)) }
            .map(NotificationPreferenceCodec::decode)
}

@Singleton
class SecuritySettingsRepository @Inject constructor(
    private val accountApi: AccountSecurityApi,
    private val deviceApi: DeviceSecurityApi,
    private val errorMapper: ErrorMapper,
) {
    suspend fun account(): AppResult<AccountSummary> =
        apiCall(errorMapper) { accountApi.account() }.map { it.toDomain() }

    suspend fun sessions(): AppResult<List<AccountSession>> =
        listApiCall(errorMapper) { deviceApi.sessions() }.map { rows -> rows.map { it.toDomain() } }

    suspend fun revokeSession(id: String): AppResult<Unit> =
        apiCall(errorMapper) { deviceApi.revokeSession(id) }.map { }

    suspend fun logoutAll(): AppResult<Unit> = apiCall(errorMapper) { deviceApi.logoutAll() }.map { }

    suspend fun trustedDevices(): AppResult<List<TrustedDevice>> =
        listApiCall(errorMapper) {
            deviceApi.trustedDevices()
        }.map { rows -> rows.map { it.toDomain() } }

    suspend fun removeTrustedDevice(id: String): AppResult<Unit> =
        apiCall(errorMapper) { deviceApi.removeTrustedDevice(id) }.map { }

    suspend fun securityEvents(): AppResult<List<SecurityEvent>> =
        listApiCall(errorMapper) {
            accountApi.securityEvents()
        }.map { rows -> rows.map { it.toDomain() } }

    suspend fun acknowledgeEvent(id: String): AppResult<Unit> =
        apiCall(errorMapper) { accountApi.acknowledgeEvent(id) }.map { }

    suspend fun setupTwoFactor(): AppResult<TwoFactorSetup> =
        apiCall(errorMapper) { accountApi.setupTwoFactor() }.map { it.toDomain() }

    suspend fun verifyTwoFactor(code: String): AppResult<Unit> =
        apiCall(errorMapper) { accountApi.verifyTwoFactor(CodeRequest(code)) }.map { }

    suspend fun disableTwoFactor(password: String, code: String): AppResult<Unit> =
        apiCall(errorMapper) {
            accountApi.disableTwoFactor(DisableTwoFactorRequest(password, code))
        }.map { }
}

@Singleton
class ProfileDetailsRepository @Inject constructor(
    private val api: ProfileDetailsApi,
    private val errorMapper: ErrorMapper,
) {
    suspend fun about(): AppResult<List<ProfileAboutItem>> =
        listApiCall(errorMapper) { api.about() }.map { rows -> rows.map { it.toDomain() } }

    suspend fun saveAbout(value: ProfileAboutItem): AppResult<ProfileAboutItem> =
        apiCall(errorMapper) { api.saveAbout(value.section, value.toRequest()) }.map { it.toDomain() }

    suspend fun deleteAbout(value: ProfileAboutItem): AppResult<Unit> =
        apiCall(errorMapper) { api.deleteAbout(value.section, value.itemId) }.map { }

    suspend fun links(): AppResult<List<ProfileLink>> =
        listApiCall(errorMapper) { api.links() }.map { rows -> rows.map { it.toDomain() } }

    suspend fun saveLink(value: ProfileLink): AppResult<ProfileLink> = apiCall(errorMapper) {
        if (value.id.isBlank()) {
            api.createLink(value.toRequest())
        } else {
            api.updateLink(value.id, value.toRequest())
        }
    }.map { it.toDomain() }

    suspend fun deleteLink(value: ProfileLink): AppResult<Unit> =
        apiCall(errorMapper) { api.deleteLink(value.id) }.map { }

    suspend fun changeHandle(username: String): AppResult<Unit> =
        apiCall(errorMapper) { api.changeHandle(ChangeHandleRequest(username.trim())) }.map { }
}

private fun PrivacySettingsDto.toDomain() = PrivacySettings(
    whoCanMessage,
    whoCanSendConnectionRequest,
    whoCanCall,
    whoCanAddToGroups,
    whoCanSeeOnlineStatus,
    whoCanSeeReadReceipts,
    whoCanSeeLastSeen,
    whoCanSeeProfilePhoto,
    allowPhoneDiscovery,
    allowContactSyncMatch,
    discoverableByPhoneToContacts,
    strictPrivacyMode,
    blockUnknownCalls,
    autoFilterAbusiveContent,
    under18Mode,
    trustedCircleCloseFriendsPosts,
    trustedCircleLocationPings,
    trustedCircleAfterHoursPosts,
    trustedCircleAudioRoomInvites,
    chatAvailability,
    sendTypingIndicators,
    showMessagePreview,
    accountVisibility,
    allowCommentsFrom,
    privacyVersion,
)

private fun PrivacySettings.toRequest() = UpdatePrivacySettingsRequest(
    whoCanMessage,
    whoCanSendConnectionRequest,
    whoCanCall,
    whoCanAddToGroups,
    whoCanSeeOnlineStatus,
    whoCanSeeReadReceipts,
    whoCanSeeLastSeen,
    whoCanSeeProfilePhoto,
    allowPhoneDiscovery,
    allowContactSyncMatch,
    discoverableByPhoneToContacts,
    strictPrivacyMode,
    blockUnknownCalls,
    autoFilterAbusiveContent,
    trustedCircleCloseFriendsPosts,
    trustedCircleLocationPings,
    trustedCircleAfterHoursPosts,
    trustedCircleAudioRoomInvites,
    chatAvailability,
    sendTypingIndicators,
    showMessagePreview,
    accountVisibility,
    allowCommentsFrom,
)

/** Shared with [ManageAccountRepository], which reads the same `/v1/auth/me`. */
internal fun AccountSummaryDto.toAccountSummary() = AccountSummary(
    userId,
    email,
    phone,
    emailVerified,
    phoneVerified,
    twoFactorEnabled,
    accountType,
    accountStatus,
    ageVerification,
    lastLoginAt,
    createdAt,
    deactivatedAt = deactivatedAt?.takeIf { it.isNotBlank() },
    scheduledPurgeDate = scheduledPurgeDate?.takeIf { it.isNotBlank() },
)

private fun AccountSummaryDto.toDomain() = toAccountSummary()

private fun AccountSessionDto.toDomain() = AccountSession(
    id,
    deviceId,
    platform,
    ip,
    userAgent,
    createdAt,
    expiresAt,
)

private fun TrustedDeviceDto.toDomain() = TrustedDevice(
    id,
    name.orEmpty(),
    fingerprint,
    lastUsedAt,
    trustedAt,
)

private fun SecurityEventDto.toDomain() = SecurityEvent(
    id,
    type,
    ip,
    userAgent,
    deviceId,
    countryCode,
    riskScore,
    challenged,
    acknowledgedAt != null,
    occurredAt,
)

private fun TwoFactorSetupDto.toDomain() = TwoFactorSetup(secret, qrCodeUrl, recoveryCodes)

private fun AboutItemDto.toDomain() = ProfileAboutItem(
    itemId = itemId,
    section = section,
    title = (data["title"] as? JsonPrimitive)?.contentOrNull.orEmpty(),
    subtitle = (data["subtitle"] as? JsonPrimitive)?.contentOrNull.orEmpty(),
    detail = (data["detail"] as? JsonPrimitive)?.contentOrNull.orEmpty(),
    visibility = visibility,
    sortOrder = sortOrder,
)

private fun ProfileAboutItem.toRequest() = UpsertAboutItemRequest(
    itemId = itemId.ifBlank { null },
    data = mapOf(
        "title" to JsonPrimitive(title),
        "subtitle" to JsonPrimitive(subtitle),
        "detail" to JsonPrimitive(detail),
    ),
    visibility = visibility,
    sortOrder = sortOrder,
)

private fun ProfileLinkDto.toDomain() = ProfileLink(
    id,
    title,
    url,
    category.orEmpty(),
    visibility,
    pinned,
    sortOrder,
)

private fun ProfileLink.toRequest() = SaveProfileLinkRequest(
    title,
    url,
    category.ifBlank { null },
    sortOrder,
    pinned,
    visibility,
)
