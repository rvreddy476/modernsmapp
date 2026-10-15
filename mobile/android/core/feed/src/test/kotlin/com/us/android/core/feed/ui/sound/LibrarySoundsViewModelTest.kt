package com.us.android.core.feed.ui.sound

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.LIBRARY_UNAVAILABLE
import com.us.android.core.feed.data.LibrarySound
import com.us.android.core.feed.data.LibrarySoundUseDto
import com.us.android.core.feed.data.LibrarySoundUseFailure
import com.us.android.core.feed.data.LibrarySoundsRepository
import com.us.android.core.feed.data.LibraryTrackDto
import com.us.android.core.feed.data.LibraryTracksDto
import com.us.android.core.feed.data.SoundReelsDto
import com.us.android.core.feed.data.SoundRowDto
import com.us.android.core.feed.data.SoundsApi
import com.us.android.core.feed.data.UseSoundDto
import com.us.android.core.media.SoundEntry
import com.us.android.core.media.sound.SoundPreview
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import java.io.IOException

/**
 * The Library list (2026-10-15), shared by the reel form's sheet and the
 * Creator Hub's Sounds tab.
 *
 * What this protects: "Use" leaving the ANSWER's `audio_tracks` id in
 * SoundEntry — never the library id — so the create flow takes it like any
 * other sound; a refusal said in the list with nothing chosen; the preview
 * playing only on a press, one row at a time, and stopping when the list
 * changes, a choice is made, or the list closes; and the genre filter and
 * paging.
 */
class LibrarySoundsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = NetworkModule.provideJson()

    /** Answers from queues; a missing answer is a network failure. Records what was asked. */
    private class FakeApi : SoundsApi {
        val pages = ArrayDeque<() -> ApiEnvelope<LibraryTracksDto>>()
        val uses = ArrayDeque<() -> ApiEnvelope<LibrarySoundUseDto>>()
        val calls = mutableListOf<String>()

        override suspend fun audioLibrary(genre: String?, limit: Int, offset: Int): ApiEnvelope<LibraryTracksDto> {
            calls += "list:${genre.orEmpty()}:$limit:$offset"
            return (pages.removeFirstOrNull() ?: throw IOException("offline"))()
        }

        override suspend fun useLibrarySound(libraryId: String): ApiEnvelope<LibrarySoundUseDto> {
            calls += "use:$libraryId"
            return (uses.removeFirstOrNull() ?: throw IOException("offline"))()
        }

        override suspend fun useSound(postId: String): ApiEnvelope<UseSoundDto> = error("not the library")
        override suspend fun reelsBySound(soundId: String, limit: Int, cursor: String?): ApiEnvelope<SoundReelsDto> =
            error("not the library")
        override suspend fun sound(soundId: String): ApiEnvelope<SoundRowDto> = error("not the library")
    }

    /** The one player, as the list sees it. */
    private class FakePreview : LibrarySoundPreview {
        override val state = MutableStateFlow(SoundPreview.IDLE)
        val calls = mutableListOf<String>()
        override fun toggle(libraryId: String) {
            calls += "toggle:$libraryId"
            state.value = SoundPreview.LOADING
        }
        override fun pause() {
            calls += "pause"
        }
        override fun stop() {
            calls += "stop"
            state.value = SoundPreview.IDLE
        }
        override fun release() {
            calls += "release"
        }
    }

    private val api = FakeApi()
    private val preview = FakePreview()
    private val entry = SoundEntry()

    private fun viewModel() = LibrarySoundsViewModel(LibrarySoundsRepository(api, ErrorMapper(json)), entry, preview)

    private fun row(id: String, genre: String? = "pop", audioUrl: String = "/v1/audio-library/$id/serve") =
        LibraryTrackDto(id = id, title = "Sound $id", artist = "Studio", durationMs = 20_000L, genre = genre, audioUrl = audioUrl)

    private fun page(vararg rows: LibraryTrackDto): () -> ApiEnvelope<LibraryTracksDto> =
        { ApiEnvelope(data = LibraryTracksDto(tracks = rows.toList(), total = rows.size)) }

    private fun refusal(status: Int, code: String): () -> Nothing = {
        throw HttpException(
            Response.error<Any>(
                status,
                """{"error":{"code":"$code","message":"m"}}""".toResponseBody("application/json".toMediaType()),
            ),
        )
    }

    private val StateFlow<LibrarySoundsUiState>.sounds: List<LibrarySound> get() = value.sounds

    // ── the list ────────────────────────────────────────────────────────

    @Test
    fun `it opens on every genre's first page, with nothing playing`() {
        api.pages += page(row("l1"), row("l2", genre = "lo-fi"))

        val vm = viewModel()

        assertThat(api.calls).containsExactly("list::20:0")
        assertThat(vm.state.sounds.map { it.id }).containsExactly("l1", "l2").inOrder()
        assertThat(vm.state.value.loading).isFalse()
        assertThat(vm.state.value.genres).containsExactly("lo-fi", "pop").inOrder()
        assertThat(vm.state.value.hasMore).isFalse()
        // Founder rule: a preview plays only on a press.
        assertThat(preview.calls).isEmpty()
    }

    @Test
    fun `a failed first page says so, and Try again reads it`() {
        val vm = viewModel()
        assertThat(vm.state.value.error).isEqualTo(LIBRARY_UNAVAILABLE)

        api.pages += page(row("l1"))
        vm.load(more = false)

        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.sounds.map { it.id }).containsExactly("l1")
    }

    @Test
    fun `a genre asks again from the start, stops the preview, and keeps the other genres on offer`() {
        api.pages += page(row("l1"), row("l2", genre = "lo-fi"))
        val vm = viewModel()
        vm.togglePreview(vm.state.sounds.first())

        api.pages += page(row("l2", genre = "lo-fi"))
        vm.onGenre("lo-fi")

        assertThat(api.calls.last()).isEqualTo("list:lo-fi:20:0")
        assertThat(vm.state.sounds.map { it.id }).containsExactly("l2")
        assertThat(vm.state.value.genres).containsExactly("lo-fi", "pop").inOrder()
        assertThat(preview.calls.last()).isEqualTo("stop")
        assertThat(vm.state.value.previewingId).isNull()
    }

    @Test
    fun `a full page offers more, and the next page follows on`() {
        api.pages += page(*Array(20) { row("a$it") })
        val vm = viewModel()
        assertThat(vm.state.value.nextOffset).isEqualTo(20)

        api.pages += page(row("b1"))
        vm.load(more = true)

        assertThat(api.calls.last()).isEqualTo("list::20:20")
        assertThat(vm.state.sounds).hasSize(21)
        assertThat(vm.state.value.hasMore).isFalse()
    }

    @Test
    fun `only the next page failing keeps the rows shown`() {
        api.pages += page(*Array(20) { row("a$it") })
        val vm = viewModel()

        vm.load(more = true)

        assertThat(vm.state.value.moreFailed).isTrue()
        assertThat(vm.state.sounds).hasSize(20)
        assertThat(vm.state.value.error).isNull()
    }

    // ── the preview ─────────────────────────────────────────────────────

    @Test
    fun `a press plays that row by its library id, and a press on another moves the one player`() {
        api.pages += page(row("l1"), row("l2"))
        val vm = viewModel()

        vm.togglePreview(vm.state.sounds[0])
        assertThat(vm.state.value.previewingId).isEqualTo("l1")

        vm.togglePreview(vm.state.sounds[1])
        assertThat(vm.state.value.previewingId).isEqualTo("l2")
        assertThat(preview.calls).containsExactly("toggle:l1", "toggle:l2").inOrder()
    }

    @Test
    fun `a row with nothing to play is never sent to the player`() {
        api.pages += page(row("l1", audioUrl = ""))
        val vm = viewModel()

        vm.togglePreview(vm.state.sounds.single())

        assertThat(vm.state.sounds.single().playable).isFalse()
        assertThat(preview.calls).isEmpty()
        assertThat(vm.state.value.previewingId).isNull()
    }

    @Test
    fun `closing the list stops the preview, and leaving the foreground pauses it`() {
        api.pages += page(row("l1"))
        val vm = viewModel()
        vm.togglePreview(vm.state.sounds.single())

        vm.pausePreview()
        assertThat(preview.calls.last()).isEqualTo("pause")
        assertThat(vm.state.value.previewingId).isEqualTo("l1")

        vm.stopPreview()
        assertThat(preview.calls.last()).isEqualTo("stop")
        assertThat(vm.state.value.previewingId).isNull()
        assertThat(vm.previewState.value).isEqualTo(SoundPreview.IDLE)
    }

    // ── "Use" ───────────────────────────────────────────────────────────

    @Test
    fun `Use leaves the answer's audio_tracks id in SoundEntry, never the library id`() {
        api.pages += page(row("lib-1"))
        api.uses += {
            ApiEnvelope(
                data = LibrarySoundUseDto(
                    audioTrackId = "track-1",
                    sound = SoundRowDto(id = "track-1", title = "Sound lib-1", artist = "Studio", durationMs = 20_100L),
                ),
            )
        }
        val vm = viewModel()
        vm.togglePreview(vm.state.sounds.single())

        vm.use(vm.state.sounds.single())

        assertThat(api.calls.last()).isEqualTo("use:lib-1")
        val chosen = entry.chosen.value
        assertThat(chosen?.id).isEqualTo("track-1")
        assertThat(chosen?.id).isNotEqualTo("lib-1")
        assertThat(chosen?.title).isEqualTo("Sound lib-1")
        assertThat(vm.state.value.chosenSoundId).isEqualTo("track-1")
        assertThat(vm.state.value.usingId).isNull()
        // A choice stops the preview first.
        assertThat(preview.calls).containsExactly("toggle:lib-1", "stop").inOrder()

        vm.onChosenHandled()
        assertThat(vm.state.value.chosenSoundId).isNull()
    }

    @Test
    fun `each refusal is said in the list, and nothing is chosen`() {
        val cases = listOf(
            refusal(401, "UNAUTHORIZED") to LibrarySoundUseFailure.SIGNED_OUT,
            refusal(404, "NOT_FOUND") to LibrarySoundUseFailure.GONE,
            refusal(422, "NOT_STORED") to LibrarySoundUseFailure.NOT_STORED,
            refusal(503, "SOUND_UNAVAILABLE") to LibrarySoundUseFailure.UNAVAILABLE,
        )
        api.pages += page(row("lib-1"))
        val vm = viewModel()

        for ((answer, expected) in cases) {
            api.uses += answer
            vm.use(vm.state.sounds.single())

            assertThat(vm.state.value.useMessage).isEqualTo(expected.message)
            assertThat(vm.state.value.usingId).isNull()
            assertThat(vm.state.value.chosenSoundId).isNull()
            assertThat(entry.chosen.value).isNull()
        }
    }

    @Test
    fun `an answer that names no track chooses nothing`() {
        api.pages += page(row("lib-1"))
        api.uses += { ApiEnvelope(data = LibrarySoundUseDto()) }
        val vm = viewModel()

        vm.use(vm.state.sounds.single())

        assertThat(entry.chosen.value).isNull()
        assertThat(vm.state.value.useMessage).isEqualTo(LibrarySoundUseFailure.OTHER.message)
    }
}
