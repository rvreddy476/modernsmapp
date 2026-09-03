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
    /** `public` or `private`. */
    val accountVisibility: String,
    /** `everyone` or `friends`. */
    val allowCommentsFrom: String,
    val privacyVersion: Int,
) {
    val isPrivateAccount: Boolean get() = accountVisibility == VISIBILITY_PRIVATE

    companion object {
        const val VISIBILITY_PUBLIC = "public"
        const val VISIBILITY_PRIVATE = "private"
        const val COMMENTS_EVERYONE = "everyone"
        const val COMMENTS_FRIENDS = "friends"
    }
}

/**
 * One notification category on the detailed preferences endpoint. [key] is
 * the suffix of the `inapp_<key>` / `push_<key>` wire pair; [label] is what
 * the settings row shows. [primary] categories are the TikTok-style
 * "Interactions" block; the rest sit under an expandable "More".
 */
enum class NotificationCategory(val key: String, val label: String, val primary: Boolean) {
    LIKES("likes", "Likes", primary = true),
    COMMENTS("comments", "Comments", primary = true),
    FOLLOWS("follows", "New followers", primary = true),
    MENTIONS("mentions", "Mentions and tags", primary = true),
    REPOSTS("reposts", "Reposts", primary = true),
    LIVE("live", "LIVE", primary = true),
    MESSAGES("messages", "Messages", primary = true),
    SUPER_LIKES("super_likes", "Super likes", primary = false),
    REPLIES("replies", "Replies", primary = false),
    FRIEND_REQUESTS("friend_requests", "Connection requests", primary = false),
    GROUP_POSTS("group_posts", "Group posts", primary = false),
    GROUP_MENTIONS("group_mentions", "Group mentions", primary = false),
    CHANNEL_UPDATES("channel_updates", "Channel updates", primary = false),
    CHANNEL_URGENT("channel_urgent", "Urgent channel updates", primary = false),
    COMMUNITY_POSTS("community_posts", "Community posts", primary = false),
    COMMUNITY_MENTIONS("community_mentions", "Community mentions", primary = false),
    EVENT_REMINDERS("event_reminders", "Event reminders", primary = false),
    SYSTEM("system", "Security and system", primary = false),
    ;

    companion object {
        val primaries: List<NotificationCategory> get() = entries.filter { it.primary }
        val secondaries: List<NotificationCategory> get() = entries.filterNot { it.primary }
    }
}

/** The in-app and push switches for one category. */
data class NotificationChannels(val inApp: Boolean, val push: Boolean)

data class NotificationSettings(
    val pushEnabled: Boolean,
    val emailEnabled: Boolean,
    val quietHoursEnabled: Boolean,
    val quietHoursStart: String,
    val quietHoursEnd: String,
    val quietHoursTimeZone: String,
    val emailDigest: String,
    val channels: Map<NotificationCategory, NotificationChannels> = emptyMap(),
) {
    fun channels(category: NotificationCategory): NotificationChannels =
        channels[category] ?: NotificationChannels(inApp = true, push = true)

    fun withChannels(category: NotificationCategory, value: NotificationChannels): NotificationSettings =
        copy(channels = channels + (category to value))
}

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
    val deactivatedAt: String? = null,
    val scheduledPurgeDate: String? = null,
)

/** Screen-time controls owned by user-service (`/v1/users/me/wellbeing`). */
data class WellbeingSettings(
    /** Minutes per day; null is "off". */
    val dailyLimitMins: Int?,
    /** `HH:mm`, or null. Both null means sleep hours are off. */
    val bedtimeStart: String?,
    val bedtimeEnd: String?,
    val focusModeEnabled: Boolean,
    val nudgeIntervalMins: Int,
    val hideLikeCounts: Boolean,
) {
    val sleepHoursEnabled: Boolean get() = bedtimeStart != null && bedtimeEnd != null
}

/**
 * The subset of [WellbeingSettings] [com.us.android.screentime.ScreenTimeGuardCoordinator]
 * resolves against. A separate, smaller type rather than reusing
 * [WellbeingSettings] directly so the guard's cache — and what gets persisted
 * for a cold start — carries only the fields it actually reads.
 */
data class WellbeingGuardSnapshot(
    val dailyLimitMins: Int?,
    val bedtimeStart: String?,
    val bedtimeEnd: String?,
) {
    val sleepHoursEnabled: Boolean get() = bedtimeStart != null && bedtimeEnd != null
}

fun WellbeingSettings.toGuardSnapshot(): WellbeingGuardSnapshot =
    WellbeingGuardSnapshot(dailyLimitMins, bedtimeStart, bedtimeEnd)

data class ScreenTimeDay(val date: String, val minutes: Int, val sessions: Int)

data class ScreenTimeWeek(
    val days: List<ScreenTimeDay>,
    val todayMinutes: Int,
    val dailyLimitMins: Int?,
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
