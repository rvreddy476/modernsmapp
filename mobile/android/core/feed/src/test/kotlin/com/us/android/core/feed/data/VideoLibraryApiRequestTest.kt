package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import mockwebserver3.RecordedRequest
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Watch later, the dislike and collections, byte for byte: the method, the
 * path and the body of every call, as the web makes them and as
 * post-service's routes take them (`handler.go`: `/:postId/watch-later`,
 * `/:postId/tune`, the `/v1/playlists` group).
 *
 * The repository is driven through the real Retrofit stack, so what is
 * pinned is what actually goes on the wire, including the two calls the
 * repository makes for one intent (add to a collection, read Watch later).
 */
class VideoLibraryApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var api: VideoLibraryApi
    private lateinit var repository: VideoLibraryRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(VideoLibraryApi::class.java)
        // The identity hydrator: nothing here is about authors or covers.
        repository = VideoLibraryRepository(api, ErrorMapper(json)) { it }
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun RecordedRequest.line(): String = "$method $target"

    private fun RecordedRequest.bodyText(): String = body?.utf8().orEmpty()

    // ── Watch later ─────────────────────────────────────────────────────

    @Test
    fun `adding to watch later is a POST to the post's watch-later with no body`() {
        enqueue("""{"data":{"queued":true}}""")

        val result = runBlocking { repository.setWatchLater("p1", queued = true) }

        val request = server.takeRequest()
        assertThat(request.line()).isEqualTo("POST /v1/posts/p1/watch-later")
        assertThat(request.bodyText()).isEmpty()
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `removing from watch later is a DELETE on the same path`() {
        enqueue("""{"data":{"queued":false}}""")

        val result = runBlocking { repository.setWatchLater("p1", queued = false) }

        assertThat(server.takeRequest().line()).isEqualTo("DELETE /v1/posts/p1/watch-later")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    /** A post the viewer may not see answers 404; the write is a failure, not a silent success. */
    @Test
    fun `a refused watch later write is a failure`() {
        enqueue("""{"error":{"code":"NOT_FOUND","message":"post not found"}}""", code = 404)

        val result = runBlocking { repository.setWatchLater("gone", queued = true) }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `the watch later list is the system playlist, then that playlist's items`() {
        enqueue(
            """{"data":{"id":"wl1","creator_id":"me","title":"Queue","visibility":"private",""" +
                """"item_count":2,"kind":"watch_later"}}""",
        )
        enqueue(
            """{"data":[""" +
                """{"playlist_id":"wl1","post_id":"b","position":1,"post":{"id":"b","content_type":"long_video"}},""" +
                """{"playlist_id":"wl1","post_id":"a","position":0,"post":{"id":"a","content_type":"long_video"}},""" +
                """{"playlist_id":"wl1","post_id":"hidden","position":2,"post":null}]}""",
        )

        val result = runBlocking { repository.watchLater() }

        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/playlists/system/watch_later")
        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/playlists/wl1/items")
        val contents = (result as AppResult.Success).data
        assertThat(contents.collection.title).isEqualTo("Watch later")
        assertThat(contents.collection.isWatchLater).isTrue()
        // In the list's own order, and a row whose post the viewer may not see is dropped.
        assertThat(contents.videos.map { it.id }).containsExactly("a", "b").inOrder()
    }

    /** An empty list is `"data": null` from a Go handler: empty, not an error. */
    @Test
    fun `an empty watch later list is an empty list, not a failure`() {
        enqueue("""{"data":{"id":"wl1","kind":"watch_later"}}""")
        enqueue("""{"data":null}""")

        val result = runBlocking { repository.watchLater() }

        assertThat((result as AppResult.Success).data.videos).isEmpty()
    }

    // ── Dislike ─────────────────────────────────────────────────────────

    @Test
    fun `a dislike is a POST to the post's tune and its undo a DELETE`() {
        enqueue("""{"data":{"ok":true}}""")
        enqueue("""{"data":{"ok":true}}""")

        runBlocking {
            repository.setDisliked("p1", disliked = true)
            repository.setDisliked("p1", disliked = false)
        }

        val on = server.takeRequest()
        assertThat(on.line()).isEqualTo("POST /v1/posts/p1/tune")
        assertThat(on.bodyText()).isEmpty()
        assertThat(server.takeRequest().line()).isEqualTo("DELETE /v1/posts/p1/tune")
    }

    // ── Collections ─────────────────────────────────────────────────────

    @Test
    fun `the viewer's collections are read from the creator's playlists, fifty at a time`() {
        enqueue(
            """{"data":[{"id":"c1","title":"Build logs","visibility":"public","item_count":4,"kind":"user"},""" +
                """{"id":"c2","title":"  ","visibility":"private","item_count":0,"kind":"user"}]}""",
        )

        val result = runBlocking { repository.collections("me") }

        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/creators/me/playlists?limit=50&offset=0")
        val collections = (result as AppResult.Success).data
        assertThat(collections.map { it.title }).containsExactly("Build logs", "Untitled").inOrder()
        assertThat(collections.map { it.isPrivate }).containsExactly(false, true).inOrder()
    }

    @Test
    fun `creating a collection posts the title and the visibility, as the web does`() {
        enqueue("""{"data":{"id":"c9","title":"Cooking","visibility":"private","item_count":0,"kind":"user"}}""", 201)

        val result = runBlocking { repository.createCollection("  Cooking ", isPrivate = true) }

        val request = server.takeRequest()
        assertThat(request.line()).isEqualTo("POST /v1/playlists")
        assertThat(request.bodyText()).isEqualTo("""{"title":"Cooking","visibility":"private"}""")
        assertThat((result as AppResult.Success).data.id).isEqualTo("c9")
    }

    @Test
    fun `a public collection says public`() {
        enqueue("""{"data":{"id":"c9","title":"Cooking","visibility":"public","kind":"user"}}""", 201)

        runBlocking { repository.createCollection("Cooking", isPrivate = false) }

        assertThat(server.takeRequest().bodyText()).isEqualTo("""{"title":"Cooking","visibility":"public"}""")
    }

    /**
     * The server upserts on (playlist, position): a position already taken
     * REPLACES the video there. So the items are read first and the video
     * goes one past the highest position in use, never 0 and never a taken one.
     */
    @Test
    fun `adding to a collection reads the items, then posts the post id at a free position`() {
        enqueue("""{"data":[{"post_id":"a","position":0},{"post_id":"b","position":3}]}""")
        enqueue("""{"data":{"playlist_id":"c1","post_id":"p1","position":4}}""", 201)

        val result = runBlocking { repository.addToCollection("c1", "p1") }

        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/playlists/c1/items")
        val add = server.takeRequest()
        assertThat(add.line()).isEqualTo("POST /v1/playlists/c1/items")
        assertThat(add.bodyText()).isEqualTo("""{"post_id":"p1","position":4}""")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    /** Position 0 is a real position and must be SENT: an absent one is also 0 on the server, but explicit is the contract. */
    @Test
    fun `the first video of an empty collection is sent at position 0`() {
        enqueue("""{"data":[]}""")
        enqueue("""{"data":{"playlist_id":"c1","post_id":"p1","position":0}}""", 201)

        runBlocking { repository.addToCollection("c1", "p1") }

        server.takeRequest()
        assertThat(server.takeRequest().bodyText()).isEqualTo("""{"post_id":"p1","position":0}""")
    }

    @Test
    fun `a video already in the collection is not added twice`() {
        enqueue("""{"data":[{"post_id":"p1","position":0}]}""")

        val result = runBlocking { repository.addToCollection("c1", "p1") }

        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(server.requestCount).isEqualTo(1)
    }

    /** If the items cannot be read the position cannot be known, so nothing is written. */
    @Test
    fun `a failed items read stops the add rather than guessing a position`() {
        enqueue("""{"error":{"code":"INTERNAL_ERROR","message":"boom"}}""", code = 500)

        val result = runBlocking { repository.addToCollection("c1", "p1") }

        assertThat(result).isInstanceOf(AppResult.Failure::class.java)
        assertThat(server.requestCount).isEqualTo(1)
    }

    /** DELETE answers 204 with no body at all; that is a success. */
    @Test
    fun `removing from a collection is a DELETE on the item, and 204 is success`() {
        server.enqueue(MockResponse.Builder().code(204).build())

        val result = runBlocking { repository.removeFromCollection("c1", "p1") }

        assertThat(server.takeRequest().line()).isEqualTo("DELETE /v1/playlists/c1/items/p1")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `one collection is the playlist, then its items`() {
        enqueue("""{"data":{"id":"c1","title":"Build logs","visibility":"public","item_count":1,"kind":"user"}}""")
        enqueue("""{"data":[{"playlist_id":"c1","post_id":"a","position":0,"post":{"id":"a"}}]}""")

        val result = runBlocking { repository.collection("c1") }

        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/playlists/c1")
        assertThat(server.takeRequest().line()).isEqualTo("GET /v1/playlists/c1/items")
        assertThat((result as AppResult.Success).data.videos.map { it.id }).containsExactly("a")
    }
}
