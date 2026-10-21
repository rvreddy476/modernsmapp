package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.time.LocalDate

/**
 * The owner's Edit (2026-10-21, the web's `editForm.ts` and post-service's
 * `PATCH /v1/posts/:id`): the form read from the post detail, the PATCH that
 * carries ONLY what moved, the checks before Save, the request on the wire,
 * and what a landed save does to the rows on screen.
 */
class PostEditTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: PostEditRepository
    private lateinit var edits: PostEdits

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(PostEditApi::class.java)
        edits = PostEdits()
        repository = PostEditRepository(api, ErrorMapper(json), edits)
    }

    @After
    fun tearDown() = server.close()

    /** A long video's `PostDetail` as post-service writes it (`store/postgres/posts.go` tags, omitempty and all). */
    private val detailFixture = """
        {"data":{
          "id":"p1","author_id":"u1","text":"A walk by the lake","visibility":"unlisted","status":"published",
          "content_type":"long_video","post_type":"video","no_comments":false,"hashtags":["lake","walk"],
          "title":"Sunday","tags":["travel","nature"],"category":"travel","language":"en",
          "paid_promotion":false,"altered_content":true,"is_made_for_kids":false,"license":"creative_commons",
          "allow_embedding":false,"remix_setting":"disallow","comment_access":"followers",
          "recording_date":"2026-10-01T00:00:00Z","recording_location":"Hyderabad",
          "is_branded":false,"ai_generated":false,"allow_download":false,
          "age_restricted":true,"hide_like_count":true,"default_comment_sort":"newest",
          "counts":{"likes":3},"view_count":10,"has_reacted":false,"is_bookmarked":false
        }}
    """.trimIndent()

    @Test
    fun `the form is read from the post detail, field for field`() {
        server.enqueue(MockResponse.Builder().code(200).body(detailFixture).build())

        val detail = (runBlocking { repository.detail("p1") } as AppResult.Success).data
        val form = detail.toEditForm()

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/p1")
        assertThat(form).isEqualTo(
            PostEditForm(
                title = "Sunday",
                text = "A walk by the lake",
                category = "travel",
                tags = "travel, nature",
                hashtags = "lake, walk",
                language = "en",
                visibility = "unlisted",
                allowDownload = false,
                noComments = false,
                madeForKids = false,
                paidPromotion = false,
                alteredContent = true,
                license = "creative_commons",
                allowEmbedding = false,
                recordingDate = "2026-10-01",
                recordingLocation = "Hyderabad",
                remixSetting = "disallow",
                commentModeration = "none",
                commentAccess = "followers",
                ageRestricted = true,
                hideLikeCount = true,
                defaultCommentSort = "newest",
            ),
        )
    }

    @Test
    fun `absent omitempty fields read as the server's defaults`() {
        val form = PostEditDetailDto(id = "p1").toEditForm()

        assertThat(form.visibility).isEqualTo("public")
        assertThat(form.license).isEqualTo("standard")
        assertThat(form.remixSetting).isEqualTo("allow")
        assertThat(form.commentAccess).isEqualTo("everyone")
        assertThat(form.defaultCommentSort).isEqualTo("top")
        assertThat(form.allowDownload).isTrue()
        assertThat(form.allowEmbedding).isTrue()
        assertThat(form.recordingDate).isEmpty()
    }

    @Test
    fun `nothing moved, nothing to send`() {
        val base = PostEditForm(title = "Sunday", tags = "a, b")

        assertThat(postEditPatch(base, base.copy(tags = "a,b,  ")).isEmpty).isTrue()
    }

    @Test
    fun `only the moved fields are sent, the title trimmed and the tags split`() {
        val base = PostEditForm(title = "Sunday", text = "old", hashtags = "lake", noComments = false)
        val next = base.copy(title = "  Sunday walk ", hashtags = "#lake, walk, Lake", noComments = true)

        assertThat(postEditPatch(base, next)).isEqualTo(
            PostEditPatch(title = "Sunday walk", hashtags = listOf("lake", "walk"), noComments = true),
        )
    }

    @Test
    fun `a cleared recording date goes as the contract's empty clear`() {
        val base = PostEditForm(recordingDate = "2026-10-01")

        assertThat(postEditPatch(base, base.copy(recordingDate = "")).recordingDate).isEqualTo("")
    }

    @Test
    fun `a scheduled post's visibility is never sent`() {
        val base = PostEditForm(visibility = "scheduled", title = "a")

        assertThat(postEditPatch(base, base.copy(title = "b")).visibility).isNull()
    }

    @Test
    fun `the save is a PATCH on the post with only the moved keys`() {
        server.enqueue(MockResponse.Builder().code(200).body(detailFixture).build())
        val patch = postEditPatch(PostEditForm(title = "Sunday"), PostEditForm(title = "Monday", allowDownload = false))

        val result = runBlocking { repository.save("p1", patch) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PATCH")
        assertThat(request.target).isEqualTo("/v1/posts/p1")
        assertThat(request.body?.utf8()).isEqualTo("""{"title":"Monday","allow_download":false}""")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
        // Every list learns the save.
        assertThat(edits.edits.value["p1"]).isEqualTo(patch)
    }

    @Test
    fun `the wire names of every field are post-service's`() {
        val body = json.encodeToString(
            PostEditPatch.serializer(),
            PostEditPatch(
                madeForKids = true,
                paidPromotion = true,
                alteredContent = false,
                allowEmbedding = true,
                recordingDate = "",
                recordingLocation = "x",
                remixSetting = "allow",
                commentModeration = "basic",
                commentAccess = "nobody",
                ageRestricted = false,
                hideLikeCount = true,
                defaultCommentSort = "top",
                noComments = false,
            ),
        )

        assertThat(body).isEqualTo(
            """{"no_comments":false,"made_for_kids":true,"paid_promotion":true,"altered_content":false,""" +
                """"allow_embedding":true,"recording_date":"","recording_location":"x","remix_setting":"allow",""" +
                """"comment_moderation":"basic","comment_access":"nobody","age_restricted":false,""" +
                """"hide_like_count":true,"default_comment_sort":"top"}""",
        )
    }

    @Test
    fun `a refused save is not recorded and says why in the web's words`() {
        server.enqueue(
            MockResponse.Builder().code(422)
                .body("""{"error":{"code":"TITLE_TOO_LONG","message":"title is too long"}}""").build(),
        )

        val result = runBlocking { repository.save("p1", PostEditPatch(title = "x".repeat(500))) }

        assertThat(postEditRefusal((result as AppResult.Failure).error)).isEqualTo("The title is too long.")
        assertThat(edits.edits.value).isEmpty()
    }

    @Test
    fun `the refusals by code`() {
        assertThat(postEditRefusal(AppError.Forbidden(code = "FORBIDDEN")))
            .isEqualTo("Only the creator can change this video.")
        assertThat(postEditRefusal(AppError.NotFound())).isEqualTo("This video no longer exists.")
        assertThat(postEditRefusal(AppError.Unknown(code = "INVALID_HASHTAG", statusCode = 422)))
            .isEqualTo("One of the hashtags can't be used.")
    }

    @Test
    fun `the checks before Save`() {
        val today = LocalDate.of(2026, 10, 21)

        assertThat(postEditProblems(PostEditForm(), today)).isEmpty()
        assertThat(postEditProblems(PostEditForm(recordingDate = "2026-10-21"), today)).isEmpty()
        assertThat(postEditProblems(PostEditForm(recordingDate = "2026-10-22"), today))
            .containsExactly("The recording date can't be in the future.")
        assertThat(postEditProblems(PostEditForm(recordingDate = "21/10/2026"), today))
            .containsExactly("Write the recording date as YYYY-MM-DD.")
        assertThat(postEditProblems(PostEditForm(recordingLocation = "x".repeat(101)), today)).hasSize(1)
    }

    @Test
    fun `a landed save shows on the row at once, and two saves fold into one`() {
        val row = FeedItem(
            id = "p1",
            authorId = "u1",
            author = FeedAuthor(id = "u1", displayName = "Ada"),
            text = "old words",
            title = "Sunday",
            visibility = "public",
            feedContentType = "long_video",
            postType = "video",
            createdAt = "",
            isPinned = false,
            media = emptyList(),
            counts = FeedCounts(0, 0, 0, 0),
            viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
            isRepostable = true,
        )
        edits.record("p1", PostEditPatch(title = "Monday", noComments = true))
        edits.record("p1", PostEditPatch(text = "new words", visibility = "unlisted"))

        val shown = edits.edits.value.applyTo(row)

        assertThat(shown.title).isEqualTo("Monday")
        assertThat(shown.text).isEqualTo("new words")
        assertThat(shown.visibility).isEqualTo("unlisted")
        assertThat(shown.controls.noComments).isTrue()
        // Another post is left as it was.
        assertThat(edits.edits.value.applyTo(row.copy(id = "p2"))).isEqualTo(row.copy(id = "p2"))
    }

    @Test
    fun `tags split as the web splits them`() {
        assertThat(splitTags("#Lake, lake,\nwalk ,, ")).containsExactly("Lake", "walk").inOrder()
    }
}
