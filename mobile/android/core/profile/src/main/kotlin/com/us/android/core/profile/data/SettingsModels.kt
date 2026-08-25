package com.us.android.core.profile.data

/** The privacy policy graph-service actually enforces. */
data class PrivacySettings(
    val whoCanMessage: String,
    val whoCanSendConnectionRequest: String,
    val whoCanCall: String,
    val whoCanAddToGroups: String,
    val whoCanSeeOnlineStatus: String,
    val whoCanSeeReadReceipts: String,
    val whoCanSeeLastSeen: String,
    val whoCanSeeProfilePhoto: String,
    val allowPhoneDiscovery: Boolean,
    val allowContactSyncMatch: Boolean,
    val discoverableByPhoneToContacts: Boolean,
    val strictPrivacyMode: Boolean,
    val blockUnknownCalls: Boolean,
    val autoFilterAbusiveContent: Boolean,
    val under18Mode: Boolean,
    val trustedCircleCloseFriendsPosts: Boolean,
    val trustedCircleLocationPings: Boolean,
    val trustedCircleAfterHoursPosts: Boolean,
    val trustedCircleAudioRoomInvites: Boolean,
    // Production chat pass (directive §3.2).
    val chatAvailability: String,
    val sendTypingIndicators: Boolean,
    val showMessagePreview: Boolean,
    val privacyVersion: Int,
)

data class NotificationSettings(
    val pushEnabled: Boolean,
    val emailEnabled: Boolean,
    val quietHoursEnabled: Boolean,
    val quietHoursStart: String,
    val quietHoursEnd: String,
    val quietHoursTimeZone: String,
    val pushLikes: Boolean,
    val pushSuperLikes: Boolean,
    val pushComments: Boolean,
    val pushReplies: Boolean,
    val pushMentions: Boolean,
    val pushFollows: Boolean,
    val pushFriendRequests: Boolean,
    val pushGroupPosts: Boolean,
    val pushGroupMentions: Boolean,
    val pushChannelUpdates: Boolean,
    val pushChannelUrgent: Boolean,
    val pushCommunityPosts: Boolean,
    val pushCommunityMentions: Boolean,
    val pushEventReminders: Boolean,
    val pushSystem: Boolean,
    val emailDigest: String,
)

data class AccountSummary(
    val userId: String,
    val email: String,
    val phone: String,
    val emailVerified: Boolean,
    val phoneVerified: Boolean,
    val twoFactorEnabled: Boolean,
    val accountType: String,
    val accountStatus: String,
    val ageVerification: String,
    val lastLoginAt: String,
    val createdAt: String,
)

data class AccountSession(
    val id: String,
    val deviceId: String,
    val platform: String,
    val ip: String,
    val userAgent: String,
    val createdAt: String,
    val expiresAt: String,
)

data class TrustedDevice(
    val id: String,
    val name: String,
    val fingerprint: String,
    val lastUsedAt: String,
    val trustedAt: String,
)

data class SecurityEvent(
    val id: String,
    val type: String,
    val ip: String,
    val userAgent: String,
    val deviceId: String,
    val countryCode: String,
    val riskScore: Int,
    val challenged: Boolean,
    val acknowledged: Boolean,
    val occurredAt: String,
)

data class TwoFactorSetup(
    val secret: String,
    val qrCodeUrl: String,
    val recoveryCodes: List<String>,
)

data class ProfileAboutItem(
    val itemId: String,
    val section: String,
    val title: String,
    val subtitle: String,
    val detail: String,
    val visibility: String,
    val sortOrder: Int,
)

data class ProfileLink(
    val id: String,
    val title: String,
    val url: String,
    val category: String,
    val visibility: String,
    val pinned: Boolean,
    val sortOrder: Int,
)
