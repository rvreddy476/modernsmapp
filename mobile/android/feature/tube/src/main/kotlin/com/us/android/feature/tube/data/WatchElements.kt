package com.us.android.feature.tube.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.doubleOrNull
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import java.net.URI
import javax.inject.Inject
import javax.inject.Singleton

/**
 * End screens and cards as a viewer sees them (web parity, 2026-10-24; the
 * web's features/posttube/watch/watchApi.ts and endScreenView.ts):
 *
 *     GET  /v1/posts/:id/end-screens                         post-service internal/http/handler.go:394
 *     GET  /v1/posts/:id/cards                               handler.go:396
 *     POST /v1/posts/:id/end-screens/:elementId/impression|click   204, handler.go:399-400
 *     POST /v1/posts/:id/cards/:cardId/impression|click            204, handler.go:401-402
 *
 * Both reads answer a bare list, resolved for this viewer
 * (service/end_screens.go:647 EndScreenElement, :678 VideoCardView); the
 * owner gets the same objects with editor fields added, which are ignored.
 * A made-for-kids video answers [] (end_screens.go:727). Read raw, as the
 * web reads them, so one odd element is dropped rather than the whole list.
 */
interface WatchElementsApi {

    @GET("v1/posts/{postId}/end-screens")
    suspend fun endScreens(@Path("postId") postId: String): ApiEnvelope<JsonElement>

    @GET("v1/posts/{postId}/cards")
    suspend fun cards(@Path("postId") postId: String): ApiEnvelope<JsonElement>

    /** [surface] is `end-screens` or `cards`; [event] is `impression` or `click`. 204, no body. */
    @POST("v1/posts/{postId}/{surface}/{elementId}/{event}")
    suspend fun record(
        @Path("postId") postId: String,
        @Path("surface") surface: String,
        @Path("elementId") elementId: String,
        @Path("event") event: String,
    )
}

// ── Geometry (the web's endScreenGeometry.ts; the server's end_screens.go:131-200) ──

/** What an end-screen element links to. */
enum class EndScreenKind(val wire: String) {
    VIDEO("video"),
    PLAYLIST("playlist"),
    CHANNEL_SUBSCRIBE("channel_subscribe"),
    CHANNEL("channel"),
    EXTERNAL_LINK("external_link"),
    ;

    /** Subscribe and channel elements are circles of diameter w; the rest are 16:9 tiles. */
    val isCircle: Boolean get() = this == CHANNEL_SUBSCRIBE || this == CHANNEL

    companion object {
        fun of(raw: String): EndScreenKind? = entries.firstOrNull { it.wire == raw.trim().lowercase() }
    }
}

/** A box's top-left corner and width as fractions of the 16:9 frame; the height follows from the kind. */
data class EndScreenPosition(val x: Double, val y: Double, val w: Double)

/** A box in frame fractions, height included. */
data class FrameBox(val x: Double, val y: Double, val w: Double, val h: Double)

const val FRAME_ASPECT = 16.0 / 9.0
const val MIN_ELEMENT_W = 0.12
const val MAX_ELEMENT_W = 0.5
private const val SLOT_INSET_X = 0.05
private const val SLOT_INSET_Y = 0.10
private const val ROUND = 10_000.0

/** A new element's width: a circle 0.2, a tile 0.3 (end_screens.go:144-145). */
fun defaultWidth(kind: EndScreenKind): Double = if (kind.isCircle) 0.2 else 0.3

/** The element's height as a fraction of the frame's: a tile is w, a circle w × 16/9. */
fun heightFraction(kind: EndScreenKind, w: Double): Double = if (kind.isCircle) w * FRAME_ASPECT else w

fun boxOf(kind: EndScreenKind, position: EndScreenPosition): FrameBox =
    FrameBox(position.x, position.y, position.w, heightFraction(kind, position.w))

private fun round4(value: Double): Double = Math.round(value * ROUND) / ROUND

/** Where an old `{slot:n}` row sits (n mod 4: 0 top-left, 1 top-right, 2 bottom-left, 3 bottom-right). */
fun slotPosition(slot: Int, kind: EndScreenKind): EndScreenPosition {
    val s = ((slot % 4) + 4) % 4
    val w = defaultWidth(kind)
    val h = heightFraction(kind, w)
    val right = s == 1 || s == 3
    val bottom = s == 2 || s == 3
    return EndScreenPosition(
        x = round4(if (right) 1 - SLOT_INSET_X - w else SLOT_INSET_X),
        y = round4(if (bottom) maxOf(0.0, 1 - SLOT_INSET_Y - h) else SLOT_INSET_Y),
        w = w,
    )
}

/** The width into 0.12..0.5 (short enough that a circle fits), then the corner inside the frame. */
fun clampPosition(kind: EndScreenKind, position: EndScreenPosition): EndScreenPosition {
    val maxW = if (kind.isCircle) minOf(MAX_ELEMENT_W, 1 / FRAME_ASPECT) else MAX_ELEMENT_W
    val w = (if (position.w.isFinite()) position.w else defaultWidth(kind)).coerceIn(MIN_ELEMENT_W, maxW)
    val h = heightFraction(kind, w)
    return EndScreenPosition(
        x = round4((if (position.x.isFinite()) position.x else 0.0).coerceIn(0.0, 1 - w)),
        y = round4((if (position.y.isFinite()) position.y else 0.0).coerceIn(0.0, 1 - h)),
        w = round4(w),
    )
}

// ── What the viewer is shown ─────────────────────────────────────────────

data class EndScreenVideo(
    val id: String,
    val title: String,
    val thumbnailUrl: String,
    val durationSeconds: Long,
    val channelName: String,
    val viewCount: Long,
)

data class EndScreenCollection(val id: String, val title: String, val thumbnailUrl: String, val itemCount: Long)

data class EndScreenChannel(
    val userId: String,
    val handle: String,
    val name: String,
    val avatarUrl: String,
    val subscriberCount: Long,
    val isSubscribed: Boolean,
)

data class EndScreenLink(val url: String, val title: String, val domain: String)

/** One end-screen element, always resolved: exactly the target its kind needs. */
data class ViewerEndScreenElement(
    val id: String,
    val kind: EndScreenKind,
    val position: EndScreenPosition,
    val startMs: Long,
    val endMs: Long,
    val video: EndScreenVideo? = null,
    val playlist: EndScreenCollection? = null,
    val channel: EndScreenChannel? = null,
    val link: EndScreenLink? = null,
)

/** What a card links to; poll cards are dropped (there is no poll surface on the watch page). */
enum class CardKind(val wire: String) { VIDEO("video"), PLAYLIST("playlist"), EXTERNAL_LINK("external_link") }

data class ViewerCard(
    val id: String,
    val kind: CardKind,
    val appearAtMs: Long,
    val title: String,
    val teaser: String,
    val video: EndScreenVideo? = null,
    val playlist: EndScreenCollection? = null,
    val link: EndScreenLink? = null,
)

/** What a tap on an element or a card opens. */
sealed interface ElementTarget {
    data class Video(val postId: String) : ElementTarget
    data class Collection(val collectionId: String) : ElementTarget
    data class Channel(val userId: String, val handle: String) : ElementTarget

    /** An https link, already checked; opened outside the app. */
    data class External(val url: String) : ElementTarget
}

fun ViewerEndScreenElement.target(): ElementTarget? = when {
    video != null -> ElementTarget.Video(video.id)
    playlist != null -> ElementTarget.Collection(playlist.id)
    channel != null -> ElementTarget.Channel(channel.userId, channel.handle)
    link != null -> ElementTarget.External(link.url)
    else -> null
}

fun ViewerCard.target(): ElementTarget? = when {
    video != null -> ElementTarget.Video(video.id)
    playlist != null -> ElementTarget.Collection(playlist.id)
    link != null -> ElementTarget.External(link.url)
    else -> null
}

// ── Timing (the web's endScreenView.ts) ──────────────────────────────────

/** The player's length and the post's differ by a frame or two; end_ms may run to duration + 500. */
const val END_TOLERANCE_MS = 500L

/** A card's teaser stays up this long from its appear time. */
const val CARD_TEASER_MS = 5_000L

/** End screens are not drawn on frames narrower than this (the web's phones and miniplayer); cards still are. */
const val END_SCREEN_MIN_FRAME_WIDTH_DP = 480

fun endScreensAllowed(frameWidthDp: Float): Boolean = frameWidthDp >= END_SCREEN_MIN_FRAME_WIDTH_DP

/**
 * The elements on screen at [positionMs]: start ≤ t < end. Once the video
 * has ended the clock sits on the duration, so an element that runs to the
 * end (end ≥ duration − 500 ms) stays up over the last frame.
 */
fun visibleEndScreenElements(
    elements: List<ViewerEndScreenElement>,
    positionMs: Long,
    ended: Boolean = false,
    durationMs: Long = 0L,
): List<ViewerEndScreenElement> {
    if (ended) {
        val d = if (durationMs > 0) durationMs else positionMs
        return elements.filter { it.startMs <= d + END_TOLERANCE_MS && it.endMs >= d - END_TOLERANCE_MS }
    }
    return elements.filter { positionMs >= it.startMs && positionMs < it.endMs }
}

/** The card whose teaser is up at [positionMs]: the latest with appear ≤ t < appear + 5 s. */
fun teaserCardAt(cards: List<ViewerCard>, positionMs: Long): ViewerCard? {
    var hit: ViewerCard? = null
    for (card in cards) {
        if (positionMs >= card.appearAtMs && positionMs < card.appearAtMs + CARD_TEASER_MS) {
            if (hit == null || card.appearAtMs >= hit.appearAtMs) hit = card
        }
    }
    return hit
}

/** One impression per element per view: [take] answers only the ids not seen before, and remembers them. */
class ImpressionLog {
    private val seen = mutableSetOf<String>()

    fun take(ids: List<String>): List<String> = ids.filter { it.isNotBlank() && seen.add(it) }

    fun reset() = seen.clear()
}

// ── Reading the wire, the way the web reads it ───────────────────────────

private fun JsonElement?.obj(): JsonObject? = this as? JsonObject

private fun JsonElement?.text(): String = (this as? JsonPrimitive)?.takeIf { it.isString }?.content?.trim().orEmpty()

/** A finite number, or a numeric string; null for "", null, missing and anything else. */
private fun JsonElement?.finite(): Double? {
    val p = this as? JsonPrimitive ?: return null
    if (p is JsonNull) return null
    val value = if (p.isString) p.content.trim().takeIf { it.isNotEmpty() }?.toDoubleOrNull() else p.doubleOrNull
    return value?.takeIf { it.isFinite() }
}

private fun JsonElement?.count(): Long = finite()?.takeIf { it > 0 }?.toLong() ?: 0L

private fun listFrom(raw: JsonElement?, vararg keys: String): List<JsonElement> {
    if (raw is JsonArray) return raw
    val o = raw.obj() ?: return emptyList()
    return keys.firstNotNullOfOrNull { o[it] as? JsonArray } ?: emptyList()
}

/** An https URL with a host, normalised; "" for anything else (http, javascript:, junk). */
fun safeHttpsUrl(raw: String): String {
    val value = raw.trim().takeIf { it.isNotEmpty() } ?: return ""
    val uri = runCatching { URI(value) }.getOrNull() ?: return ""
    return if (uri.scheme.equals("https", ignoreCase = true) && !uri.host.isNullOrBlank()) uri.toString() else ""
}

/** The link's host without `www.`. */
fun linkDomain(url: String): String = runCatching { URI(url).host.orEmpty().removePrefix("www.") }.getOrDefault("")

/**
 * A stored position → `{x, y, w}`. The object, or its JSON string. `{x, y}`
 * is read as is (a missing or zero w takes the kind's default); an old
 * `{slot: n}` sits in that corner; anything else falls back to the slot of
 * its index. The result is kept inside the frame.
 */
fun readEndScreenPosition(raw: JsonElement?, kind: EndScreenKind, index: Int): EndScreenPosition {
    val parsed = if (raw is JsonPrimitive && raw.isString) {
        runCatching { Json.parseToJsonElement(raw.content) }.getOrNull()
    } else {
        raw
    }
    val o = parsed.obj()
    if (o != null) {
        val x = o["x"].finite()
        val y = o["y"].finite()
        val w = o["w"].finite()
        if (x != null && y != null) return clampPosition(kind, EndScreenPosition(x, y, w?.takeIf { it > 0 } ?: defaultWidth(kind)))
        val slot = o["slot"].finite()
        if (slot != null) return slotPosition(slot.toInt(), kind)
    }
    return slotPosition(index, kind)
}

/** The gateway-relative media paths the server sends (`/v1/…`) made absolute; anything else as sent. */
fun interface MediaHref {
    fun resolve(raw: String): String
}

/** The web's `mediaUrl`: only a gateway path (`/v1/…`) is resolved; an absolute URL is used as sent. */
private fun JsonElement?.media(href: MediaHref): String {
    val raw = text()
    return when {
        raw.isEmpty() -> ""
        raw.startsWith("/v1/") -> href.resolve(raw)
        else -> raw
    }
}

private fun JsonElement?.video(href: MediaHref): EndScreenVideo? {
    val o = obj() ?: return null
    val id = o["id"].text().takeIf { it.isNotEmpty() } ?: return null
    return EndScreenVideo(
        id = id,
        title = o["title"].text().ifEmpty { "Untitled" },
        thumbnailUrl = o["thumbnail_url"].media(href),
        durationSeconds = o["duration_seconds"].count(),
        channelName = o["channel_name"].text(),
        viewCount = o["view_count"].count(),
    )
}

private fun JsonElement?.collection(href: MediaHref): EndScreenCollection? {
    val o = obj() ?: return null
    val id = o["id"].text().takeIf { it.isNotEmpty() } ?: return null
    return EndScreenCollection(
        id = id,
        title = o["title"].text().ifEmpty { "Untitled collection" },
        thumbnailUrl = o["thumbnail_url"].media(href),
        itemCount = o["item_count"].count(),
    )
}

private fun JsonElement?.channel(href: MediaHref): EndScreenChannel? {
    val o = obj() ?: return null
    val userId = o["user_id"].text().ifEmpty { o["owner_id"].text() }.ifEmpty { o["id"].text() }
    val handle = o["handle"].text().removePrefix("@")
    if (userId.isEmpty() && handle.isEmpty()) return null
    return EndScreenChannel(
        userId = userId,
        handle = handle,
        name = o["name"].text().ifEmpty { if (handle.isNotEmpty()) "@$handle" else "Channel" },
        avatarUrl = o["avatar_url"].media(href),
        subscriberCount = o["subscriber_count"].count(),
        isSubscribed = (o["is_subscribed"] as? JsonPrimitive)?.booleanOrNull == true,
    )
}

private fun JsonElement?.link(): EndScreenLink? {
    val o = obj() ?: return null
    val url = safeHttpsUrl(o["url"].text()).takeIf { it.isNotEmpty() } ?: return null
    val domain = o["domain"].text().ifEmpty { linkDomain(url) }
    return EndScreenLink(url = url, title = o["title"].text().ifEmpty { domain }, domain = domain)
}

/**
 * `GET …/end-screens` → the elements to draw. A bare list or
 * `{screens|elements|items:[…]}`. An element with no id, an unknown type, no
 * window (end ≤ start) or without the resolved object its type needs is
 * dropped; at most four are kept.
 */
fun normalizeViewerEndScreens(raw: JsonElement?, href: MediaHref = MediaHref { it }): List<ViewerEndScreenElement> =
    listFrom(raw, "screens", "elements", "items").mapIndexedNotNull { index, entry ->
        val o = entry.obj() ?: return@mapIndexedNotNull null
        val id = o["id"].text().takeIf { it.isNotEmpty() } ?: return@mapIndexedNotNull null
        val kind = EndScreenKind.of(o["type"].text()) ?: return@mapIndexedNotNull null
        val startMs = o["start_ms"].count()
        val endMs = o["end_ms"].count()
        if (endMs <= startMs) return@mapIndexedNotNull null
        val element = ViewerEndScreenElement(
            id = id,
            kind = kind,
            position = readEndScreenPosition(o["position"], kind, index),
            startMs = startMs,
            endMs = endMs,
            video = if (kind == EndScreenKind.VIDEO) o["video"].video(href) else null,
            playlist = if (kind == EndScreenKind.PLAYLIST) (o["playlist"] ?: o["collection"]).collection(href) else null,
            channel = if (kind.isCircle) o["channel"].channel(href) else null,
            link = if (kind == EndScreenKind.EXTERNAL_LINK) o["link"].link() else null,
        )
        element.takeIf { it.target() != null }
    }.take(MAX_END_SCREENS)

/**
 * `GET …/cards` → the cards, by time. Poll cards are dropped, as is any card
 * whose target did not resolve; at most five. The teaser falls back to the
 * title, the title to the target's.
 */
fun normalizeViewerCards(raw: JsonElement?, href: MediaHref = MediaHref { it }): List<ViewerCard> =
    listFrom(raw, "cards", "items").mapNotNull { entry ->
        val o = entry.obj() ?: return@mapNotNull null
        val id = o["id"].text().takeIf { it.isNotEmpty() } ?: return@mapNotNull null
        val kind = CardKind.entries.firstOrNull { it.wire == o["type"].text().lowercase() } ?: return@mapNotNull null
        val video = if (kind == CardKind.VIDEO) o["video"].video(href) else null
        val playlist = if (kind == CardKind.PLAYLIST) (o["playlist"] ?: o["collection"]).collection(href) else null
        val link = if (kind == CardKind.EXTERNAL_LINK) o["link"].link() else null
        if (video == null && playlist == null && link == null) return@mapNotNull null
        val title = o["title"].text().ifEmpty { video?.title ?: playlist?.title ?: link?.title.orEmpty() }
        ViewerCard(
            id = id,
            kind = kind,
            appearAtMs = o["appear_at_ms"].count(),
            title = title,
            teaser = o["teaser_text"].text().ifEmpty { title },
            video = video,
            playlist = playlist,
            link = link,
        )
    }.sortedBy { it.appearAtMs }.take(MAX_CARDS)

private const val MAX_END_SCREENS = 4
private const val MAX_CARDS = 5

/** A video's authored elements: what shows in its last seconds, and the cards along the way. */
data class WatchElements(
    val postId: String,
    val endScreens: List<ViewerEndScreenElement> = emptyList(),
    val cards: List<ViewerCard> = emptyList(),
)

/** Which list an event is for, and which event. */
enum class ElementSurface(val wire: String) { END_SCREENS("end-screens"), CARDS("cards") }

enum class ElementEvent(val wire: String) { IMPRESSION("impression"), CLICK("click") }

/**
 * The watch page's elements. Every failure — a 404, an age code, a route not
 * deployed — is "none": the overlay is never an error.
 */
@Singleton
class WatchElementsRepository @Inject constructor(
    private val api: WatchElementsApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun elements(postId: String, href: MediaHref): WatchElements {
        val screens = (apiCall(errorMapper) { api.endScreens(postId) } as? AppResult.Success)?.data
        val cards = (apiCall(errorMapper) { api.cards(postId) } as? AppResult.Success)?.data
        return WatchElements(
            postId = postId,
            endScreens = normalizeViewerEndScreens(screens, href),
            cards = normalizeViewerCards(cards, href),
        )
    }

    /** Fire-and-forget: the answer is never waited on by navigation, and a failure is not said. */
    suspend fun record(postId: String, surface: ElementSurface, elementId: String, event: ElementEvent) {
        noContentApiCall(errorMapper) { api.record(postId, surface.wire, elementId, event.wire) }
    }
}
