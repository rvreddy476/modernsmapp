package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowCounts
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.ProfileCounts
import com.us.android.core.model.withGraph
import com.us.android.core.network.ErrorMapper
import com.us.android.core.profile.data.dto.GraphCountsDto
import com.us.android.core.profile.data.dto.RelationshipDto
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
 * T-20 and the relationship block fields (2026-10-15): the live follow counts
 * come from graph-service, and the block direction is read from the named
 * fields first, the legacy pair second.
 */
class GraphCountsAndBlockTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
    }
    private lateinit var server: MockWebServer
    private lateinit var counts: GraphCountsRepository
    private lateinit var profiles: ProfileRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        counts = GraphCountsRepository(retrofit.create(GraphCountsApi::class.java), ErrorMapper(json))
        profiles = ProfileRepository(retrofit.create(ProfileApi::class.java), ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String) {
        server.enqueue(
            MockResponse.Builder().code(200).setHeader("Content-Type", "application/json").body(body).build(),
        )
    }

    // ── Counts ───────────────────────────────────────────────────────────

    @Test
    fun `counts are read from the graph route and mapped`() = runTest {
        enqueue(
            """{"data":{"user_id":"u1","follower_count":12,"following_count":3,"friend_count":2,""" +
                """"updated_at":"2026-10-15T10:00:00Z"}}""",
        )

        val result = counts.counts("u1")

        assertThat(server.takeRequest().target).isEqualTo("/v1/graph/counts/u1")
        assertThat((result as AppResult.Success).data)
            .isEqualTo(FollowCounts(followers = 12, following = 3, friends = 2))
    }

    @Test
    fun `a count beyond Int is clamped, never wrapped negative`() {
        val mapped = GraphCountsDto(followerCount = Long.MAX_VALUE, followingCount = -4).toDomain()

        assertThat(mapped.followers).isEqualTo(Int.MAX_VALUE)
        assertThat(mapped.following).isEqualTo(0)
    }

    @Test
    fun `the graph counts replace the stale follow counts and keep the post count`() {
        val stale = ProfileCounts(followers = 1, following = 1, friends = 0, posts = 42)

        val merged = stale.withGraph(FollowCounts(followers = 9, following = 4, friends = 2))

        assertThat(merged).isEqualTo(ProfileCounts(followers = 9, following = 4, friends = 2, posts = 42))
        assertThat(stale.withGraph(null)).isEqualTo(stale)
    }

    // ── Block direction ──────────────────────────────────────────────────

    @Test
    fun `the named block fields win over the legacy pair`() {
        val rel = RelationshipDto(
            blocked = false,
            blockedBy = false,
            viewerBlockedTarget = true,
            targetBlockedViewer = true,
        ).toDomain()

        assertThat(rel.isBlocked).isTrue()
        assertThat(rel.isBlockedByTarget).isTrue()
    }

    @Test
    fun `a named false is believed even when a legacy key says true`() {
        val rel = RelationshipDto(
            blocked = true,
            blockedBy = true,
            viewerBlockedTarget = false,
            targetBlockedViewer = false,
        ).toDomain()

        assertThat(rel.isBlocked).isFalse()
        assertThat(rel.isBlockedByTarget).isFalse()
    }

    /** On this route `blocked_by` is the viewer's block and `blocked` the target's. */
    @Test
    fun `an older server falls back to the legacy pair in this route's directions`() {
        val viewerBlocked = RelationshipDto(blockedBy = true).toDomain()
        val targetBlocked = RelationshipDto(blocked = true).toDomain()

        assertThat(viewerBlocked.isBlocked).isTrue()
        assertThat(viewerBlocked.isBlockedByTarget).isFalse()
        assertThat(targetBlocked.isBlocked).isFalse()
        assertThat(targetBlocked.isBlockedByTarget).isTrue()
    }

    @Test
    fun `the relationship route decodes the named fields off the wire`() = runTest {
        enqueue(
            """{"data":{"follows":false,"followed_by":false,"blocked":true,"blocked_by":false,""" +
                """"viewer_blocked_target":false,"target_blocked_viewer":true,"is_private":false,""" +
                """"follow_request_status":"none"}}""",
        )

        val rel = (profiles.relationship("viewer", "target") as AppResult.Success).data

        assertThat(rel.isBlockedByTarget).isTrue()
        assertThat(rel.isBlocked).isFalse()
        assertThat(rel.followStatus).isEqualTo(FollowStatus.NONE)
    }

    @Test
    fun `a payload without the named fields still maps through the fallback`() = runTest {
        enqueue("""{"data":{"follows":true,"blocked":false,"blocked_by":true}}""")

        val rel = (profiles.relationship("viewer", "target") as AppResult.Success).data

        assertThat(rel.isBlocked).isTrue()
        assertThat(rel.isBlockedByTarget).isFalse()
        assertThat(rel.followStatus).isEqualTo(FollowStatus.FOLLOWING)
    }
}
