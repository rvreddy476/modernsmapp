// The fixtures at the bottom of this file are response bodies copied VERBATIM
// from a live capture. Wrapping them to satisfy the line-length rule would
// mean editing recorded evidence, and a reformatted fixture is no longer proof
// of what the server sent. The suppression is scoped to this file only.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
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
 * Contract tests against the payloads captured from the live stack on
 * 2026-08-16 (prompt/android-api-contracts.md §5 and §4).
 *
 * The response bodies below are copied verbatim from that capture. That is the
 * entire value of this file: it proves the DTOs deserialize the bytes the
 * server actually sends, not the bytes the handler source suggests it sends.
 * Three contract assumptions were already wrong this month — `dob` vs
 * `date_of_birth`, `/health` vs `/v1/auth/health`, `id` vs `user_id` — and
 * each was found only by hitting a real endpoint.
 *
 * When a payload changes, recapture and paste the new body here. Do not edit a
 * fixture to make a test pass.
 */
class ProfileContractTest {

    private lateinit var server: MockWebServer
    private lateinit var repository: ProfileRepository
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ProfileApi::class.java)
        repository = ProfileRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(code: Int, body: String) {
        server.enqueue(
            MockResponse.Builder()
                .code(code)
                .setHeader("Content-Type", "application/json")
                .body(body)
                .build(),
        )
    }

    @Test
    fun `public profile payload deserializes`() = runTest {
        enqueue(200, PUBLIC_PROFILE)

        val result = repository.getProfile("03b43cb4-420b-4d2d-baae-2ade11490b19")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val profile = (result as AppResult.Success).data
        assertThat(profile.userId).isEqualTo("03b43cb4-420b-4d2d-baae-2ade11490b19")
        assertThat(profile.displayName).isEqualTo("Android Contract")
        assertThat(profile.category).isEqualTo("personal")
        assertThat(profile.counts.followers).isEqualTo(0)
    }

    /**
     * The public projection genuinely omits the private fields. If this ever
     * starts returning a non-null `personal`, the public and owner DTOs have
     * been conflated and other users' birthdays are on screen.
     */
    @Test
    fun `public profile exposes no private fields`() = runTest {
        enqueue(200, PUBLIC_PROFILE)

        val profile = (repository.getProfile("u") as AppResult.Success).data

        assertThat(profile.personal).isNull()
        assertThat(profile.isOwnProfile).isFalse()
    }

    @Test
    fun `own profile payload deserializes including private fields`() = runTest {
        enqueue(200, OWN_PROFILE)

        val profile = (repository.getOwnProfile() as AppResult.Success).data

        assertThat(profile.isOwnProfile).isTrue()
        val personal = requireNotNull(profile.personal)
        assertThat(personal.firstName).isEqualTo("Android")
        assertThat(personal.lastName).isEqualTo("Contract")
        assertThat(personal.gender).isEqualTo("other")
        // Full ISO-8601 instant on read, though registration accepts yyyy-MM-dd.
        assertThat(personal.dateOfBirth).isEqualTo("1990-01-01T00:00:00Z")
    }

    @Test
    fun `stats payload deserializes`() = runTest {
        enqueue(200, STATS)

        val stats = (repository.getStats("u") as AppResult.Success).data

        assertThat(stats.totalSparks).isEqualTo(0)
        assertThat(stats.isCreator).isFalse()
        assertThat(stats.counts.posts).isEqualTo(0)
    }

    @Test
    fun `missing profile maps to NotFound`() = runTest {
        enqueue(404, """{"error":{"code":"NOT_FOUND","message":"Profile not found"}}""")

        val result = repository.getProfile("00000000-0000-0000-0000-000000000000")

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError.NotFound::class.java)
    }

    @Test
    fun `missing access token maps to an auth failure`() = runTest {
        enqueue(401, """{"error":{"code":"UNAUTHORIZED","message":"Missing access token"}}""")

        val result = repository.getOwnProfile()

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    /**
     * An account whose fields were cleared by a `PUT /me` full replacement.
     * This is a state the live stack demonstrably produces, not a hypothetical.
     */
    @Test
    fun `profile with every string cleared still deserializes`() = runTest {
        enqueue(200, CLEARED_PROFILE)

        val profile = (repository.getOwnProfile() as AppResult.Success).data

        assertThat(profile.displayName).isEmpty()
        assertThat(profile.nameForDisplay).isEqualTo("Unnamed")
    }

    @Test
    fun `follow posts the captured body shape`() = runTest {
        enqueue(200, """{"data":{"status":"followed"}}""")

        val result = repository.follow("e872a073-e1b6-4e5b-94ca-0dd31f12d93f")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/graph/follow")
        assertThat(request.body?.utf8())
            .isEqualTo("""{"user_id":"e872a073-e1b6-4e5b-94ca-0dd31f12d93f"}""")
    }

    /**
     * The asymmetry that a tidy-minded refactor would break. `DELETE
     * /v1/graph/follow` is not registered — the live probe got a plain-text
     * `404 page not found` from the router.
     */
    @Test
    fun `unfollow is a POST to its own path, not a DELETE on follow`() = runTest {
        enqueue(200, """{"data":{"status":"unfollowed"}}""")

        repository.unfollow("e872a073-e1b6-4e5b-94ca-0dd31f12d93f")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/graph/unfollow")
    }

    /** Unblock really is a DELETE, and it really does carry a body. */
    @Test
    fun `unblock is a DELETE carrying the user id body`() = runTest {
        enqueue(200, """{"data":{"status":"unblocked"}}""")

        repository.unblock("e872a073-e1b6-4e5b-94ca-0dd31f12d93f")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/graph/block")
        assertThat(request.body?.utf8())
            .isEqualTo("""{"user_id":"e872a073-e1b6-4e5b-94ca-0dd31f12d93f"}""")
    }

    @Test
    fun `graph validation failure surfaces as a failure`() = runTest {
        enqueue(
            400,
            """{"error":{"code":"INVALID_REQUEST","message":"Key: 'UserIDRequest.UserID' Error:Field validation for 'UserID' failed on the 'required' tag"}}""",
        )

        val result = repository.follow("")

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    /**
     * Unknown fields must not break deserialization. The server adds fields
     * without a client release, and a strict parser would blank the screen.
     */
    @Test
    fun `unknown response fields are ignored`() = runTest {
        enqueue(
            200,
            """{"data":{"user_id":"u","display_name":"A","a_brand_new_field":{"nested":true}}}""",
        )

        val result = repository.getProfile("u")

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat((result as AppResult.Success).data.displayName).isEqualTo("A")
    }

    private companion object {
        // Verbatim from prompt/android-api-contracts.md §5.
        const val PUBLIC_PROFILE =
            """{"data":{"user_id":"03b43cb4-420b-4d2d-baae-2ade11490b19","display_name":"Android Contract","bio":"","category":"personal","profession":"","website":"","location":"","badge_flags":0,"is_verified":false,"verification_level":"","status_text":"","status_emoji":"","profile_theme_color":"","intro_media_type":"","cta_label":"","member_since_badge":false,"follower_count":0,"following_count":0,"friend_count":0,"post_count":0,"created_at":"2026-08-16T17:53:05.762028Z"}}"""

        const val OWN_PROFILE =
            """{"data":{"user_id":"03b43cb4-420b-4d2d-baae-2ade11490b19","display_name":"Android Contract","first_name":"Android","last_name":"Contract","bio":"","dob":"1990-01-01T00:00:00Z","gender":"other","category":"personal","profession":"","website":"","location":"","badge_flags":0,"is_verified":false,"verification_level":"","status_text":"","status_emoji":"","profile_theme_color":"","intro_media_url":"","intro_media_type":"","cta_label":"","cta_url":"","member_since_badge":false,"timezone":"","follower_count":0,"following_count":0,"friend_count":0,"post_count":0,"created_at":"2026-08-16T17:53:05.762028Z","updated_at":"2026-08-16T17:53:05.762028Z"}}"""

        const val STATS =
            """{"data":{"user_id":"03b43cb4-420b-4d2d-baae-2ade11490b19","post_count":0,"follower_count":0,"following_count":0,"friend_count":0,"total_sparks":0,"is_creator":false,"updated_at":"2026-08-16T17:53:55.722992Z"}}"""

        // The documented result of PUT /me with an empty body.
        const val CLEARED_PROFILE =
            """{"data":{"user_id":"03b43cb4-420b-4d2d-baae-2ade11490b19","display_name":"","bio":"","category":"","profession":"","website":"","location":"","badge_flags":0,"is_verified":false,"verification_level":"","status_text":"","status_emoji":"","profile_theme_color":"#1A73E8","intro_media_url":"","intro_media_type":"","cta_label":"","cta_url":"","member_since_badge":false,"timezone":"","follower_count":0,"following_count":0,"friend_count":0,"post_count":0,"created_at":"2026-08-16T17:53:05.762028Z","updated_at":"2026-08-16T17:59:07.709666Z"}}"""
    }
}
