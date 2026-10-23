package com.us.android.core.feed.data.credits

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.PostEditDetailDto
import com.us.android.core.feed.data.creditsForm
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.toDomain
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * The credits read shape's EDGES (contract pinned 2026-10-23) — what no
 * golden file holds. The shapes themselves, owner versus viewer, are read
 * from post-service's fixtures in [CreditsContractFixtureTest].
 */
class CreditsContractTest {

    private val json = NetworkModule.provideJson()

    @Test
    fun `a user object sent with a pending status is not shown as a link`() {
        val stray = """{"id":"p","credits":[{"id":"c","name":"Ravi","role":"Actor","status":"pending","user_id":"u2",
            "user":{"id":"u2","username":"ravi","display_name":"Ravi"}}]}"""
        val credit = json.decodeFromString(FeedItemDto.serializer(), stray).toDomain().credits.credits.single()

        assertThat(credit.user).isNull()
        assertThat(credit.linkedUserId).isEqualTo("u2")
    }

    @Test
    fun `a post without credits, and a credit without a name, read as nothing`() {
        val bare = json.decodeFromString(FeedItemDto.serializer(), """{"id":"p","content_type":"long_video"}""")
        assertThat(bare.toDomain().credits.isEmpty).isTrue()

        val nameless = json.decodeFromString(
            FeedItemDto.serializer(),
            """{"id":"p","credits":[{"id":"c","name":"  ","role":"Actor"}],"release_year":0}""",
        )
        assertThat(nameless.toDomain().credits.isEmpty).isTrue()
    }

    /** A Go zero value or a stray scalar is "no credits", never a post that fails to decode and drops out of a list. */
    @Test
    fun `an empty string, a null or an object where a list belongs reads as none, and the post still decodes`() {
        listOf("\"\"", "null", "{}", "[]").forEach { value ->
            val raw = """{"id":"p","title":"T","content_type":"long_video","credits":$value,"genres":$value}"""

            val item = json.decodeFromString(FeedItemDto.serializer(), raw).toDomain()
            val detail = json.decodeFromString(PostEditDetailDto.serializer(), raw)

            assertThat(item.id).isEqualTo("p")
            assertThat(item.credits.credits).isEmpty()
            assertThat(item.credits.genres).isEmpty()
            assertThat(detail.creditsForm { "k$it" }.isEmpty).isTrue()
        }
    }
}
