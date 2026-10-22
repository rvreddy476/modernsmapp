package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CachedMessageExtras
import com.us.android.core.chat.data.ChatLocation
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.LOCATION_MESSAGE
import com.us.android.core.chat.data.LOCATION_PLACE_MAX
import com.us.android.core.chat.data.LOCATION_PREVIEW
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.chat.data.TypedSend
import com.us.android.core.chat.data.accuracyText
import com.us.android.core.chat.data.capPlaceText
import com.us.android.core.chat.data.chatLocationOf
import com.us.android.core.chat.data.coordsText
import com.us.android.core.chat.data.geoUri
import com.us.android.core.chat.data.inboxPreview
import com.us.android.core.chat.data.mapsWebUrl
import com.us.android.core.chat.data.parseChatFrame
import com.us.android.core.chat.data.roundCoord
import com.us.android.core.chat.data.toDomain
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import org.junit.Test

/** Shared location (founder, 23 Oct 2026): the wire, cache, outbox and preview. */
class ChatLocationTest {

    /** NetworkModule.provideJson, exactly. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private fun fixture(name: String): String =
        requireNotNull(javaClass.classLoader?.getResource("contracts/chat/$name.json")) { "missing fixture $name" }.readText()

    @Test
    fun `a device fix is rounded, capped and checked before it is sent`() {
        val loc = chatLocationOf(12.9715987, 77.5945627, 12.4, label = "  Cubbon Park ", address = "Kasturba Rd\nBengaluru")!!
        assertThat(loc.lat).isEqualTo(12.9716)
        assertThat(loc.lng).isEqualTo(77.59456)
        assertThat(loc.accuracyM).isEqualTo(12.0)
        assertThat(loc.label).isEqualTo("Cubbon Park")
        assertThat(loc.address).isEqualTo("Kasturba Rd Bengaluru")
        assertThat(chatLocationOf(91.0, 1.0, null, null, null)).isNull()
        assertThat(chatLocationOf(1.0, -180.5, null, null, null)).isNull()
        assertThat(chatLocationOf(Double.NaN, 1.0, null, null, null)).isNull()
        assertThat(chatLocationOf(0.0, 0.0, -1.0, null, null)!!.accuracyM).isNull()
        assertThat(chatLocationOf(0.0, 0.0, 1e9, null, null)!!.accuracyM).isEqualTo(100_000.0)
        // No "-0" on the wire.
        assertThat(1.0 / roundCoord(-0.000001)).isPositiveInfinity()
        val long = "é".repeat(LOCATION_PLACE_MAX + 5)
        val capped = capPlaceText(long)!!
        assertThat(capped.codePointCount(0, capped.length)).isEqualTo(LOCATION_PLACE_MAX)
        assertThat(capPlaceText("   ")).isNull()
    }

    @Test
    fun `the send carries type location and only the location`() {
        val typed = TypedSend.location(ChatLocation(12.9716, 77.59456, 12.0, "Park"))
        val wire = Json { explicitNulls = false }.encodeToString(SendMessageRequest.serializer(), typed.toRequest())
        assertThat(wire).contains("\"type\":\"location\"")
        assertThat(wire).contains("\"location\":{\"lat\":12.9716,\"lng\":77.59456,\"accuracy_m\":12.0,\"label\":\"Park\"}")
        assertThat(wire).doesNotContain("media_id")
        assertThat(wire).doesNotContain("sticker_id")
        // The outbox row round-trips it.
        val row = json.encodeToString(TypedSend.serializer(), typed)
        assertThat(json.decodeFromString(TypedSend.serializer(), row).location).isEqualTo(typed.location)
        assertThat(typed.type).isEqualTo(LOCATION_MESSAGE)
        assertThat(typed.text).isEmpty()
    }

    @Test
    fun `the server fixture decodes to the place`() {
        val message = json.decodeFromString<ApiEnvelope<MessageDto>>(fixture("message_location")).data!!.toDomain()
        assertThat(message.type).isEqualTo(LOCATION_MESSAGE)
        assertThat(message.location).isEqualTo(ChatLocation(12.9716, 77.59456, 12.0, "Cubbon Park", "Kasturba Rd, Bengaluru"))
        assertThat(message.text).isEmpty()
    }

    @Test
    fun `an impossible place from the server is dropped, not drawn`() {
        val raw = """{"data":{"msg_id":"m","conversation_id":"c","sender_id":"s","type":"location","location":{"lat":95,"lng":1}}}"""
        assertThat(json.decodeFromString<ApiEnvelope<MessageDto>>(raw).data!!.toDomain().location).isNull()
    }

    @Test
    fun `a realtime frame carries the place`() {
        val event = parseChatFrame(
            json,
            """{"type":"message","payload":{"message_id":"m1","conversation_id":"c1","sender_id":"s1","type":"location","text":"",""" +
                """"location":{"lat":1.5,"lng":2.25}}}""",
        )
        val message = (event as ChatSocketEvent.MessageReceived).message
        assertThat(message.location).isEqualTo(ChatLocation(1.5, 2.25))
    }

    @Test
    fun `the inbox previews a location without the place, and the cache keeps it`() {
        val message = Message(
            id = "m", conversationId = "c", senderId = "s", senderDisplayName = null, text = "", createdAt = "t",
            type = LOCATION_MESSAGE, location = ChatLocation(12.9716, 77.59456, label = "Secret spot"),
        )
        assertThat(inboxPreview(message)).isEqualTo(LOCATION_PREVIEW)
        assertThat(inboxPreview(message)).doesNotContain("12.97")
        val cached = CachedMessageExtras.of(message)!!
        val restored = cached.applyTo(message.copy(location = null))
        assertThat(restored.location).isEqualTo(message.location)
    }

    @Test
    fun `open in maps prefers geo, with the web link as the fallback`() {
        val place = ChatLocation(12.9716, 77.59456, label = "Cubbon Park")
        assertThat(geoUri(place) { it.replace(" ", "%20") }).isEqualTo("geo:12.9716,77.59456?q=12.9716,77.59456(Cubbon%20Park)")
        assertThat(geoUri(ChatLocation(0.0, -0.5)) { it }).isEqualTo("geo:0,-0.5?q=0,-0.5")
        assertThat(mapsWebUrl(place)).isEqualTo("https://www.google.com/maps/search/?api=1&query=12.9716,77.59456")
        assertThat(coordsText(place)).isEqualTo("12.97160, 77.59456")
        assertThat(accuracyText(12.4)).isEqualTo("Accurate to 12 m")
        assertThat(accuracyText(1400.0)).isEqualTo("Accurate to 1.4 km")
        assertThat(accuracyText(null)).isNull()
    }
}
