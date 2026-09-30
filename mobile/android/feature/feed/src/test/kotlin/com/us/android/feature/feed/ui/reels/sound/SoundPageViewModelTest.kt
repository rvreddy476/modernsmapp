package com.us.android.feature.feed.ui.reels.sound

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.SoundReelsDto
import com.us.android.core.feed.data.SoundReelsPage
import com.us.android.core.feed.data.SoundRowDto
import com.us.android.core.feed.data.SoundsApi
import com.us.android.core.feed.data.SoundsRepository
import com.us.android.core.feed.data.UseSoundDto
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.dto.FeedMediaDto
import com.us.android.core.feed.data.dto.FeedSoundDto
import com.us.android.core.media.ChosenSound
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.ReelsEntry
import com.us.android.core.media.SoundEntry
import com.us.android.core.model.ReelSoundWire
import com.us.android.core.model.toReelSound
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiErrorBody
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/**
 * The sound page: the sound and its reels, a page at a time, and the two
 * things it hands on — the sound to the create flow, a reel to Reels.
 *
 * What this protects: a sound that is gone or not this viewer's to hear
 * reading as gone, not as an error to retry; the origin leading the grid
 * once and only once across pages; a later page failing without taking the
 * tiles already shown; and "Use this sound" leaving the sound where the
 * create flow will find it.
 */
class SoundPageViewModelTest {

    @get:Rule
    val mainDispatcher = MainDispatcherRule()

    private val json = Json { ignoreUnknownKeys = true }

    private fun reel(id: String, sound: FeedSoundDto? = null) = FeedItemDto(
        id = id,
        authorId = "a-$id",
        contentType = "flick",
        media = listOf(
            FeedMediaDto(
                mediaId = "m-$id",
                kind = "video",
                durationMs = 12_000L,
                variants = mapOf("thumb_150" to "https://obj/$id.jpg"),
            ),
        ),
        sound = sound,
    )

    private val sound = FeedSoundDto(
        id = "s1",
        title = "Original sound - Asha",
        artist = "Asha",
        durationMs = 28_400L,
        useCount = 3,
    )

    /** Answers by-sound, one scripted page per cursor; a missing script is a failure. */
    private class ScriptedSoundsApi(
        private val pages: Map<String?, () -> ApiEnvelope<SoundReelsDto>>,
    ) : SoundsApi {
        val asked = mutableListOf<String?>()

        override suspend fun reelsBySound(soundId: String, limit: Int, cursor: String?): ApiEnvelope<SoundReelsDto> {
            asked += cursor
            return pages[cursor]?.invoke() ?: throw java.io.IOException("no page scripted for cursor $cursor")
        }

        override suspend fun useSound(postId: String): ApiEnvelope<UseSoundDto> = error("unused")
        override suspend fun sound(soundId: String): ApiEnvelope<SoundRowDto> = error("unused")
    }

    private fun page(
        vararg ids: String,
        origin: String? = null,
        next: String? = null,
    ): () -> ApiEnvelope<SoundReelsDto> = {
        ApiEnvelope(
            data = SoundReelsDto(sound = sound, origin = origin?.let { reel(it) }, items = ids.map { reel(it, sound) }),
            meta = next?.let { ApiMeta(nextCursor = it) },
        )
    }

    private fun notFound(): () -> ApiEnvelope<SoundReelsDto> = {
        val body = """{"error":{"code":"NOT_FOUND","message":"Audio track not found"}}"""
        throw HttpException(Response.error<Any>(404, body.toResponseBody("application/json".toMediaType())))
    }

    private fun refused(code: String): () -> ApiEnvelope<SoundReelsDto> = {
        ApiEnvelope(error = ApiErrorBody(code = code, message = "not shown"))
    }

    private class Harness(
        val api: ScriptedSoundsApi,
        val soundEntry: SoundEntry = SoundEntry(),
        val reelsEntry: ReelsEntry = ReelsEntry(),
    )

    private fun viewModel(h: Harness, soundId: String = "s1") = SoundPageViewModel(
        sounds = SoundsRepository(h.api, ErrorMapper(json)) { it },
        urlResolver = MediaUrlResolver(
            ApiConfig(
                baseUrl = "http://127.0.0.1:8080",
                wsBaseUrl = "ws://127.0.0.1:8093",
                clientVersion = "test",
                environment = "test",
                isDebug = true,
            ),
        ),
        soundEntry = h.soundEntry,
        reelsEntry = h.reelsEntry,
        savedStateHandle = SavedStateHandle(mapOf(SoundPageViewModel.SOUND_ID_ARG to soundId)),
    )

    @Test
    fun `the first page carries the sound, the origin first and marked, and the reels`() {
        val h = Harness(ScriptedSoundsApi(mapOf(null to page("a", "b", origin = "o", next = "c1"))))

        val state = viewModel(h).state.value

        assertThat(state.phase).isEqualTo(SoundPagePhase.Ready)
        assertThat(state.sound?.title).isEqualTo("Original sound - Asha")
        assertThat(state.sound?.useCount).isEqualTo(3)
        assertThat(state.tiles.map { it.reel.id to it.isOrigin })
            .containsExactly("o" to true, "a" to false, "b" to false).inOrder()
        assertThat(state.canLoadMore).isTrue()
        assertThat(state.isEmpty).isFalse()
        assertThat(h.api.asked).containsExactly(null)
    }

    @Test
    fun `the next page is asked with the cursor, and the origin is not repeated`() {
        val h = Harness(
            ScriptedSoundsApi(
                mapOf(
                    null to page("a", origin = "o", next = "c1"),
                    "c1" to page("b", "o", "a", "c"),
                ),
            ),
        )
        val vm = viewModel(h)

        vm.loadMore()

        assertThat(h.api.asked).containsExactly(null, "c1").inOrder()
        assertThat(vm.state.value.tiles.map { it.reel.id }).containsExactly("o", "a", "b", "c").inOrder()
        assertThat(vm.state.value.canLoadMore).isFalse()
        assertThat(vm.state.value.loadingMore).isFalse()
    }

    @Test
    fun `nothing more is asked for at the end, or while a page is on its way`() {
        val h = Harness(ScriptedSoundsApi(mapOf(null to page("a"))))
        val vm = viewModel(h)

        vm.loadMore()
        vm.loadMore()

        assertThat(h.api.asked).containsExactly(null)
    }

    @Test
    fun `a later page failing keeps the tiles and offers to try again`() {
        val h = Harness(ScriptedSoundsApi(mapOf(null to page("a", next = "c1"))))
        val vm = viewModel(h)

        vm.loadMore()

        val state = vm.state.value
        assertThat(state.phase).isEqualTo(SoundPagePhase.Ready)
        assertThat(state.tiles.map { it.reel.id }).containsExactly("a")
        assertThat(state.moreFailed).isTrue()
        assertThat(state.loadingMore).isFalse()
        assertThat(state.canLoadMore).isTrue()
    }

    /** The server gives one answer for a missing sound and one this viewer may not hear; so does the page. */
    @Test
    fun `a sound that is gone reads as gone, not as an error`() {
        val vm = viewModel(Harness(ScriptedSoundsApi(mapOf(null to notFound()))))

        assertThat(vm.state.value.phase).isEqualTo(SoundPagePhase.Gone)
        assertThat(vm.state.value.tiles).isEmpty()
    }

    @Test
    fun `any other failure of the first page is an error with a retry, and retry asks again`() {
        var refuse = true
        val h = Harness(
            ScriptedSoundsApi(mapOf(null to { if (refuse) refused("SOUND_UNAVAILABLE")() else page("a")() })),
        )
        val vm = viewModel(h)
        assertThat(vm.state.value.phase).isInstanceOf(SoundPagePhase.Failed::class.java)

        refuse = false
        vm.load()

        assertThat(vm.state.value.phase).isEqualTo(SoundPagePhase.Ready)
        assertThat(vm.state.value.tiles.map { it.reel.id }).containsExactly("a")
        assertThat(h.api.asked).containsExactly(null, null)
    }

    @Test
    fun `the phase of a first-page failure is by its kind`() {
        assertThat(soundPagePhase(AppError.NotFound())).isEqualTo(SoundPagePhase.Gone)
        assertThat(soundPagePhase(AppError.NoNetwork()))
            .isEqualTo(SoundPagePhase.Failed("You're offline. Check your connection and try again."))
        assertThat(soundPagePhase(AppError.Timeout()))
            .isEqualTo(SoundPagePhase.Failed("That took too long. Try again."))
        assertThat(soundPagePhase(AppError.Unknown(code = "SOUND_UNAVAILABLE", statusCode = 503)))
            .isEqualTo(SoundPagePhase.Failed("We couldn't load this sound."))
    }

    @Test
    fun `a sound nobody has used yet is empty, not gone`() {
        val vm = viewModel(Harness(ScriptedSoundsApi(mapOf(null to page()))))

        assertThat(vm.state.value.phase).isEqualTo(SoundPagePhase.Ready)
        assertThat(vm.state.value.isEmpty).isTrue()
        assertThat(vm.state.value.sound).isNotNull()
    }

    @Test
    fun `the state is the pages, read once`() {
        val first = SoundReelsPage(
            sound = ReelSoundWire(id = "s1", title = "x").toReelSound(),
            origin = null,
            items = emptyList(),
            nextCursor = "c1",
        )
        val second = first.copy(sound = null, nextCursor = null)

        assertThat(soundPageState(listOf(first, second)).sound?.id).isEqualTo("s1")
        assertThat(soundPageState(listOf(first)).canLoadMore).isTrue()
        assertThat(soundPageState(listOf(first, second)).canLoadMore).isFalse()
        assertThat(soundPageState(emptyList()).sound).isNull()
    }

    @Test
    fun `use this sound leaves the sound for the create flow, at start 0`() {
        val h = Harness(ScriptedSoundsApi(mapOf(null to page("a"))))
        val vm = viewModel(h)

        assertThat(vm.chooseSound()).isTrue()

        assertThat(h.soundEntry.chosen.value).isEqualTo(
            ChosenSound(id = "s1", title = "Original sound - Asha", artist = "Asha", durationMs = 28_400L),
        )
    }

    @Test
    fun `use this sound has nothing to leave before the sound is known`() {
        val h = Harness(ScriptedSoundsApi(mapOf(null to notFound())))

        assertThat(viewModel(h).chooseSound()).isFalse()
        assertThat(h.soundEntry.chosen.value).isNull()
    }

    @Test
    fun `a tile leaves its reel for the Reels tab`() {
        val h = Harness(ScriptedSoundsApi(mapOf(null to page("a"))))

        viewModel(h).openReel("a")

        assertThat(h.reelsEntry.requested.value).isEqualTo("a")
    }

    @Test
    fun `a tile's still is the transcode's own thumbnail when there is no cover`() {
        val vm = viewModel(Harness(ScriptedSoundsApi(mapOf(null to page("a")))))

        assertThat(vm.posterUrl(vm.state.value.tiles.single().reel)).isEqualTo("https://obj/a.jpg")
    }

    @Test
    fun `the sound id comes from the route`() {
        val h = Harness(ScriptedSoundsApi(mapOf(null to page("a"))))

        assertThat(viewModel(h, soundId = "s-9").soundId).isEqualTo("s-9")
    }
}
