package com.us.android.core.profile

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.dto.UpdateNotificationSettingsRequest
import com.us.android.core.profile.data.dto.UpdatePrivacySettingsRequest
import kotlinx.serialization.encodeToString
import kotlinx.serialization.json.Json
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
            ),
        )
        val body = json.parseToJsonElement(encoded).jsonObject

        assertThat(body.keys).containsExactlyElementsIn(PRIVACY_KEYS)
        assertThat(encoded).contains("\"allow_phone_discovery\":false")
        assertThat(encoded).contains("\"auto_filter_abusive_content\":false")
        assertThat(encoded).contains("\"tc_audio_room_invite\":false")
    }

    @Test
    fun notificationSnapshotEncodesEveryDisabledCategory() {
        val request = UpdateNotificationSettingsRequest(
            pushEnabled = false,
            emailEnabled = false,
            quietHoursEnabled = false,
            quietHoursStart = "22:00",
            quietHoursEnd = "07:00",
            quietHoursTimeZone = "Asia/Kolkata",
            pushLikes = false,
            pushSuperLikes = false,
            pushComments = false,
            pushReplies = false,
            pushMentions = false,
            pushFollows = false,
            pushFriendRequests = false,
            pushGroupPosts = false,
            pushGroupMentions = false,
            pushChannelUpdates = false,
            pushChannelUrgent = false,
            pushCommunityPosts = false,
            pushCommunityMentions = false,
            pushEventReminders = false,
            pushSystem = false,
            emailDigest = "never",
        )
        val encoded = json.encodeToString(request)
        val body = json.parseToJsonElement(encoded).jsonObject

        assertThat(body.keys).containsExactlyElementsIn(NOTIFICATION_KEYS)
        assertThat(encoded).contains("\"push_enabled\":false")
        assertThat(encoded).contains("\"push_system\":false")
        assertThat(encoded).contains("\"email_digest\":\"never\"")
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
            "show_message_preview",
        )
        val NOTIFICATION_KEYS = setOf(
            "push_enabled", "email_enabled", "quiet_hours_enabled", "quiet_hours_start",
            "quiet_hours_end", "quiet_hours_tz", "push_likes", "push_super_likes",
            "push_comments", "push_replies", "push_mentions", "push_follows",
            "push_friend_requests", "push_group_posts", "push_group_mentions",
            "push_channel_updates", "push_channel_urgent", "push_community_posts",
            "push_community_mentions", "push_event_reminders", "push_system", "email_digest",
        )
    }
}
