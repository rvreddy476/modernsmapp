package com.us.android.core.profile.data.dto

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement

@Serializable
data class PrivacySettingsDto(
    @SerialName("who_can_message") val whoCanMessage: String = "connections_only",
    @SerialName("who_can_send_connection_request") val whoCanSendConnectionRequest: String = "everyone",
    @SerialName("who_can_call") val whoCanCall: String = "connections_only",
    @SerialName("who_can_add_to_groups") val whoCanAddToGroups: String = "connections_only",
    @SerialName("who_can_see_online_status") val whoCanSeeOnlineStatus: String = "connections_only",
    @SerialName("who_can_see_read_receipts") val whoCanSeeReadReceipts: String = "connections_only",
    @SerialName("who_can_see_last_seen") val whoCanSeeLastSeen: String = "connections_only",
    @SerialName("who_can_see_profile_photo") val whoCanSeeProfilePhoto: String = "everyone",
    @SerialName("allow_phone_discovery") val allowPhoneDiscovery: Boolean = false,
    @SerialName("allow_contact_sync_match") val allowContactSyncMatch: Boolean = false,
    @SerialName("discoverable_by_phone_to_contacts") val discoverableByPhoneToContacts: Boolean = false,
    @SerialName("strict_privacy_mode") val strictPrivacyMode: Boolean = false,
    @SerialName("block_unknown_calls") val blockUnknownCalls: Boolean = false,
    @SerialName("auto_filter_abusive_content") val autoFilterAbusiveContent: Boolean = true,
    @SerialName("under_18_mode") val under18Mode: Boolean = false,
    @SerialName("tc_close_friends_posts") val trustedCircleCloseFriendsPosts: Boolean = true,
    @SerialName("tc_location_pings") val trustedCircleLocationPings: Boolean = true,
    @SerialName("tc_after_hours_posts") val trustedCircleAfterHoursPosts: Boolean = true,
    @SerialName("tc_audio_room_invite") val trustedCircleAudioRoomInvites: Boolean = true,
    // Production chat pass (directive §3.2).
    @SerialName("chat_availability") val chatAvailability: String = "enabled",
    @SerialName("send_typing_indicators") val sendTypingIndicators: Boolean = true,
    @SerialName("show_message_preview") val showMessagePreview: Boolean = true,
    // Launch-safety pass: private accounts and comment audiences.
    @SerialName("account_visibility") val accountVisibility: String = "public",
    @SerialName("allow_comments_from") val allowCommentsFrom: String = "everyone",
    @SerialName("privacy_version") val privacyVersion: Int = 0,
)

/** Full authoritative snapshot. No defaults: false and enum values must all be encoded. */
@Serializable
data class UpdatePrivacySettingsRequest(
    @SerialName("who_can_message") val whoCanMessage: String,
    @SerialName("who_can_send_connection_request") val whoCanSendConnectionRequest: String,
    @SerialName("who_can_call") val whoCanCall: String,
    @SerialName("who_can_add_to_groups") val whoCanAddToGroups: String,
    @SerialName("who_can_see_online_status") val whoCanSeeOnlineStatus: String,
    @SerialName("who_can_see_read_receipts") val whoCanSeeReadReceipts: String,
    @SerialName("who_can_see_last_seen") val whoCanSeeLastSeen: String,
    @SerialName("who_can_see_profile_photo") val whoCanSeeProfilePhoto: String,
    @SerialName("allow_phone_discovery") val allowPhoneDiscovery: Boolean,
    @SerialName("allow_contact_sync_match") val allowContactSyncMatch: Boolean,
    @SerialName("discoverable_by_phone_to_contacts") val discoverableByPhoneToContacts: Boolean,
    @SerialName("strict_privacy_mode") val strictPrivacyMode: Boolean,
    @SerialName("block_unknown_calls") val blockUnknownCalls: Boolean,
    @SerialName("auto_filter_abusive_content") val autoFilterAbusiveContent: Boolean,
    @SerialName("tc_close_friends_posts") val trustedCircleCloseFriendsPosts: Boolean,
    @SerialName("tc_location_pings") val trustedCircleLocationPings: Boolean,
    @SerialName("tc_after_hours_posts") val trustedCircleAfterHoursPosts: Boolean,
    @SerialName("tc_audio_room_invite") val trustedCircleAudioRoomInvites: Boolean,
    // Production chat pass (directive §3.2). Part of the full snapshot so a
    // hidden value can never be silently reset by an older write.
    @SerialName("chat_availability") val chatAvailability: String,
    @SerialName("send_typing_indicators") val sendTypingIndicators: Boolean,
    @SerialName("show_message_preview") val showMessagePreview: Boolean,
    // Launch-safety pass: `public` | `private`, and `everyone` | `friends`.
    @SerialName("account_visibility") val accountVisibility: String,
    @SerialName("allow_comments_from") val allowCommentsFrom: String,
)

/*
 * Notification preferences deliberately have NO typed DTO. The detailed
 * endpoint carries a global block plus an `inapp_<category>` / `push_<category>`
 * pair for every category, and the category list grows server-side. A flat
 * data class with forty booleans would have to be edited in three places per
 * category; [com.us.android.core.profile.data.NotificationPreferenceCodec]
 * reads and writes the JSON object by category key instead.
 */

@Serializable
data class AccountSummaryDto(
    @SerialName("user_id") val userId: String = "",
    val email: String = "",
    val phone: String = "",
    @SerialName("email_verified") val emailVerified: Boolean = false,
    @SerialName("phone_verified") val phoneVerified: Boolean = false,
    @SerialName("two_factor_enabled") val twoFactorEnabled: Boolean = false,
    @SerialName("account_type") val accountType: String = "",
    @SerialName("account_status") val accountStatus: String = "",
    @SerialName("age_verification") val ageVerification: String = "",
    @SerialName("deactivated_at") val deactivatedAt: String? = null,
    @SerialName("scheduled_purge_date") val scheduledPurgeDate: String? = null,
    @SerialName("last_login_at") val lastLoginAt: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class AccountSessionDto(
    val id: String = "",
    @SerialName("device_id") val deviceId: String = "",
    val platform: String = "",
    val ip: String = "",
    @SerialName("user_agent") val userAgent: String = "",
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class TrustedDeviceDto(
    val id: String = "",
    @SerialName("device_name") val name: String? = null,
    @SerialName("device_fingerprint") val fingerprint: String = "",
    @SerialName("last_used_at") val lastUsedAt: String = "",
    @SerialName("trusted_at") val trustedAt: String = "",
)

@Serializable
data class SecurityEventDto(
    val id: String = "",
    @SerialName("anomaly_type") val type: String = "",
    val ip: String = "",
    @SerialName("user_agent") val userAgent: String = "",
    @SerialName("device_id") val deviceId: String = "",
    @SerialName("country_code") val countryCode: String = "",
    @SerialName("risk_score") val riskScore: Int = 0,
    val challenged: Boolean = false,
    @SerialName("acknowledged_at") val acknowledgedAt: String? = null,
    @SerialName("occurred_at") val occurredAt: String = "",
)

@Serializable data class StatusDto(val status: String = "", val message: String = "")

@Serializable data class CodeRequest(val code: String)

@Serializable data class DisableTwoFactorRequest(val password: String, val code: String)

@Serializable
data class TwoFactorSetupDto(
    val secret: String = "",
    @SerialName("qr_code_url") val qrCodeUrl: String = "",
    @SerialName("recovery_codes") val recoveryCodes: List<String> = emptyList(),
)

@Serializable
data class AboutItemDto(
    @SerialName("item_id") val itemId: String = "",
    val section: String = "",
    val data: Map<String, JsonElement> = emptyMap(),
    val visibility: String = "private",
    @SerialName("sort_order") val sortOrder: Int = 0,
)

@Serializable
data class UpsertAboutItemRequest(
    @SerialName("item_id") val itemId: String?,
    val data: Map<String, JsonElement>,
    val visibility: String,
    @SerialName("sort_order") val sortOrder: Int,
)

@Serializable
data class ProfileLinkDto(
    val id: String = "",
    val title: String = "",
    val url: String = "",
    val category: String? = null,
    @SerialName("sort_order") val sortOrder: Int = 0,
    @SerialName("is_pinned") val pinned: Boolean = false,
    val visibility: String = "private",
)

@Serializable
data class SaveProfileLinkRequest(
    val title: String,
    val url: String,
    val category: String?,
    @SerialName("sort_order") val sortOrder: Int,
    @SerialName("is_pinned") val pinned: Boolean,
    val visibility: String,
)

@Serializable data class ChangeHandleRequest(val username: String)

// ── Manage account ────────────────────────────────────────────────────

/** The read-only `region` on `GET /v1/users/me/settings`. */
@Serializable
data class RegionDto(val region: String = "")

@Serializable
data class UpdateRegionRequest(@SerialName("country_code") val countryCode: String)

// ── Screen time / wellbeing (user-service) ────────────────────────────

@Serializable
data class WellbeingDto(
    /** Minutes per day; null means no limit. */
    @SerialName("daily_limit_mins") val dailyLimitMins: Int? = null,
    @SerialName("bedtime_start") val bedtimeStart: String? = null,
    @SerialName("bedtime_end") val bedtimeEnd: String? = null,
    @SerialName("focus_mode_enabled") val focusModeEnabled: Boolean = false,
    @SerialName("focus_mode_until") val focusModeUntil: String? = null,
    @SerialName("nudge_interval_mins") val nudgeIntervalMins: Int = 0,
    @SerialName("hide_like_counts") val hideLikeCounts: Boolean = false,
    @SerialName("detox_mode_until") val detoxModeUntil: String? = null,
    @SerialName("updated_at") val updatedAt: String = "",
)

/**
 * Full snapshot. The bedtime fields are [JsonElement] rather than `String?`
 * because the shared `Json` has `explicitNulls = false`: a Kotlin null would
 * be DROPPED from the body, and "sleep hours off" would never reach the
 * server. `JsonNull` is a value, so it is written as a literal `null`.
 */
@Serializable
data class UpdateWellbeingRequest(
    /** 0 switches the limit off. */
    @SerialName("daily_limit_mins") val dailyLimitMins: Int,
    @SerialName("bedtime_start") val bedtimeStart: JsonElement,
    @SerialName("bedtime_end") val bedtimeEnd: JsonElement,
    @SerialName("focus_mode_enabled") val focusModeEnabled: Boolean,
    @SerialName("nudge_interval_mins") val nudgeIntervalMins: Int,
    @SerialName("hide_like_counts") val hideLikeCounts: Boolean,
)

@Serializable
data class ScreenTimeReportRequest(
    val date: String,
    @SerialName("foreground_secs") val foregroundSecs: Long,
    val sessions: Int,
)

@Serializable
data class ScreenTimeDayDto(
    val date: String = "",
    val minutes: Int = 0,
    val sessions: Int = 0,
)

@Serializable
data class ScreenTimeWeekDto(
    val range: String = "week",
    val days: List<ScreenTimeDayDto> = emptyList(),
    @SerialName("today_minutes") val todayMinutes: Int = 0,
    @SerialName("daily_limit_mins") val dailyLimitMins: Int? = null,
)

// ── Content preferences ───────────────────────────────────────────────

@Serializable
data class KeywordFiltersDto(val keywords: List<String> = emptyList())

@Serializable
data class UpdateKeywordFiltersRequest(val keywords: List<String>)
