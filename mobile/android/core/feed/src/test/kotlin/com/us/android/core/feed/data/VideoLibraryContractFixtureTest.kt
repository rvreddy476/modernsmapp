package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.network.di.NetworkModule
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Watch later, collections and the viewer's state on a long video, against
 * post-service's golden files (`internal/http/testdata/contracts/mtube`),
 * copied byte for byte into `src/test/resources/contracts/mtube`.
 *
 * What this protects: a key renamed on either side. The three small shapes
 * the app claims WHOLE (a collection row, the Watch later answer) decode
 * STRICTLY, so a key the server adds or renames fails here rather than
 * defaulting silently in production. The post detail is decoded with the
 * platform Json: `FeedItemDto` reads a post, it does not claim every key.
 *
 * The post detail is the fixture that matters most: `is_bookmarked`,
 * `viewer_queued` and `viewer_disliked` are what make Saved, "In Watch
 * later" and Dislike right when a video is opened again.
 */
class VideoLibraryContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }
    private val app = NetworkModule.provideJson()

    private val contractsDir = File("src/test/resources/contracts/mtube")

    private val parsers: Map<String, (raw: String) -> Unit> = mapOf(
        // GET /v1/playlists/{id}, POST /v1/playlists, and each row of GET /v1/creators/{id}/playlists.
        "playlist.json" to { raw ->
            val dto = strict.decodeFromString(PlaylistDto.serializer(), raw)
            assertThat(dto.kind).isEqualTo("user")

            val collection = dto.toCollection()
            assertThat(collection).isEqualTo(
                VideoCollection(
                    id = "88888888-8888-4888-8888-888888888888",
                    title = "Build logs",
                    itemCount = 4,
                    isPrivate = false,
                    isWatchLater = false,
                ),
            )
        },
        // GET /v1/playlists/system/watch_later: the server's row title is "Queue"; the app says "Watch later".
        "system_playlist.json" to { raw ->
            val dto = strict.decodeFromString(PlaylistDto.serializer(), raw)
            assertThat(dto.kind).isEqualTo(VideoLibraryApi.KIND_WATCH_LATER)
            assertThat(dto.title).isEqualTo("Queue")

            val collection = dto.toCollection()
            assertThat(collection.isWatchLater).isTrue()
            assertThat(collection.title).isEqualTo("Watch later")
            assertThat(collection.isPrivate).isTrue()
            assertThat(collection.itemCount).isEqualTo(1)
        },
        // POST | DELETE /v1/posts/{id}/watch-later.
        "watch_later.json" to { raw ->
            assertThat(strict.decodeFromString(WatchLaterDto.serializer(), raw).queued).isTrue()
        },
        // GET /v1/posts/{id}: the viewer's state the watch screen reads on open.
        "post_detail.json" to { raw ->
            val keys = app.parseToJsonElement(raw).jsonObject.keys
            assertThat(keys).containsAtLeast("is_bookmarked", "has_reacted", "viewer_queued", "viewer_disliked")

            val dto = app.decodeFromString(FeedItemDto.serializer(), raw)
            assertThat(dto.viewerQueued).isTrue()
            assertThat(dto.viewerDisliked).isFalse()
            assertThat(dto.isBookmarked).isFalse()

            val viewer = dto.toDomain().viewer
            assertThat(viewer.isQueued).isTrue()
            assertThat(viewer.hasDisliked).isFalse()
            assertThat(viewer.isBookmarked).isFalse()
            assertThat(viewer.hasReacted).isFalse()
        },
    )

    private fun fixtures(): Set<String> =
        contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()

    @Test
    fun `every fixture has a parser and every parser a fixture`() {
        assertThat(fixtures()).isNotEmpty()
        assertThat(fixtures() - parsers.keys).isEmpty()
        assertThat(parsers.keys - fixtures()).isEmpty()
    }

    @Test
    fun `every fixture decodes into its DTO and maps into the domain`() {
        for ((name, parse) in parsers) {
            val raw = File(contractsDir, name).readText()
            try {
                parse(raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed: ${e.message}", e)
            }
        }
    }

    @Test
    fun `the copies are byte-identical to post-service's goldens`() {
        val source = File("../../../../Architecture/services/post-service/internal/http/testdata/contracts/mtube")
        assumeTrue("post-service is not checked out beside the app", source.isDirectory)

        for (name in parsers.keys) {
            val original = File(source, name)
            assertThat(original.exists()).isTrue()
            assertThat(File(contractsDir, name).readBytes()).isEqualTo(original.readBytes())
        }
    }

    /** A viewer-state key that is true on the wire is true in the domain: each of the four, alone. */
    @Test
    fun `each viewer flag on the post detail reaches the domain on its own`() {
        fun viewer(body: String) = app.decodeFromString(FeedItemDto.serializer(), body).toDomain().viewer

        assertThat(viewer("""{"id":"p","is_bookmarked":true}""").isBookmarked).isTrue()
        assertThat(viewer("""{"id":"p","viewer_queued":true}""").isQueued).isTrue()
        assertThat(viewer("""{"id":"p","viewer_disliked":true}""").hasDisliked).isTrue()
        assertThat(viewer("""{"id":"p","has_reacted":true}""").hasReacted).isTrue()

        // A list row that carries none of them reads as "not", never as a crash.
        val bare = viewer("""{"id":"p"}""")
        assertThat(bare.isBookmarked).isFalse()
        assertThat(bare.isQueued).isFalse()
        assertThat(bare.hasDisliked).isFalse()
    }
}
