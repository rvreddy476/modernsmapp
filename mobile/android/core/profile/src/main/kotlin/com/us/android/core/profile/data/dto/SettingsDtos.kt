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
)

@Serializable
data class NotificationSettingsDto(
    @SerialName("push_enabled") val pushEnabled: Boolean = true,
    @SerialName("email_enabled") val emailEnabled: Boolean = false,
    @SerialName("quiet_hours_enabled") val quietHoursEnabled: Boolean = false,
    @SerialName("quiet_hours_start") val quietHoursStart: String? = null,
    @SerialName("quiet_hours_end") val quietHoursEnd: String? = null,
    @SerialName("quiet_hours_tz") val quietHoursTimeZone: String? = null,
    @SerialName("push_likes") val pushLikes: Boolean = false,
    @SerialName("push_super_likes") val pushSuperLikes: Boolean = true,
    @SerialName("push_comments") val pushComments: Boolean = true,
    @SerialName("push_replies") val pushReplies: Boolean = true,
    @SerialName("push_mentions") val pushMentions: Boolean = true,
    @SerialName("push_follows") val pushFollows: Boolean = true,
    @SerialName("push_friend_requests") val pushFriendRequests: Boolean = true,
    @SerialName("push_group_posts") val pushGroupPosts: Boolean = true,
    @SerialName("push_group_mentions") val pushGroupMentions: Boolean = true,
    @SerialName("push_channel_updates") val pushChannelUpdates: Boolean = true,
    @SerialName("push_channel_urgent") val pushChannelUrgent: Boolean = true,
    @SerialName("push_community_posts") val pushCommunityPosts: Boolean = false,
    @SerialName("push_community_mentions") val pushCommunityMentions: Boolean = true,
    @SerialName("push_event_reminders") val pushEventReminders: Boolean = true,
    @SerialName("push_system") val pushSystem: Boolean = true,
    @SerialName("email_digest") val emailDigest: String = "weekly",
)

@Serializable
data class UpdateNotificationSettingsRequest(
    @SerialName("push_enabled") val pushEnabled: Boolean,
    @SerialName("email_enabled") val emailEnabled: Boolean,
    @SerialName("quiet_hours_enabled") val quietHoursEnabled: Boolean,
    @SerialName("quiet_hours_start") val quietHoursStart: String,
    @SerialName("quiet_hours_end") val quietHoursEnd: String,
    @SerialName("quiet_hours_tz") val quietHoursTimeZone: String,
    @SerialName("push_likes") val pushLikes: Boolean,
    @SerialName("push_super_likes") val pushSuperLikes: Boolean,
    @SerialName("push_comments") val pushComments: Boolean,
    @SerialName("push_replies") val pushReplies: Boolean,
    @SerialName("push_mentions") val pushMentions: Boolean,
    @SerialName("push_follows") val pushFollows: Boolean,
    @SerialName("push_friend_requests") val pushFriendRequests: Boolean,
    @SerialName("push_group_posts") val pushGroupPosts: Boolean,
    @SerialName("push_group_mentions") val pushGroupMentions: Boolean,
    @SerialName("push_channel_updates") val pushChannelUpdates: Boolean,
    @SerialName("push_channel_urgent") val pushChannelUrgent: Boolean,
    @SerialName("push_community_posts") val pushCommunityPosts: Boolean,
    @SerialName("push_community_mentions") val pushCommunityMentions: Boolean,
    @SerialName("push_event_reminders") val pushEventReminders: Boolean,
    @SerialName("push_system") val pushSystem: Boolean,
    @SerialName("email_digest") val emailDigest: String,
)

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
