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
import java.io.File

/**
 * The three sound requests, byte for byte, and what each answer becomes.
 *
 * What this protects: the paths of contract 2.2 and 2.3 (a sound id in the
 * wrong place is a 404 that reads as "this sound is gone"), the cursor being
 * replayed verbatim and omitted on the first page, "use this sound" going
 * out with NO body, and a refusal keeping the server's code all the way to
 * the one wording function.
 */
class SoundsApiRequestTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: SoundsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(SoundsApi::class.java)
        repository = SoundsRepository(api, ErrorMapper(json)) { it }
    }

    @After
    fun tearDown() = server.close()

    private fun fixture(name: String) = File("src/test/resources/contracts/sounds", name).readText()

    private fun enqueue(body: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(body).build())
    }

    private fun refusal(status: Int, code: String) =
        enqueue("""{"error":{"code":"$code","message":"words that must not be branched on"}}""", status)

    // ── use this sound ──────────────────────────────────────────────────

    @Test
    fun `use this sound posts to the reel's sound route with no body`() {
        enqueue("""{"data":${fixture("use_sound.json")}}""")

        val result = runBlocking { repository.useSound("dddddddd-dddd-4ddd-8ddd-dddddddddddd") }

        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("POST")
        assertThat(sent.target).isEqualTo("/v1/posts/dddddddd-dddd-4ddd-8ddd-dddddddddddd/sound")
        assertThat(sent.body?.size ?: 0).isEqualTo(0)
        val sound = (result as AppResult.Success).data
        assertThat(sound.id).isEqualTo("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
        assertThat(sound.startMs).isEqualTo(0L)
    }

    @Test
    fun `an answer without a usable sound is a failure, never a sound with an empty id`() {
        enqueue("""{"data":{"sound":{"id":"","title":"Original sound"}}}""")
        enqueue("""{"data":{}}""")

        val empty = runBlocking { repository.useSound("p1") }
        val absent = runBlocking { repository.useSound("p1") }

        assertThat((empty as AppResult.Failure).error).isInstanceOf(AppError.Malformed::class.java)
        assertThat((absent as AppResult.Failure).error).isInstanceOf(AppError.Malformed::class.java)
    }

    @Test
    fun `a refusal keeps the server's code, whatever its status`() {
        val cases = listOf(
            403 to "SOUND_REUSE_NOT_ALLOWED",
            404 to "NOT_FOUND",
            422 to "NOT_READY",
            422 to "TOO_LONG",
            422 to "NOT_A_REEL",
            422 to "NO_AUDIO",
            429 to "RATE_LIMITED",
            503 to "SOUND_UNAVAILABLE",
        )
        for ((status, code) in cases) {
            refusal(status, code)

            val result = runBlocking { repository.useSound("p1") }
            server.takeRequest()

            val error = (result as AppResult.Failure).error
            assertThat(error.soundRefusalCode()).isEqualTo(code)
            assertThat(error.soundRefusalMessage()).isEqualTo(soundRefusalMessage(code))
        }
    }

    // ── the reels that play a sound ─────────────────────────────────────

    @Test
    fun `the first page asks by the sound id with the page size and no cursor`() {
        enqueue("""{"data":${fixture("by_sound.json")},"meta":{"next_cursor":"MjAyNi0wOS0yNg"}}""")

        val result = runBlocking { repository.reels("cccccccc-cccc-4ccc-8ccc-cccccccccccc") }

        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("GET")
        assertThat(sent.target).isEqualTo("/v1/posts/by-sound/cccccccc-cccc-4ccc-8ccc-cccccccccccc?limit=24")
        val page = (result as AppResult.Success).data
        assertThat(page.origin?.id).isEqualTo("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
        assertThat(page.items).hasSize(2)
        assertThat(page.nextCursor).isEqualTo("MjAyNi0wOS0yNg")
    }

    @Test
    fun `a later page replays the cursor verbatim`() {
        enqueue("""{"data":{"sound":{"id":"s1"},"origin":null,"items":[]}}""")

        val result = runBlocking { repository.reels("s1", cursor = "MjAyNi0wOS0yNg==") }

        assertThat(server.takeRequest().target).isEqualTo("/v1/posts/by-sound/s1?limit=24&cursor=MjAyNi0wOS0yNg%3D%3D")
        val page = (result as AppResult.Success).data
        assertThat(page.origin).isNull()
        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isNull()
    }

    @Test
    fun `a sound that is gone or may not be heard is not found`() {
        refusal(404, "NOT_FOUND")

        val result = runBlocking { repository.reels("s1") }

        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError.NotFound::class.java)
    }

    // ── the sound's own row ─────────────────────────────────────────────

    @Test
    fun `the sound's row is read from the audio route`() {
        enqueue("""{"data":${fixture("sound.json")}}""")

        val result = runBlocking { repository.sound("085f7b72-eba4-43f4-b303-ddf92b2d64ca") }

        val sent = server.takeRequest()
        assertThat(sent.method).isEqualTo("GET")
        assertThat(sent.target).isEqualTo("/v1/audio/085f7b72-eba4-43f4-b303-ddf92b2d64ca")
        assertThat((result as AppResult.Success).data.title).isEqualTo("Kitchen take")
    }

    @Test
    fun `a row that is not ready to be played is answered like a missing one`() {
        enqueue("""{"data":{"id":"s1","title":"x","status":"processing"}}""")
        refusal(404, "NOT_FOUND")

        val unready = runBlocking { repository.sound("s1") }
        val missing = runBlocking { repository.sound("s1") }

        assertThat((unready as AppResult.Failure).error).isInstanceOf(AppError.NotFound::class.java)
        assertThat((missing as AppResult.Failure).error).isInstanceOf(AppError.NotFound::class.java)
    }
}
