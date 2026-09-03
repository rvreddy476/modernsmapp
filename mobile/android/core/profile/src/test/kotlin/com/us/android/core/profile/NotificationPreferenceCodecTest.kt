package com.us.android.core.profile

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.NotificationCategory
import com.us.android.core.profile.data.NotificationChannels
import com.us.android.core.profile.data.NotificationPreferenceCodec
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Test

class NotificationPreferenceCodecTest {

    @Test
    fun `decodes the global block and every category pair, defaulting absent pairs to on`() {
        val body = Json.parseToJsonElement(
            """{"push_enabled":true,"email_enabled":false,"quiet_hours_enabled":true,
               "quiet_hours_start":"22:00","quiet_hours_end":"07:00","quiet_hours_tz":"Asia/Kolkata",
               "email_digest":"daily","inapp_likes":false,"push_likes":true,"push_live":false,
               "updated_at":"2026-09-03T00:00:00Z"}""",
        ).jsonObject

        val decoded = NotificationPreferenceCodec.decode(body)

        assertThat(decoded.quietHoursEnabled).isTrue()
        assertThat(decoded.quietHoursTimeZone).isEqualTo("Asia/Kolkata")
        assertThat(decoded.emailDigest).isEqualTo("daily")
        assertThat(decoded.channels(NotificationCategory.LIKES))
            .isEqualTo(NotificationChannels(inApp = false, push = true))
        assertThat(decoded.channels(NotificationCategory.LIVE))
            .isEqualTo(NotificationChannels(inApp = true, push = false))
        assertThat(decoded.channels(NotificationCategory.SYSTEM))
            .isEqualTo(NotificationChannels(inApp = true, push = true))
    }

    @Test
    fun `a null quiet-hours field decodes as blank rather than the literal null`() {
        val body = Json.parseToJsonElement("""{"quiet_hours_start":null,"email_digest":null}""").jsonObject

        val decoded = NotificationPreferenceCodec.decode(body)

        assertThat(decoded.quietHoursStart).isEmpty()
        assertThat(decoded.emailDigest).isEqualTo("weekly")
    }

    @Test
    fun `encode then decode round-trips every pair`() {
        val original = NotificationPreferenceCodec.decode(Json.parseToJsonElement("{}").jsonObject)
            .withChannels(NotificationCategory.MESSAGES, NotificationChannels(inApp = true, push = false))
            .withChannels(NotificationCategory.REPOSTS, NotificationChannels(inApp = false, push = false))
            .copy(pushEnabled = false, quietHoursStart = "23:00")

        val roundTripped = NotificationPreferenceCodec.decode(NotificationPreferenceCodec.encode(original))

        assertThat(roundTripped).isEqualTo(original)
    }
}
