package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
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

/**
 * The curated sound library's two requests (2026-10-15), byte for byte, and
 * what each answer becomes.
 *
 * What this protects: the list's query (genre only when chosen, the page and
 * the offset), a row the server marks inactive never reaching the list,
 * "Use" going out with NO body to the library id's route, and — the rule
 * the whole feature rests on — the sound a reel is made with being the
 * answer's `audio_tracks` id, never the library id the creator pressed.
 */
class LibrarySoundsApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: LibrarySoundsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(SoundsApi::class.java)
        repository = LibrarySoundsRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun refusal(status: Int, code: String) =
        enqueue("""{"error":{"code":"$code","message":"words that must not be branched on"}}""", status)

    private val row = LibrarySound(
        id = LIBRARY_ID,
        title = "Morning run",
        artist = "Studio",
        durationMs = 28_000L,
        genre = "pop",
        mood = "",
        usageCount = 0L,
        isTrending = false,
        audioTrackId = null,
        playable = true,
    )

    // ── the list ────────────────────────────────────────────────────────

    @Test
    fun `the first page asks for every genre with the page size and offset 0`() {
        enqueue("""{"data":{"tracks":[],"total":0}}""")

        val result = runBlocking { repository.page(genre = "", offset = 0) }

        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("GET")
        assertThat(sent.target).isEqualTo("/v1/audio-library?limit=20&offset=0")
        assertThat((result as AppResult.Success).data.sounds).isEmpty()
        assertThat(result.data.nextOffset).isNull()
    }

    @Test
    fun `a chosen genre and a later offset go on the query`() {
        enqueue("""{"data":{"tracks":[],"total":0}}""")

        runBlocking { repository.page(genre = " lo-fi ", offset = 40) }

        assertThat(server.takeRequest().target).isEqualTo("/v1/audio-library?genre=lo-fi&limit=20&offset=40")
    }

    @Test
    fun `rows map to the list, an inactive or id-less one is dropped, and a full page has a next`() {
        val tracks = (1..20).joinToString(",") { i ->
            when (i) {
                1 -> """{"id":"$LIBRARY_ID","title":" Morning run ","artist":"Studio","duration_ms":28000,
                    |"audio_url":"/v1/audio-library/$LIBRARY_ID/serve","genre":"pop","mood":"upbeat",
                    |"usage_count":1200,"is_trending":true,"is_active":true,"audio_track_id":null,
                    |"waveform_data":[1,2,3],"created_at":"2026-10-15T00:00:00Z"}""".trimMargin()
                2 -> """{"id":"l2","title":"","audio_url":"","is_active":true,"audio_track_id":"$TRACK_ID"}"""
                3 -> """{"id":"l3","title":"Withdrawn","audio_url":"https://cdn.test/a.m4a","is_active":false}"""
                4 -> """{"id":"","title":"No id"}"""
                else -> """{"id":"l$i","title":"Row $i","audio_url":"https://cdn.test/$i.m4a"}"""
            }
        }
        enqueue("""{"data":{"tracks":[$tracks],"total":20}}""")

        val page = (runBlocking { repository.page(genre = "", offset = 20) } as AppResult.Success).data

        assertThat(page.sounds.map { it.id }).doesNotContain("l3")
        assertThat(page.sounds).hasSize(18)
        val first = page.sounds.first()
        assertThat(first).isEqualTo(
            LibrarySound(
                id = LIBRARY_ID,
                title = "Morning run",
                artist = "Studio",
                durationMs = 28_000L,
                genre = "pop",
                mood = "upbeat",
                usageCount = 1_200L,
                isTrending = true,
                audioTrackId = null,
                playable = true,
            ),
        )
        val untitled = page.sounds[1]
        assertThat(untitled.title).isEqualTo(UNTITLED_SOUND)
        assertThat(untitled.audioTrackId).isEqualTo(TRACK_ID)
        assertThat(untitled.playable).isFalse()
        // The next page starts after every row sent, the dropped ones included.
        assertThat(page.nextOffset).isEqualTo(40)
    }

    @Test
    fun `the library failing to answer is a failure`() {
        enqueue("""{"error":{"code":"INTERNAL_ERROR","message":"x"}}""", 500)

        val result = runBlocking { repository.page(genre = "", offset = 0) }

        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError.Server::class.java)
    }

    // ── "Use" ───────────────────────────────────────────────────────────

    @Test
    fun `use posts to the library row's route with no body, and answers the audio_tracks id`() {
        enqueue(
            """{"data":{"audio_track_id":"$TRACK_ID","sound":{"id":"$TRACK_ID","title":"Morning run",
              |"artist":"Studio","duration_ms":28100,"status":"ready","library_id":"$LIBRARY_ID",
              |"creator_user_id":null,"source_post_id":null}}}""".trimMargin(),
        )

        val result = runBlocking { repository.use(row) }

        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("POST")
        assertThat(sent.target).isEqualTo("/v1/audio-library/$LIBRARY_ID/use")
        assertThat(sent.body?.size ?: 0).isEqualTo(0)
        val chosen = (result as AppResult.Success).data
        assertThat(chosen.id).isEqualTo(TRACK_ID)
        assertThat(chosen.id).isNotEqualTo(LIBRARY_ID)
        assertThat(chosen.title).isEqualTo("Morning run")
        assertThat(chosen.artist).isEqualTo("Studio")
        assertThat(chosen.durationMs).isEqualTo(28_100L)
    }

    @Test
    fun `an answer with only the sound still names its track, and the row fills what it lacks`() {
        enqueue("""{"data":{"sound":{"id":"$TRACK_ID","title":""}}}""")

        val chosen = (runBlocking { repository.use(row) } as AppResult.Success).data

        assertThat(chosen.id).isEqualTo(TRACK_ID)
        assertThat(chosen.title).isEqualTo("Morning run")
        assertThat(chosen.artist).isEqualTo("Studio")
        assertThat(chosen.durationMs).isEqualTo(28_000L)
    }

    @Test
    fun `an answer that names no track is malformed, never the library id`() {
        enqueue("""{"data":{"audio_track_id":"","sound":{"id":" "}}}""")
        enqueue("""{"data":{}}""")

        val blank = runBlocking { repository.use(row) }
        val empty = runBlocking { repository.use(row) }

        assertThat((blank as AppResult.Failure).error).isInstanceOf(AppError.Malformed::class.java)
        assertThat((empty as AppResult.Failure).error).isInstanceOf(AppError.Malformed::class.java)
    }

    @Test
    fun `each refusal is said by its code or status`() {
        val cases = listOf(
            Triple(401, "UNAUTHORIZED", LibrarySoundUseFailure.SIGNED_OUT),
            Triple(404, "NOT_FOUND", LibrarySoundUseFailure.GONE),
            Triple(422, "NOT_STORED", LibrarySoundUseFailure.NOT_STORED),
            Triple(503, "SOUND_UNAVAILABLE", LibrarySoundUseFailure.UNAVAILABLE),
            Triple(400, "BAD_REQUEST", LibrarySoundUseFailure.OTHER),
        )
        for ((status, code, expected) in cases) {
            refusal(status, code)

            val result = runBlocking { repository.use(row) }
            server.takeRequest()

            assertThat((result as AppResult.Failure).error.librarySoundUseFailure()).isEqualTo(expected)
        }
    }

    private companion object {
        const val LIBRARY_ID = "11111111-1111-4111-8111-111111111111"
        const val TRACK_ID = "22222222-2222-4222-8222-222222222222"
    }
}
