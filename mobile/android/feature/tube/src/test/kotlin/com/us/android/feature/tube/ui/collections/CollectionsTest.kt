package com.us.android.feature.tube.ui.collections

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.feed.data.AddPlaylistItemRequest
import com.us.android.core.feed.data.CreatePlaylistRequest
import com.us.android.core.feed.data.PlaylistDto
import com.us.android.core.feed.data.PlaylistItemDto
import com.us.android.core.feed.data.TuneDto
import com.us.android.core.feed.data.VideoCollection
import com.us.android.core.feed.data.VideoLibraryApi
import com.us.android.core.feed.data.VideoLibraryRepository
import com.us.android.core.feed.data.VideoLibraryState
import com.us.android.core.feed.data.WatchLaterDto
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.tube.ui.saved.stillSaved
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.io.IOException

/**
 * Where a video put in Watch later, in a collection or in Saved is found
 * again: the "Add to collection" sheet, and the rules that decide which rows
 * the three lists show after the viewer changes their mind.
 */
class CollectionsTest {

    /** The playlist routes as a list in memory, with a record of every write. */
    private class FakeLibraryApi : VideoLibraryApi {
        val playlists = mutableListOf(
            PlaylistDto(id = "c1", title = "Build logs", visibility = "public", itemCount = 1, kind = "user"),
        )
        val items = mutableMapOf(
            "c1" to mutableListOf(PlaylistItemDto(playlistId = "c1", postId = "old", position = 0)),
        )
        val writes = mutableListOf<String>()

        /** How many times a collection's items were read: each add reads them first. */
        var itemReads = 0
        var failAdds = false
        var failCreates = false
        var failList = false

        override suspend fun creatorPlaylists(
            creatorId: String,
            limit: Int,
            offset: Int,
        ): ApiEnvelope<List<PlaylistDto>> {
            if (failList) throw IOException("offline")
            return ApiEnvelope(data = playlists.toList(), meta = null)
        }

        override suspend fun createPlaylist(body: CreatePlaylistRequest): ApiEnvelope<PlaylistDto> {
            if (failCreates) throw IOException("offline")
            writes += "create:${body.title}:${body.visibility}"
            val made = PlaylistDto(id = "new", title = body.title, visibility = body.visibility, kind = "user")
            playlists += made
            items["new"] = mutableListOf()
            return ApiEnvelope(data = made, meta = null)
        }

        override suspend fun items(playlistId: String): ApiEnvelope<List<PlaylistItemDto>> {
            itemReads++
            return ApiEnvelope(data = items[playlistId].orEmpty().toList(), meta = null)
        }

        override suspend fun addItem(playlistId: String, body: AddPlaylistItemRequest): ApiEnvelope<PlaylistItemDto> {
            if (failAdds) throw IOException("offline")
            writes += "add:$playlistId:${body.postId}@${body.position}"
            val row = PlaylistItemDto(playlistId = playlistId, postId = body.postId, position = body.position)
            items.getValue(playlistId) += row
            return ApiEnvelope(data = row, meta = null)
        }

        override suspend fun removeItem(playlistId: String, postId: String) = error("not used here")
        override suspend fun playlist(playlistId: String): ApiEnvelope<PlaylistDto> = error("not used here")
        override suspend fun systemPlaylist(kind: String): ApiEnvelope<PlaylistDto> = error("not used here")
        override suspend fun addWatchLater(postId: String): ApiEnvelope<WatchLaterDto> = error("not used here")
        override suspend fun removeWatchLater(postId: String): ApiEnvelope<WatchLaterDto> = error("not used here")
        override suspend fun addDislike(postId: String): ApiEnvelope<TuneDto> = error("not used here")
        override suspend fun removeDislike(postId: String): ApiEnvelope<TuneDto> = error("not used here")
    }

    private fun picker(api: FakeLibraryApi) =
        CollectionPicker(VideoLibraryRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })) { it })

    private fun video(id: String) = FeedItem(
        id = id,
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Clee"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "2026-09-27T10:00:00Z",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(likes = 0, comments = 0, reposts = 0, views = 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
    )

    // ── Add to collection ───────────────────────────────────────────────

    @Test
    fun `opening the sheet lists the viewer's collections`() = runTest {
        val picker = picker(FakeLibraryApi())

        picker.open(postId = "v1", ownerId = "me")

        val state = picker.state.value
        assertThat(state.loading).isFalse()
        assertThat(state.collections.map { it.title }).containsExactly("Build logs")
        assertThat(state.added).isEmpty()
        assertThat(state.error).isNull()
    }

    @Test
    fun `a tap adds the video after the ones already there, and the row is checked only then`() = runTest {
        val api = FakeLibraryApi()
        val picker = picker(api)
        picker.open("v1", "me")

        picker.add(picker.state.value.collections.single())

        assertThat(api.writes).containsExactly("add:c1:v1@1")
        assertThat(picker.state.value.added).containsExactly("c1")
        assertThat(picker.state.value.busy).isFalse()
        // The video is in the list the collection page reads.
        assertThat(api.items.getValue("c1").map { it.postId }).containsExactly("old", "v1").inOrder()
    }

    @Test
    fun `a second tap on a collection that has the video writes nothing`() = runTest {
        val api = FakeLibraryApi()
        val picker = picker(api)
        picker.open("v1", "me")
        val collection = picker.state.value.collections.single()

        picker.add(collection)
        picker.add(collection)

        assertThat(api.writes).hasSize(1)
        // The sheet already knows: the second tap does not even ask the server.
        assertThat(api.itemReads).isEqualTo(1)
    }

    @Test
    fun `a refused add leaves the row unchecked and says why`() = runTest {
        val api = FakeLibraryApi().apply { failAdds = true }
        val picker = picker(api)
        picker.open("v1", "me")

        picker.add(picker.state.value.collections.single())

        val state = picker.state.value
        assertThat(state.added).isEmpty()
        assertThat(state.busy).isFalse()
        assertThat(state.error).isNotNull()
        assertThat(api.items.getValue("c1").map { it.postId }).containsExactly("old")
    }

    @Test
    fun `create makes the collection, private as chosen, and puts the video in it`() = runTest {
        val api = FakeLibraryApi()
        val picker = picker(api)
        picker.open("v1", "me")

        picker.create("  Cooking  ", isPrivate = true)

        assertThat(api.writes).containsExactly("create:Cooking:private", "add:new:v1@0").inOrder()
        val state = picker.state.value
        assertThat(state.collections.first().title).isEqualTo("Cooking")
        assertThat(state.added).containsExactly("new")
    }

    @Test
    fun `a blank title creates nothing`() = runTest {
        val api = FakeLibraryApi()
        val picker = picker(api)
        picker.open("v1", "me")

        picker.create("   ", isPrivate = true)

        assertThat(api.writes).isEmpty()
        assertThat(picker.state.value.busy).isFalse()
    }

    @Test
    fun `a refused create says why and adds nothing`() = runTest {
        val api = FakeLibraryApi().apply { failCreates = true }
        val picker = picker(api)
        picker.open("v1", "me")

        picker.create("Cooking", isPrivate = false)

        assertThat(api.writes).isEmpty()
        assertThat(picker.state.value.error).isNotNull()
        assertThat(picker.state.value.busy).isFalse()
    }

    @Test
    fun `collections that cannot be read are an error line, not an empty invitation`() = runTest {
        val picker = picker(FakeLibraryApi().apply { failList = true })

        picker.open("v1", "me")

        assertThat(picker.state.value.loading).isFalse()
        assertThat(picker.state.value.error).isNotNull()
        assertThat(picker.state.value.collections).isEmpty()
    }

    @Test
    fun `opening on another video forgets the last one's checks`() = runTest {
        val picker = picker(FakeLibraryApi())
        picker.open("v1", "me")
        picker.add(picker.state.value.collections.single())

        picker.open("v2", "me")

        assertThat(picker.state.value.added).isEmpty()
    }

    // ── What the lists show after a change of mind ──────────────────────

    private val watchLater = VideoCollection("wl", "Watch later", 2, isPrivate = true, isWatchLater = true)
    private val own = VideoCollection("c1", "Build logs", 2, isPrivate = false, isWatchLater = false)

    /** Taken out on the watch screen, then Back: the row is already gone from the list. */
    @Test
    fun `a video taken out of watch later this session leaves the watch later list at once`() {
        val content = CollectionContent.Ready(watchLater, listOf(video("a"), video("b")))

        val shown = visibleVideos(content, VideoLibraryState(queued = mapOf("a" to false)))

        assertThat(shown.map { it.id }).containsExactly("b")
    }

    /** The remove was refused: the store put the value back, and the row is back with it. */
    @Test
    fun `a refused removal brings the row back`() {
        val content = CollectionContent.Ready(watchLater, listOf(video("a"), video("b")))

        val shown = visibleVideos(content, VideoLibraryState(queued = mapOf("a" to true)))

        assertThat(shown.map { it.id }).containsExactly("a", "b").inOrder()
    }

    /** Watch later's state says nothing about a collection the viewer made. */
    @Test
    fun `a collection the viewer made is not filtered by watch later state`() {
        val content = CollectionContent.Ready(own, listOf(video("a"), video("b")))

        val shown = visibleVideos(content, VideoLibraryState(queued = mapOf("a" to false, "b" to false)))

        assertThat(shown.map { it.id }).containsExactly("a", "b").inOrder()
    }

    @Test
    fun `a saved row stays until the viewer un-saves it, and returns if the un-save is refused`() {
        assertThat(stillSaved(null)).isTrue()
        assertThat(stillSaved(EngagementOverlay())).isTrue()
        assertThat(stillSaved(EngagementOverlay(bookmarked = true))).isTrue()
        assertThat(stillSaved(EngagementOverlay(bookmarked = false))).isFalse()
        // A like says nothing about the save.
        assertThat(stillSaved(EngagementOverlay(reacted = false))).isTrue()
    }

    @Test
    fun `a list's line counts its videos and says who can see it`() {
        assertThat(collectionMeta(1, isPrivate = true)).isEqualTo("1 video · Private")
        assertThat(collectionMeta(12, isPrivate = false)).isEqualTo("12 videos · Public")
        assertThat(collectionMeta(0, isPrivate = true)).isEqualTo("0 videos · Private")
    }
}
