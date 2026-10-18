package com.us.android.core.profile

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.InboxSettingOptions
import com.us.android.core.profile.data.dto.PrivacySettingsDto
import com.us.android.core.profile.data.dto.UpdatePrivacySettingsRequest
import kotlinx.serialization.json.Json
import org.junit.Test

/** Chat parity (CONTRACT §7, B3 value sets): the Inbox & messaging options and their wire. */
class InboxSettingOptionsTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    @Test
    fun `every B3 value has words`() {
        assertThat(InboxSettingOptions.whoCanMessage.map { it.value }).containsExactly(
            "everyone_message_requests", "followers_message_requests", "friends_of_friends_requests",
            "connections_and_mutual_followers", "connections_only", "no_one",
        )
        assertThat(InboxSettingOptions.whoCanAddToGroups.map { it.value }).containsExactly(
            "everyone_with_approval", "friends_of_friends_invites", "connections_and_contacts", "connections_only", "no_one",
        )
        assertThat(InboxSettingOptions.whoCanCall.map { it.value }).containsExactly("accepted_chats_only", "connections_only", "no_one")
        assertThat(InboxSettingOptions.audience.map { it.value }).containsExactly("everyone", "connections_only", "no_one")
        assertThat(InboxSettingOptions.bubbleStyles.map { it.value })
            .containsExactly("default", "ocean", "ember", "forest", "violet", "mono")
    }

    @Test
    fun `a clamped value the list does not know is shown, not hidden`() {
        val options = InboxSettingOptions.withCurrent(InboxSettingOptions.whoCanCall, "minors_contacts_only")
        assertThat(options.last().value).isEqualTo("minors_contacts_only")
        assertThat(options.last().label).isEqualTo("Minors contacts only")
        assertThat(InboxSettingOptions.withCurrent(InboxSettingOptions.whoCanCall, "no_one")).hasSize(3)
        assertThat(InboxSettingOptions.labelOf(InboxSettingOptions.audience, "no_one")).isEqualTo("No one")
    }

    @Test
    fun `filtered words are trimmed, lower-cased, de-duplicated and capped`() {
        assertThat(InboxSettingOptions.addKeyword(emptyList(), "  Spam ")).containsExactly("spam")
        assertThat(InboxSettingOptions.addKeyword(listOf("spam"), "SPAM")).containsExactly("spam")
        assertThat(InboxSettingOptions.addKeyword(emptyList(), "   ")).isNull()
        assertThat(InboxSettingOptions.addKeyword(emptyList(), "x".repeat(31))).isNull()
        assertThat(InboxSettingOptions.addKeyword(emptyList(), "x".repeat(30))).hasSize(1)
        val full = List(100) { "w$it" }
        assertThat(InboxSettingOptions.addKeyword(full, "one more")).isNull()
    }

    @Test
    fun `parity keys are read when present and absent stays null`() {
        val old = json.decodeFromString(PrivacySettingsDto.serializer(), """{"who_can_message":"no_one"}""")
        assertThat(old.dmSafeMode).isNull()
        assertThat(old.chatBubbleStyle).isNull()
        val new = json.decodeFromString(
            PrivacySettingsDto.serializer(),
            """{"dm_safe_mode":true,"dm_sensitive_media_filter":false,"dm_filtered_keywords":["spam"],"chat_bubble_style":"ocean"}""",
        )
        assertThat(new.dmSafeMode).isTrue()
        assertThat(new.dmFilteredKeywords).containsExactly("spam")
        assertThat(new.chatBubbleStyle).isEqualTo("ocean")
    }

    @Test
    fun `a PUT omits parity keys this client never read`() {
        val base = UpdatePrivacySettingsRequest(
            "connections_only", "everyone", "connections_only", "connections_only", "connections_only", "connections_only",
            "connections_only", "everyone", false, false, false, false, false, true, true, true, true, true, "enabled", true,
            true, "public", "everyone",
        )
        val wire = json.encodeToString(UpdatePrivacySettingsRequest.serializer(), base)
        assertThat(wire).doesNotContain("dm_safe_mode")
        assertThat(wire).doesNotContain("chat_bubble_style")
        val withKeys = json.encodeToString(
            UpdatePrivacySettingsRequest.serializer(),
            base.copy(dmSafeMode = false, dmFilteredKeywords = emptyList(), chatBubbleStyle = "mono"),
        )
        assertThat(withKeys).contains("\"dm_safe_mode\":false")
        // An empty list is a real answer (clear every word), so it is sent.
        assertThat(withKeys).contains("\"dm_filtered_keywords\":[]")
        assertThat(withKeys).contains("\"chat_bubble_style\":\"mono\"")
    }
}
