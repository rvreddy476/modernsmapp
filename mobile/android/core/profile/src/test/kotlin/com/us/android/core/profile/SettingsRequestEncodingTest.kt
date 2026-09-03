package com.us.android.core.profile

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.NotificationCategory
import com.us.android.core.profile.data.NotificationChannels
import com.us.android.core.profile.data.NotificationPreferenceCodec
import com.us.android.core.profile.data.NotificationSettings
import com.us.android.core.profile.data.dto.UpdatePrivacySettingsRequest
import com.us.android.core.profile.data.dto.UpdateWellbeingRequest
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import org.junit.Test

class SettingsRequestEncodingTest {
    private val json = Json {
        encodeDefaults = false
        explicitNulls = false
    }

    @Test
    fun privacySnapshotEncodesEveryFalseAndEveryAudience() {
        val encoded = json.encodeToString(
            UpdatePrivacySettingsRequest(
                whoCanMessage = "no_one",
                whoCanSendConnectionRequest = "no_one",
                whoCanCall = "no_one",
                whoCanAddToGroups = "no_one",
                whoCanSeeOnlineStatus = "no_one",
                whoCanSeeReadReceipts = "no_one",
                whoCanSeeLastSeen = "no_one",
                whoCanSeeProfilePhoto = "no_one",
                allowPhoneDiscovery = false,
                allowContactSyncMatch = false,
                discoverableByPhoneToContacts = false,
                strictPrivacyMode = false,
                blockUnknownCalls = false,
                autoFilterAbusiveContent = false,
                trustedCircleCloseFriendsPosts = false,
                trustedCircleLocationPings = false,
                trustedCircleAfterHoursPosts = false,
                trustedCircleAudioRoomInvites = false,
                chatAvailability = "paused",
                sendTypingIndicators = false,
                showMessagePreview = false,
                accountVisibility = "private",
                allowCommentsFrom = "friends",
            ),
        )
        val body = json.parseToJsonElement(encoded).jsonObject

        assertThat(body.keys).containsExactlyElementsIn(PRIVACY_KEYS)
        assertThat(encoded).contains("\"allow_phone_discovery\":false")
        assertThat(encoded).contains("\"auto_filter_abusive_content\":false")
        assertThat(encoded).contains("\"tc_audio_room_invite\":false")
        assertThat(encoded).contains("\"account_visibility\":\"private\"")
        assertThat(encoded).contains("\"allow_comments_from\":\"friends\"")
    }

    @Test
    fun notificationSnapshotEncodesEveryDisabledCategoryPair() {
        val value = NotificationSettings(
            pushEnabled = false,
            emailEnabled = false,
            quietHoursEnabled = false,
            quietHoursStart = "22:00",
            quietHoursEnd = "07:00",
            quietHoursTimeZone = "Asia/Kolkata",
            emailDigest = "never",
            channels = NotificationCategory.entries.associateWith { NotificationChannels(inApp = false, push = false) },
        )
        val body = NotificationPreferenceCodec.encode(value)
        val encoded = json.encodeToString(body)

        assertThat(body.keys).containsExactlyElementsIn(NOTIFICATION_KEYS)
        assertThat(encoded).contains("\"push_enabled\":false")
        assertThat(encoded).contains("\"push_system\":false")
        assertThat(encoded).contains("\"inapp_likes\":false")
        assertThat(encoded).contains("\"email_digest\":\"never\"")
    }

    @Test
    fun wellbeingSnapshotWritesSleepHoursOffAsExplicitNulls() {
        // explicitNulls = false would drop a Kotlin null; JsonNull is a value
        // and must survive, or "sleep hours off" never reaches the server.
        val encoded = json.encodeToString(
            UpdateWellbeingRequest(
                dailyLimitMins = 0,
                bedtimeStart = JsonNull,
                bedtimeEnd = JsonNull,
                focusModeEnabled = false,
                nudgeIntervalMins = 0,
                hideLikeCounts = false,
            ),
        )

        assertThat(encoded).contains("\"bedtime_start\":null")
        assertThat(encoded).contains("\"bedtime_end\":null")
        assertThat(encoded).contains("\"daily_limit_mins\":0")
        assertThat(
            json.encodeToString(
                UpdateWellbeingRequest(60, JsonPrimitive("23:00"), JsonPrimitive("07:00"), false, 0, false),
            ),
        ).contains("\"bedtime_start\":\"23:00\"")
    }

    private companion object {
        val PRIVACY_KEYS = setOf(
            "who_can_message", "who_can_send_connection_request", "who_can_call",
            "who_can_add_to_groups", "who_can_see_online_status",
            "who_can_see_read_receipts", "who_can_see_last_seen",
            "who_can_see_profile_photo", "allow_phone_discovery",
            "allow_contact_sync_match", "discoverable_by_phone_to_contacts",
            "strict_privacy_mode", "block_unknown_calls", "auto_filter_abusive_content",
            "tc_close_friends_posts", "tc_location_pings", "tc_after_hours_posts",
            "tc_audio_room_invite", "chat_availability", "send_typing_indicators",
            "show_message_preview", "account_visibility", "allow_comments_from",
        )
        val CATEGORY_KEYS = listOf(
            "likes", "comments", "follows", "mentions", "reposts", "live", "messages",
            "super_likes", "replies", "friend_requests", "group_posts", "group_mentions",
            "channel_updates", "channel_urgent", "community_posts", "community_mentions",
            "event_reminders", "system",
        )
        val NOTIFICATION_KEYS = setOf(
            "push_enabled", "email_enabled", "quiet_hours_enabled", "quiet_hours_start",
            "quiet_hours_end", "quiet_hours_tz", "email_digest",
        ) + CATEGORY_KEYS.flatMap { listOf("inapp_$it", "push_$it") }
    }
}
