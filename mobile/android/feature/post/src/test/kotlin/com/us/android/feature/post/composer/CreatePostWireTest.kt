package com.us.android.feature.post.composer

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.post.data.PostApi
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.feature.post.data.dto.POST_TYPE_IMAGE
import com.us.android.feature.post.data.dto.SupportedAudience
import com.us.android.feature.post.data.dto.VISIBILITY_PUBLIC
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * What the composer actually puts on the wire — C-LB-6.
 *
 * ## WHY THE PRODUCTION Json CONFIGURATION IS MIRRORED
 *
 * These tests build the same `Json` that `NetworkModule.provideJson()` builds,
 * with `encodeDefaults` OFF. A test that switched it on would pass while the
 * app kept failing, which is precisely the trap this project has already fallen
 * into twice — `SendMessageRequest.type` (every chat send a 400) and
 * `ResendVerificationRequestDto.type` (account recovery broken). Both were
 * caught only on a real device.
 *
 * ## WHY MockWebServer AND NOT ONLY THE SERIALIZER
 *
 * Serializing a DTO directly proves the serializer works. It does not prove
 * Retrofit sends that body, nor that the `Idempotency-Key` header is attached.
 * The second half of these tests reads the request MockWebServer actually
 * received.
 */
class CreatePostWireTest {

    /** Mirrors NetworkModule.provideJson(). Deliberately NOT `encodeDefaults`. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var api: PostApi

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(PostApi::class.java)
    }

    @After
    fun tearDown() = server.close()

    private fun textRequest() = CreatePostRequest(
        text = "hello from the composer",
        language = "en",
        distribution = DistributionRequest(),
    )

    private fun imageRequest() = CreatePostRequest(
        text = "a photo",
        postType = POST_TYPE_IMAGE,
        mediaIds = listOf("11111111-2222-3333-4444-555555555555"),
        language = "en",
        distribution = DistributionRequest(),
    )

    // ── Serialized shape ────────────────────────────────────────────────

    /**
     * The exact key set for a text post.
     *
     * Exact, not `contains`: an EXTRA key is as much a contract break as a
     * missing one. Slice C is capped, and a field appearing here that nobody
     * decided to send is how an unenforced control reaches the server.
     */
    @Test
    fun `a text post carries exactly the capped field set`() {
        val obj = json.encodeToString(textRequest()).let { json.parseToJsonElement(it).jsonObject }

        assertThat(obj.keys).containsExactly(
            "text",
            "visibility",
            "content_type",
            "post_type",
            "app_origin",
            "media_ids",
            "language",
            "distribution",
        )
        assertThat(obj["visibility"]!!.jsonPrimitive.content).isEqualTo("public")
        assertThat(obj["content_type"]!!.jsonPrimitive.content).isEqualTo("post")
        assertThat(obj["post_type"]!!.jsonPrimitive.content).isEqualTo("text")
        assertThat(obj["app_origin"]!!.jsonPrimitive.content).isEqualTo("postbook")
        assertThat(obj["language"]!!.jsonPrimitive.content).isEqualTo("en")
    }

    /**
     * `visibility` must survive `encodeDefaults = false`.
     *
     * The server binds it `required`, so its disappearance is not a subtle
     * degradation — every publish becomes a 400. NC-C6A removes the
     * `@EncodeDefault` and this test is what fails.
     */
    @Test
    fun `visibility is on the wire even though it equals its default`() {
        assertThat(json.encodeToString(textRequest())).contains("\"visibility\":\"public\"")
    }

    /** An empty media array is SENT, not omitted. */
    @Test
    fun `media_ids is emitted as an empty array for a text post`() {
        assertThat(json.encodeToString(textRequest())).contains("\"media_ids\":[]")
    }

    @Test
    fun `an image post carries its single media id and image post_type`() {
        val obj = json.encodeToString(imageRequest()).let { json.parseToJsonElement(it).jsonObject }

        assertThat(obj["post_type"]!!.jsonPrimitive.content).isEqualTo("image")
        assertThat(json.encodeToString(imageRequest()))
            .contains("\"media_ids\":[\"11111111-2222-3333-4444-555555555555\"]")
    }

    /**
     * The distribution policy, including its explicit `false` values.
     *
     * `notify_subscribers` omitted is NOT false — the server falls back to the
     * legacy `true`, and an ordinary photo post would notify every subscriber.
     * NC-C6B removes the encode protection and this is the test that fails.
     */
    @Test
    fun `the distribution policy sends every field including the false ones`() {
        val obj = json.encodeToString(DistributionRequest())
            .let { json.parseToJsonElement(it).jsonObject }

        assertThat(obj.keys).containsExactly(
            "version",
            "main_feed",
            "notify_subscribers",
            "create_reel_preview",
        )
        assertThat(obj["version"]!!.jsonPrimitive.content).isEqualTo("1")
        assertThat(obj["main_feed"]!!.jsonPrimitive.content).isEqualTo("true")
        assertThat(obj["notify_subscribers"]!!.jsonPrimitive.content).isEqualTo("false")
        assertThat(obj["create_reel_preview"]!!.jsonPrimitive.content).isEqualTo("false")
    }

    /** No excluded feature may reach the wire, even as a null. */
    @Test
    fun `no out-of-scope field appears in the request`() {
        val body = json.encodeToString(imageRequest())

        for (excluded in listOf(
            "poll", "location_name", "location_lat", "location_lng", "feeling",
            "activity", "rich_text", "tags", "category", "seo_title",
            "paid_promotion", "altered_content", "is_made_for_kids", "license",
            "remix_setting", "publish_to_feed", "share_to_postbook", "title",
            "cover_media_id", "audio_track_id", "no_comments", "no_likes",
        )) {
            assertThat(body).doesNotContain("\"$excluded\"")
        }
    }

    // ── The request Retrofit really sends ───────────────────────────────

    /**
     * The body and the header, read off the wire.
     *
     * This is the half that a serializer-only test cannot prove: that Retrofit
     * attaches `Idempotency-Key`, and that the body it sends is the body we
     * encoded.
     */
    @Test
    fun `retrofit sends the encoded body and the idempotency key header`() = runBlocking {
        server.enqueue(
            MockResponse.Builder()
                .code(201)
                .body("""{"data":{"id":"post-1"}}""")
                .setHeader("Content-Type", "application/json")
                .build(),
        )

        api.createPost("6d5707d0-1f1c-4bea-b48b-e4d343f24d5e", textRequest())

        val recorded = server.takeRequest()
        assertThat(recorded.method).isEqualTo("POST")
        assertThat(recorded.target).isEqualTo("/v1/posts")
        assertThat(recorded.headers["Idempotency-Key"])
            .isEqualTo("6d5707d0-1f1c-4bea-b48b-e4d343f24d5e")

        val sent = json.parseToJsonElement(recorded.body!!.utf8()).jsonObject
        assertThat(sent["visibility"]!!.jsonPrimitive.content).isEqualTo("public")
        assertThat(sent["distribution"]!!.jsonObject["notify_subscribers"]!!.jsonPrimitive.content)
            .isEqualTo("false")
    }

    // ── Audience allowlist (C-LB-2.5) ───────────────────────────────────

    /**
     * The audience allowlist: public, followers, private — exactly.
     *
     * The allowlist is a guarded constant rather than a hardcoded string in a
     * dropdown, so widening it is a visible change to something a test owns.
     * `followers` and `private` joined 2026-09-01 alongside post-service's
     * read-path enforcement (direct-link gate in GetPost, profile-grid filter
     * in GetPostsByAuthor) — the engagement, feed-batch and repost gates
     * already existed. `unlisted` stays out: no surface explains it, and an
     * audience the author cannot reason about is not a choice.
     */
    @Test
    fun `the audience allowlist is exactly the enforced set`() {
        assertThat(SupportedAudience).containsExactly(
            VISIBILITY_PUBLIC,
            com.us.android.feature.post.data.dto.VISIBILITY_FOLLOWERS,
            com.us.android.feature.post.data.dto.VISIBILITY_PRIVATE,
        )
    }

    @Test
    fun `the composer never sends an unsupported audience`() {
        assertThat(SupportedAudience).contains(textRequest().visibility)
        assertThat(SupportedAudience).contains(imageRequest().visibility)
    }
}
