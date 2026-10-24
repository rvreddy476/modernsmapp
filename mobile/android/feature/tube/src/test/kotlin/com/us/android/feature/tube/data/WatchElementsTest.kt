package com.us.android.feature.tube.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.feature.tube.ui.watch.fitFrame
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import androidx.compose.ui.unit.dp

/**
 * End screens and cards as a viewer sees them (2026-10-24), against
 * post-service's resolved shapes: EndScreenElement (service/end_screens.go:647)
 * and VideoCardView (:678), each a bare list from GET …/end-screens and
 * GET …/cards (internal/http/handler.go:394-396).
 */
class WatchElementsTest {

    /** Four elements as the server writes them, plus one with no window and one of an unknown type. */
    private val endScreensBody = """
        [
          {"id":"e1","type":"video","position":{"x":0.05,"y":0.1,"w":0.3},"start_ms":590000,"end_ms":600000,
           "video":{"id":"v2","title":"Part two","thumbnail_url":"/v1/media/m2/serve/thumb","duration_seconds":640,
                    "channel_name":"Kiran","view_count":1200},"playlist":null,"channel":null,"link":null},
          {"id":"e2","type":"channel_subscribe","position":{"slot":1},"start_ms":590000,"end_ms":600000,
           "video":null,"playlist":null,
           "channel":{"user_id":"u1","handle":"@kiran","name":"Kiran","avatar_url":null,"subscriber_count":10,"is_subscribed":true},
           "link":null},
          {"id":"e3","type":"playlist","position":"{\"x\":0.6,\"y\":0.55,\"w\":0.3}","start_ms":595000,"end_ms":600000,
           "video":null,"playlist":{"id":"c1","title":"","thumbnail_url":"https://cdn/c1.jpg","item_count":"7"},"channel":null,"link":null},
          {"id":"e4","type":"external_link","position":{"x":0.9,"y":0.9,"w":0.9},"start_ms":580000,"end_ms":600000,
           "video":null,"playlist":null,"channel":null,"link":{"url":"https://www.shop.example/p?x=1","title":null,"domain":""}},
          {"id":"e5","type":"video","position":{"x":0,"y":0,"w":0.3},"start_ms":600000,"end_ms":600000,
           "video":{"id":"v3"}},
          {"id":"e6","type":"poll","position":{"x":0,"y":0,"w":0.3},"start_ms":0,"end_ms":10}
        ]
    """.trimIndent()

    private fun endScreens() = normalizeViewerEndScreens(Json.parseToJsonElement(endScreensBody)) { "https://gw$it" }

    @Test
    fun `end screens decode every kind and drop what cannot be drawn`() {
        val elements = endScreens()

        assertThat(elements.map { it.id }).containsExactly("e1", "e2", "e3", "e4").inOrder()
        val video = elements[0]
        assertThat(video.kind).isEqualTo(EndScreenKind.VIDEO)
        assertThat(video.video).isEqualTo(
            EndScreenVideo("v2", "Part two", "https://gw/v1/media/m2/serve/thumb", 640, "Kiran", 1200),
        )
        assertThat(video.target()).isEqualTo(ElementTarget.Video("v2"))

        val channel = elements[1]
        assertThat(channel.channel?.handle).isEqualTo("kiran")
        assertThat(channel.channel?.avatarUrl).isEmpty()
        assertThat(channel.channel?.isSubscribed).isTrue()
        assertThat(channel.target()).isEqualTo(ElementTarget.Channel("u1", "kiran"))

        val playlist = elements[2]
        assertThat(playlist.playlist).isEqualTo(EndScreenCollection("c1", "Untitled collection", "https://cdn/c1.jpg", 7))
        assertThat(playlist.target()).isEqualTo(ElementTarget.Collection("c1"))

        val link = elements[3]
        assertThat(link.link).isEqualTo(EndScreenLink("https://www.shop.example/p?x=1", "shop.example", "shop.example"))
        assertThat(link.target()).isEqualTo(ElementTarget.External("https://www.shop.example/p?x=1"))
    }

    @Test
    fun `positions read the object, its JSON string and an old slot, and stay in the frame`() {
        val elements = endScreens()
        assertThat(elements[0].position).isEqualTo(EndScreenPosition(0.05, 0.1, 0.3))
        // slot 1 for a circle: top-right, w 0.2 (end_screens.go:144-160).
        assertThat(elements[1].position).isEqualTo(EndScreenPosition(0.75, 0.1, 0.2))
        assertThat(elements[2].position).isEqualTo(EndScreenPosition(0.6, 0.55, 0.3))
        // w 0.9 is clamped to 0.5, and the corner pulled back inside.
        assertThat(elements[3].position).isEqualTo(EndScreenPosition(0.5, 0.5, 0.5))
    }

    @Test
    fun `geometry follows the contract`() {
        assertThat(heightFraction(EndScreenKind.VIDEO, 0.3)).isEqualTo(0.3)
        assertThat(heightFraction(EndScreenKind.CHANNEL, 0.18)).isWithin(1e-9).of(0.32)
        assertThat(slotPosition(0, EndScreenKind.VIDEO)).isEqualTo(EndScreenPosition(0.05, 0.1, 0.3))
        assertThat(slotPosition(3, EndScreenKind.VIDEO)).isEqualTo(EndScreenPosition(0.65, 0.6, 0.3))
        assertThat(slotPosition(-1, EndScreenKind.VIDEO)).isEqualTo(slotPosition(3, EndScreenKind.VIDEO))
        // A circle may not be wider than the frame is tall: 9/16.
        assertThat(clampPosition(EndScreenKind.CHANNEL, EndScreenPosition(0.0, 0.0, 0.5)).w).isEqualTo(0.5)
        assertThat(clampPosition(EndScreenKind.VIDEO, EndScreenPosition(-1.0, 2.0, 0.01))).isEqualTo(EndScreenPosition(0.0, 0.88, 0.12))
        assertThat(clampPosition(EndScreenKind.VIDEO, EndScreenPosition(Double.NaN, 0.1, Double.NaN)).w).isEqualTo(0.3)
        val unknown = readEndScreenPosition(JsonPrimitive("not json"), EndScreenKind.VIDEO, index = 2)
        assertThat(unknown).isEqualTo(slotPosition(2, EndScreenKind.VIDEO))
        val zeroWidth = readEndScreenPosition(buildJsonObject { put("x", 0.1); put("y", 0.1); put("w", 0) }, EndScreenKind.VIDEO, 0)
        assertThat(zeroWidth.w).isEqualTo(0.3)
    }

    @Test
    fun `only https links with a host are kept`() {
        assertThat(safeHttpsUrl("https://example.com/a")).isEqualTo("https://example.com/a")
        assertThat(safeHttpsUrl("http://example.com")).isEmpty()
        assertThat(safeHttpsUrl("javascript:alert(1)")).isEmpty()
        assertThat(safeHttpsUrl("https:///nohost")).isEmpty()
        assertThat(safeHttpsUrl("  ")).isEmpty()
        assertThat(linkDomain("https://www.example.com/x")).isEqualTo("example.com")
        val unsafe = normalizeViewerEndScreens(
            Json.parseToJsonElement(
                """[{"id":"x","type":"external_link","start_ms":0,"end_ms":5,"link":{"url":"http://plain.example"}}]""",
            ),
        )
        assertThat(unsafe).isEmpty()
    }

    @Test
    fun `at most four end screens and five cards are kept, and other shapes are read`() {
        val many = (1..6).joinToString(",") { """{"id":"e$it","type":"video","start_ms":0,"end_ms":5,"video":{"id":"v$it"}}""" }
        assertThat(normalizeViewerEndScreens(Json.parseToJsonElement("""{"screens":[$many]}"""))).hasSize(4)
        assertThat(normalizeViewerEndScreens(null)).isEmpty()
        assertThat(normalizeViewerEndScreens(Json.parseToJsonElement("""{"nothing":1}"""))).isEmpty()
        val cards = (1..7).joinToString(",") { """{"id":"c$it","type":"video","appear_at_ms":${it * 1000},"title":"t$it","video":{"id":"v$it"}}""" }
        assertThat(normalizeViewerCards(Json.parseToJsonElement("[$cards]")).map { it.id }).containsExactly("c1", "c2", "c3", "c4", "c5").inOrder()
    }

    @Test
    fun `cards are sorted by time, a poll is dropped, and the teaser falls back to the title`() {
        val cards = normalizeViewerCards(
            Json.parseToJsonElement(
                """[
                  {"id":"c2","type":"external_link","appear_at_ms":30000,"title":"Buy","teaser_text":null,
                   "link":{"url":"https://shop.example","title":"Shop","domain":"shop.example"}},
                  {"id":"c1","type":"video","appear_at_ms":10000,"title":"","teaser_text":"Watch this next",
                   "video":{"id":"v9","title":"Nine"}},
                  {"id":"c3","type":"poll","appear_at_ms":5000,"title":"Vote","poll":{"post_id":"p"}},
                  {"id":"c4","type":"playlist","appear_at_ms":1000,"title":"Gone","playlist":null}
                ]""",
            ),
        )
        assertThat(cards.map { it.id }).containsExactly("c1", "c2").inOrder()
        assertThat(cards[0].title).isEqualTo("Nine")
        assertThat(cards[0].teaser).isEqualTo("Watch this next")
        assertThat(cards[1].teaser).isEqualTo("Buy")
        assertThat(cards[1].target()).isEqualTo(ElementTarget.External("https://shop.example"))
    }

    // ── Timing ─────────────────────────────────────────────────────────

    private fun element(id: String, start: Long, end: Long) =
        ViewerEndScreenElement(id, EndScreenKind.VIDEO, EndScreenPosition(0.0, 0.0, 0.3), start, end, video = EndScreenVideo(id, "", "", 0, "", 0))

    @Test
    fun `an element is up from its start until just before its end`() {
        val elements = listOf(element("a", 10_000, 20_000), element("b", 15_000, 25_000))
        assertThat(visibleEndScreenElements(elements, 9_999).map { it.id }).isEmpty()
        assertThat(visibleEndScreenElements(elements, 10_000).map { it.id }).containsExactly("a")
        assertThat(visibleEndScreenElements(elements, 15_000).map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(visibleEndScreenElements(elements, 20_000).map { it.id }).containsExactly("b")
        assertThat(visibleEndScreenElements(elements, 25_000).map { it.id }).isEmpty()
    }

    @Test
    fun `once ended, an element that runs to the end stays up over the last frame`() {
        val elements = listOf(element("toEnd", 590_000, 600_000), element("early", 100_000, 120_000), element("late", 599_800, 600_400))
        assertThat(visibleEndScreenElements(elements, 600_000, ended = true, durationMs = 600_000).map { it.id })
            .containsExactly("toEnd", "late").inOrder()
        // The player's length a little short of the post's: within 500 ms still counts.
        assertThat(visibleEndScreenElements(elements, 599_600, ended = true, durationMs = 599_600).map { it.id })
            .containsExactly("toEnd", "late").inOrder()
        assertThat(visibleEndScreenElements(listOf(element("x", 590_000, 598_000)), 600_000, ended = true, durationMs = 600_000))
            .isEmpty()
        // No duration: the clock is the end.
        assertThat(visibleEndScreenElements(elements, 600_000, ended = true).map { it.id }).containsExactly("toEnd", "late").inOrder()
    }

    @Test
    fun `after the end the duration is the clock, and an end up to 500 ms short still counts`() {
        val nearEnd = listOf(element("short", 590_000, 599_600), element("tooShort", 590_000, 599_400))
        assertThat(visibleEndScreenElements(nearEnd, 600_000, ended = true, durationMs = 600_000).map { it.id }).containsExactly("short")
        // The player stopped a second early: the post's length still decides.
        assertThat(visibleEndScreenElements(listOf(element("a", 0, 600_000)), 599_000, ended = true, durationMs = 600_000).map { it.id })
            .containsExactly("a")
        assertThat(visibleEndScreenElements(listOf(element("b", 0, 599_000)), 599_000, ended = true, durationMs = 600_000)).isEmpty()
    }

    @Test
    fun `an element with no window is dropped`() {
        val raw = Json.parseToJsonElement(
            """[{"id":"w0","type":"video","start_ms":5000,"end_ms":5000,"video":{"id":"v"}},
                {"id":"w1","type":"video","start_ms":6000,"end_ms":5000,"video":{"id":"v"}},
                {"id":"ok","type":"video","start_ms":5000,"end_ms":5001,"video":{"id":"v"}}]""",
        )
        assertThat(normalizeViewerEndScreens(raw).map { it.id }).containsExactly("ok")
    }

    @Test
    fun `a card's teaser is up for five seconds, the latest wins`() {
        fun card(id: String, at: Long) = ViewerCard(id, CardKind.VIDEO, at, id, id, video = EndScreenVideo(id, "", "", 0, "", 0))
        val cards = listOf(card("a", 10_000), card("b", 12_000))
        assertThat(teaserCardAt(cards, 9_999)).isNull()
        assertThat(teaserCardAt(cards, 10_000)?.id).isEqualTo("a")
        assertThat(teaserCardAt(cards, 12_000)?.id).isEqualTo("b")
        assertThat(teaserCardAt(cards, 16_999)?.id).isEqualTo("b")
        assertThat(teaserCardAt(cards, 17_000)).isNull()
        assertThat(teaserCardAt(listOf(card("a", 0)), 4_999)?.id).isEqualTo("a")
    }

    @Test
    fun `end screens need a frame 480dp wide, as on the web`() {
        assertThat(endScreensAllowed(479.9f)).isFalse()
        assertThat(endScreensAllowed(480f)).isTrue()
    }

    @Test
    fun `each element counts one impression per view`() {
        val log = ImpressionLog()
        assertThat(log.take(listOf("a", "b"))).containsExactly("a", "b").inOrder()
        assertThat(log.take(listOf("a", "c", ""))).containsExactly("c")
        log.reset()
        assertThat(log.take(listOf("a"))).containsExactly("a")
    }

    @Test
    fun `the frame is the biggest 16 by 9 box that fits, centred`() {
        val wide = fitFrame(1000.dp, 400.dp)
        assertThat(wide.height.value).isWithin(0.01f).of(400f)
        assertThat(wide.width.value).isWithin(0.01f).of(711.11f)
        assertThat(wide.left.value).isWithin(0.01f).of(144.44f)
        assertThat(wide.top.value).isWithin(0.01f).of(0f)
        val tall = fitFrame(360.dp, 640.dp)
        assertThat(tall.width.value).isWithin(0.01f).of(360f)
        assertThat(tall.top.value).isWithin(0.01f).of(218.75f)
        assertThat(fitFrame(0.dp, 10.dp).width.value).isEqualTo(0f)
    }

    // ── The wire ───────────────────────────────────────────────────────

    @Test
    fun `the repository reads both lists, degrades to none, and posts events to the contract's paths`() = runBlocking {
        val json = NetworkModule.provideJson()
        MockWebServer().use { server ->
            server.start()
            val api = Retrofit.Builder()
                .baseUrl(server.url("/"))
                .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
                .build()
                .create(WatchElementsApi::class.java)
            val repository = WatchElementsRepository(api, ErrorMapper(json))
            server.enqueue(MockResponse.Builder().code(200).body("""{"data":$endScreensBody}""").build())
            server.enqueue(MockResponse.Builder().code(404).body("""{"error":{"code":"NOT_FOUND"}}""").build())
            server.enqueue(MockResponse.Builder().code(204).build())

            val elements = repository.elements("p1") { it }
            repository.record("p1", ElementSurface.CARDS, "c1", ElementEvent.CLICK)

            assertThat(server.takeRequest().target).isEqualTo("/v1/posts/p1/end-screens")
            assertThat(server.takeRequest().target).isEqualTo("/v1/posts/p1/cards")
            val event = server.takeRequest()
            assertThat(event.method).isEqualTo("POST")
            assertThat(event.target).isEqualTo("/v1/posts/p1/cards/c1/click")
            assertThat(elements.endScreens.map { it.id }).containsExactly("e1", "e2", "e3", "e4").inOrder()
            assertThat(elements.cards).isEmpty()
        }
    }
}
