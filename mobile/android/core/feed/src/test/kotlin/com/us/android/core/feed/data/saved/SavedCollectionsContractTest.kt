package com.us.android.core.feed.data.saved

import androidx.paging.testing.asSnapshot
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.feed.data.FeedApi
import com.us.android.core.feed.data.FeedItemHydrator
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.VideoFeedApi
import com.us.android.core.feed.data.VideoFeedRepository
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Saved collections against post-service, byte for byte (server commit
 * 546eec063). The fixtures are the handlers' own shapes:
 *
 *  - the list: saved_collections_handler.go:22-25 (default first, `id:null`)
 *    and service/saved_collections.go:82-90 (the row's keys);
 *  - create / rename: :26-29, `{name}` in, a collection out;
 *  - delete: :30-31 and :249, `{deleted, collection_id, moved_to_default}`;
 *  - the moves: :32-35, `{collection_id: uuid|"default"}` in, a saved item
 *    (store/postgres/saved.go:17-27) out;
 *  - the save into a collection: handler.go:1882-1902, `{"bookmarked":true}`;
 *  - the filter: handler.go:1948-1956, `?collection_id=`;
 *  - the refusals: saved_collections_handler.go:63-85.
 */
class SavedCollectionsContractTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: SavedCollectionsRepository
    private lateinit var videos: VideoFeedRepository
    private lateinit var engagement: EngagementStore

    /** Bookmark writes outside the collection routes must not happen: a confirmed save sends nothing. */
    private class NoWrites : EngagementWrites {
        val calls = mutableListOf<String>()
        override suspend fun react(postId: String, reaction: String) = fail("react")
        override suspend fun unreact(postId: String) = fail("unreact")
        override suspend fun setBookmarked(postId: String, bookmarked: Boolean) = fail("bookmark=$bookmarked")
        override suspend fun repost(postId: String) = fail("repost")
        override suspend fun removeRepost(postId: String) = fail("unrepost")
        private fun fail(name: String): AppResult<Unit> {
            calls += name
            return AppResult.Success(Unit)
        }
    }

    private val writes = NoWrites()

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val retrofit = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
        val mapper = ErrorMapper(json)
        engagement = EngagementStore(writes)
        repository = SavedCollectionsRepository(retrofit.create(SavedCollectionsApi::class.java), engagement, mapper)
        val identity = FeedItemHydrator { it }
        videos = VideoFeedRepository(
            api = retrofit.create(VideoFeedApi::class.java),
            feeds = FeedRepository(retrofit.create(FeedApi::class.java), mapper, identity),
            errorMapper = mapper,
            hydrator = identity,
        )
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(
            MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
        )
    }

    private fun bodyOf() = server.takeRequest().let { request ->
        Triple(request.method, request.target, request.body?.utf8().orEmpty())
    }

    // ── Reads ───────────────────────────────────────────────────────────

    @Test
    fun `the list decodes the default first and the named collections after it`() = runTest {
        // saved_collections_handler.go:22-25; service/saved_collections.go:82-90 (created_at/updated_at omitempty).
        enqueue(
            """{"data":[{"id":null,"name":"All Saved","count":3,"is_default":true},""" +
                """{"id":"0b8f6a8e-1c2d-4e5f-8a9b-0c1d2e3f4a5b","name":"Recipes","count":2,"is_default":false,""" +
                """"created_at":"2026-10-24T08:00:00Z","updated_at":"2026-10-24T08:00:00Z"}]}""",
        )

        val collections = (repository.collections() as AppResult.Success).data

        assertThat(bodyOf().second).isEqualTo("/v1/saved/collections")
        assertThat(collections).containsExactly(
            SavedCollection(null, "All Saved", 3),
            SavedCollection("0b8f6a8e-1c2d-4e5f-8a9b-0c1d2e3f4a5b", "Recipes", 2),
        ).inOrder()
    }

    @Test
    fun `the saved tab reads one collection with collection_id`() = runTest {
        enqueue("""{"data":[{"id":"p1","content_type":"post"}]}""")
        videos.savedPosts(collectionRef(null)).asSnapshot()
        assertThat(server.takeRequest().url.queryParameter("collection_id")).isEqualTo("default")

        enqueue("""{"data":[]}""")
        videos.savedPosts("c-1").asSnapshot()
        val named = server.takeRequest()
        assertThat(named.url.encodedPath).isEqualTo("/v1/posts/bookmarks")
        assertThat(named.url.queryParameter("collection_id")).isEqualTo("c-1")
    }

    @Test
    fun `every saved post is still the plain list, with no collection_id`() = runTest {
        enqueue("""{"data":[]}""")
        videos.savedPosts().asSnapshot()
        assertThat(server.takeRequest().url.queryParameter("collection_id")).isNull()
    }

    // ── Writes ──────────────────────────────────────────────────────────

    @Test
    fun `create posts the name and answers the stored collection`() = runTest {
        enqueue("""{"data":{"id":"c-1","name":"Recipes","count":0,"is_default":false}}""", code = 201)

        val made = (repository.create("  Recipes ") as AppResult.Success).data

        val (method, target, body) = bodyOf()
        assertThat(method).isEqualTo("POST")
        assertThat(target).isEqualTo("/v1/saved/collections")
        assertThat(json.parseToJsonElement(body).jsonObject.keys).containsExactly("name")
        assertThat(made).isEqualTo(SavedCollection("c-1", "Recipes", 0))
        assertThat(repository.changes.value).isEqualTo(1)
    }

    @Test
    fun `rename patches the one collection with its new name`() = runTest {
        enqueue("""{"data":{"id":"c-1","name":"Dinners","count":2,"is_default":false}}""")

        repository.rename("c-1", "Dinners")

        val (method, target, body) = bodyOf()
        assertThat(method).isEqualTo("PATCH")
        assertThat(target).isEqualTo("/v1/saved/collections/c-1")
        assertThat(body).isEqualTo("""{"name":"Dinners"}""")
    }

    @Test
    fun `delete answers how many posts went back to All Saved`() = runTest {
        // saved_collections_handler.go:249.
        enqueue("""{"data":{"deleted":true,"collection_id":"c-1","moved_to_default":4}}""")

        val deleted = (repository.delete("c-1") as AppResult.Success).data

        val (method, target, _) = bodyOf()
        assertThat(method).isEqualTo("DELETE")
        assertThat(target).isEqualTo("/v1/saved/collections/c-1")
        assertThat(deleted).isEqualTo(DeletedCollection("c-1", 4))
    }

    @Test
    fun `save into a named collection posts its id on the bookmark route and shows Saved everywhere`() = runTest {
        enqueue("""{"data":{"bookmarked":true}}""")

        val result = repository.saveTo("p1", toCollectionId = "c-1", alreadySaved = false)

        val (method, target, body) = bodyOf()
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        assertThat(method).isEqualTo("POST")
        assertThat(target).isEqualTo("/v1/posts/p1/bookmark")
        assertThat(body).isEqualTo("""{"collection_id":"c-1"}""")
        assertThat(engagement.overlayFor("p1").bookmarked).isTrue()
        assertThat(writes.calls).isEmpty()
    }

    @Test
    fun `save into All Saved posts default, and an already-saved post is moved there instead`() = runTest {
        enqueue("""{"data":{"bookmarked":true}}""")
        repository.saveTo("p1", toCollectionId = null, alreadySaved = false)
        val fresh = bodyOf()
        assertThat(fresh.first).isEqualTo("POST")
        assertThat(fresh.third).isEqualTo("""{"collection_id":"default"}""")

        // A plain save keeps a saved post where it is (handler.go:1882-1884): the move is the way back.
        enqueue(
            """{"data":{"id":"s1","user_id":"u1","target_type":"post","target_id":"p1",""" +
                """"collection_name":"All Saved","collection_id":null,"created_at":"2026-10-24T08:00:00Z"}}""",
        )
        repository.saveTo("p1", toCollectionId = null, alreadySaved = true)
        val (method, target, body) = bodyOf()
        assertThat(method).isEqualTo("PUT")
        assertThat(target).isEqualTo("/v1/posts/p1/bookmark/collection")
        assertThat(body).isEqualTo("""{"collection_id":"default"}""")
    }

    @Test
    fun `move puts the collection_id key on every move, default included`() = runTest {
        enqueue("""{"data":{"id":"s1","target_id":"p1","collection_id":"c-2"}}""")
        repository.move("p1", "c-2")
        val named = bodyOf()
        assertThat(named.first).isEqualTo("PUT")
        assertThat(named.second).isEqualTo("/v1/posts/p1/bookmark/collection")
        assertThat(json.parseToJsonElement(named.third).jsonObject["collection_id"]?.jsonPrimitive?.content).isEqualTo("c-2")

        enqueue("""{"data":{"id":"s1","target_id":"p1","collection_id":null}}""")
        repository.move("p1", null)
        // requiredCollectionBody (:120-134) refuses a body without the key: null is never sent as "absent".
        assertThat(bodyOf().third).isEqualTo("""{"collection_id":"default"}""")
    }

    // ── Refusals, through the real error mapping ─────────────────────────

    private suspend fun refusalFor(code: Int, errorCode: String, action: SavedAction): String {
        enqueue("""{"error":{"code":"$errorCode","message":"server words"}}""", code = code)
        val result = when (action) {
            SavedAction.CREATE -> repository.create("x")
            SavedAction.MOVE -> repository.move("p1", "c-1")
            else -> repository.delete("c-1")
        }
        server.takeRequest()
        return savedCollectionRefusal((result as AppResult.Failure).error, action)
    }

    @Test
    fun `each refusal code reads as its own sentence`() = runTest {
        assertThat(refusalFor(400, "INVALID_COLLECTION_NAME", SavedAction.CREATE))
            .isEqualTo("A collection name needs 1 to 40 characters and can't be “All Saved”.")
        assertThat(refusalFor(409, "COLLECTION_NAME_TAKEN", SavedAction.CREATE))
            .isEqualTo("You already have a collection with that name.")
        assertThat(refusalFor(409, "COLLECTION_LIMIT_REACHED", SavedAction.CREATE))
            .isEqualTo("You can have up to 50 collections. Delete one to make another.")
        // Two different 404s on one route: the code survives the mapping (ErrorMapper, 2026-10-24).
        assertThat(refusalFor(404, "COLLECTION_NOT_FOUND", SavedAction.MOVE))
            .isEqualTo("That collection no longer exists. It may have been deleted on another device.")
        assertThat(refusalFor(404, "NOT_SAVED", SavedAction.MOVE))
            .isEqualTo("This post isn't in your saved items any more.")
        assertThat(refusalFor(503, "STORE_UNAVAILABLE", SavedAction.DELETE))
            .isEqualTo("Saved items are unavailable right now. Try again in a moment.")
        assertThat(refusalFor(500, "INTERNAL_ERROR", SavedAction.DELETE))
            .isEqualTo("That collection couldn't be deleted. Try again.")
        assertThat(repository.changes.value).isEqualTo(0)
    }
}
