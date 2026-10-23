package com.us.android.core.profile

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.NotificationCategory
import com.us.android.core.profile.data.NotificationChannels
import com.us.android.core.profile.data.NotificationPreferenceCodec
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Test
import java.io.File

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

    /** New videos (2026-09-12): `inapp_new_videos` / `push_new_videos`, default on, and part of the full snapshot. */
    @Test
    fun `the new videos pair decodes, defaults to on, and round-trips`() {
        val decoded = NotificationPreferenceCodec.decode(
            Json.parseToJsonElement("""{"inapp_new_videos":true,"push_new_videos":false}""").jsonObject,
        )
        assertThat(decoded.channels(NotificationCategory.NEW_VIDEOS))
            .isEqualTo(NotificationChannels(inApp = true, push = false))

        val absent = NotificationPreferenceCodec.decode(Json.parseToJsonElement("{}").jsonObject)
        assertThat(absent.channels(NotificationCategory.NEW_VIDEOS))
            .isEqualTo(NotificationChannels(inApp = true, push = true))

        val encoded = NotificationPreferenceCodec.encode(decoded)
        assertThat(encoded.keys).containsAtLeast("inapp_new_videos", "push_new_videos")
        assertThat(NotificationPreferenceCodec.decode(encoded)).isEqualTo(decoded)
    }

    /**
     * Video credits (2026-10-23), against notification-service's golden files
     * (`internal/http/testdata/contracts`, copied byte for byte into
     * `src/test/resources/contracts`): the `credits` category is the
     * `push_credits` / `inapp_credits` pair, default on, and the save the app
     * sends carries both keys with the values of the request fixture.
     */
    @Test
    fun `the credits pair reads from the server's fixture, defaults to on, and round-trips onto its request`() {
        val contracts = File("src/test/resources/contracts")
        val read = Json.parseToJsonElement(File(contracts, "notification_preferences_credits.json").readText())
            .jsonObject.getValue("data").jsonObject
        val request = Json.parseToJsonElement(File(contracts, "notification_preferences_credits_request.json").readText()).jsonObject

        assertThat(NotificationPreferenceCodec.pushKey(NotificationCategory.CREDITS)).isEqualTo("push_credits")
        assertThat(NotificationPreferenceCodec.inAppKey(NotificationCategory.CREDITS)).isEqualTo("inapp_credits")
        val decoded = NotificationPreferenceCodec.decode(read)
        assertThat(decoded.channels(NotificationCategory.CREDITS)).isEqualTo(NotificationChannels(inApp = true, push = false))
        val absent = NotificationPreferenceCodec.decode(Json.parseToJsonElement("{}").jsonObject)
        assertThat(absent.channels(NotificationCategory.CREDITS)).isEqualTo(NotificationChannels(inApp = true, push = true))

        val encoded = NotificationPreferenceCodec.encode(decoded)
        request.forEach { (key, value) -> assertThat(encoded[key]).isEqualTo(value) }
        // Every category the server's fixture has a pair for that this build knows is read from it, not
        // defaulted. Memberships (2026-10-23) post-date this fixture; its own test below reads them.
        NotificationCategory.entries.filter { it != NotificationCategory.MEMBERSHIPS }.forEach { category ->
            assertThat(read.keys).containsAtLeast(
                NotificationPreferenceCodec.inAppKey(category),
                NotificationPreferenceCodec.pushKey(category),
            )
        }
        assertThat(NotificationPreferenceCodec.decode(encoded)).isEqualTo(decoded)
    }

    /**
     * Paid memberships (2026-10-23), against notification-service's golden
     * files (`internal/http/testdata/contracts/notification_preferences_memberships*.json`,
     * copied byte for byte): the `memberships` category is the
     * `push_memberships` / `inapp_memberships` pair, default on, read from
     * the fixture (push off there), and the save carries both keys with the
     * request fixture's values. Every category this build knows is in that
     * fixture, so none of them is defaulted.
     */
    @Test
    fun `the memberships pair reads from the server's fixture, defaults to on, and round-trips onto its request`() {
        val contracts = File("src/test/resources/contracts")
        val read = Json.parseToJsonElement(File(contracts, "notification_preferences_memberships.json").readText())
            .jsonObject.getValue("data").jsonObject
        val requestFile = File(contracts, "notification_preferences_memberships_request.json")
        val request = Json.parseToJsonElement(requestFile.readText()).jsonObject
        val memberships = NotificationCategory.MEMBERSHIPS

        assertThat(memberships.key).isEqualTo("memberships")
        assertThat(NotificationPreferenceCodec.pushKey(memberships)).isEqualTo("push_memberships")
        assertThat(NotificationPreferenceCodec.inAppKey(memberships)).isEqualTo("inapp_memberships")
        val decoded = NotificationPreferenceCodec.decode(read)
        assertThat(decoded.channels(memberships)).isEqualTo(NotificationChannels(inApp = true, push = false))
        val absent = NotificationPreferenceCodec.decode(Json.parseToJsonElement("{}").jsonObject)
        assertThat(absent.channels(memberships)).isEqualTo(NotificationChannels(inApp = true, push = true))

        val encoded = NotificationPreferenceCodec.encode(decoded)
        request.forEach { (key, value) -> assertThat(encoded[key]).isEqualTo(value) }
        NotificationCategory.entries.forEach { category ->
            assertThat(read.keys).containsAtLeast(
                NotificationPreferenceCodec.inAppKey(category),
                NotificationPreferenceCodec.pushKey(category),
            )
        }
        assertThat(NotificationPreferenceCodec.decode(encoded)).isEqualTo(decoded)
        // A secondary category: under "More", beside Video credits.
        assertThat(NotificationCategory.secondaries).contains(NotificationCategory.MEMBERSHIPS)
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
