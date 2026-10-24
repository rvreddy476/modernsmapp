package com.us.android.core.feed.ui.sound

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.TRENDING_UNAVAILABLE
import com.us.android.core.feed.data.TrendingFilter
import com.us.android.core.feed.data.TrendingSoundsApi
import com.us.android.core.feed.data.TrendingSoundsRepository
import com.us.android.core.media.SoundEntry
import com.us.android.core.media.sound.SoundPreview
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import org.junit.Rule
import org.junit.Test
import java.io.IOException

/**
 * The Trending list (2026-10-24): the picker's shelf, the full list's All /
 * Rising tabs, and the pushed screen, on one ViewModel.
 *
 * What this protects: All read on open and nothing else; Rising read on its
 * first visit only; Load more replaying the cursor and keeping the rows
 * shown when it fails; the shelf being the top twenty of All whichever tab
 * is on screen; the preview playing only on a press, by the sound's
 * `audio_tracks` id, and stopping on a tab switch or a choice; and "Use"
 * leaving that same id in SoundEntry with no server call.
 */
class TrendingSoundsViewModelTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private val json = NetworkModule.provideJson()

    /** Answers from queues per filter; a missing answer is a network failure. Records what was asked. */
    private class FakeApi : TrendingSoundsApi {
        val answers = mutableMapOf<String, ArrayDeque<() -> ApiEnvelope<JsonElement>>>()
        val calls = mutableListOf<String>()

        fun enqueue(filter: String, answer: () -> ApiEnvelope<JsonElement>) {
            answers.getOrPut(filter) { ArrayDeque() } += answer
        }

        override suspend fun trending(filter: String, limit: Int, cursor: String?): ApiEnvelope<JsonElement> {
            calls += "$filter:$limit:${cursor.orEmpty()}"
            return (answers[filter]?.removeFirstOrNull() ?: throw IOException("offline"))()
        }

        override suspend fun sound(soundId: String): ApiEnvelope<JsonElement> = error("not the list")
    }

    private class FakePreview : TrendingSoundPreview {
        override val state = MutableStateFlow(SoundPreview.IDLE)
        val calls = mutableListOf<String>()
        override fun toggle(soundId: String) {
            calls += "toggle:$soundId"
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

    private fun viewModel() =
        TrendingSoundsViewModel(TrendingSoundsRepository(api, ErrorMapper(json), TEST_CONFIG), entry, preview)

    private fun row(id: String, rank: Int, rising: Boolean = false) =
        """{"rank":$rank,"uses_7d":12,"uses_prev_7d":3,"rising":$rising,
           "sound":{"id":"$id","title":"Sound $id","artist":"Asha","duration_ms":20000}}"""

    private fun page(vararg rows: String, next: String? = null): () -> ApiEnvelope<JsonElement> = {
        ApiEnvelope(data = Json.parseToJsonElement(rows.joinToString(",", "[", "]")), meta = ApiMeta(nextCursor = next))
    }

    private fun ranked(count: Int, prefix: String = "s", start: Int = 1) =
        (start until start + count).map { row("$prefix$it", it) }.toTypedArray()

    // ── the tabs ────────────────────────────────────────────────────────

    @Test
    fun `it opens on All's first page, with nothing playing and Rising not yet read`() {
        api.enqueue("all", page(row("a", 1), row("b", 2, rising = true)))

        val vm = viewModel()

        assertThat(api.calls).containsExactly("all:20:")
        val state = vm.state.value
        assertThat(state.filter).isEqualTo(TrendingFilter.ALL)
        assertThat(state.current.sounds.map { it.sound.id }).containsExactly("a", "b").inOrder()
        assertThat(state.current.loading).isFalse()
        assertThat(state.current.loaded).isTrue()
        assertThat(state.rising.loaded).isFalse()
        assertThat(preview.calls).isEmpty()
    }

    @Test
    fun `Rising is read on its first visit only, and switching back reads nothing`() {
        api.enqueue("all", page(row("a", 1)))
        api.enqueue("rising", page(row("r", 1, rising = true)))
        val vm = viewModel()

        vm.onFilter(TrendingFilter.RISING)
        assertThat(vm.state.value.current.sounds.map { it.sound.id }).containsExactly("r")

        vm.onFilter(TrendingFilter.ALL)
        vm.onFilter(TrendingFilter.RISING)

        assertThat(api.calls).containsExactly("all:20:", "rising:20:").inOrder()
        assertThat(vm.state.value.all.sounds.map { it.sound.id }).containsExactly("a")
    }

    @Test
    fun `an empty first page is read once - an empty tab is not asked again`() {
        api.enqueue("all", page())
        api.enqueue("rising", page())
        val vm = viewModel()

        vm.onFilter(TrendingFilter.RISING)
        vm.onFilter(TrendingFilter.ALL)
        vm.onFilter(TrendingFilter.RISING)

        assertThat(api.calls).containsExactly("all:20:", "rising:20:").inOrder()
        assertThat(vm.state.value.current.sounds).isEmpty()
        assertThat(vm.state.value.current.error).isNull()
    }

    @Test
    fun `a failed first page says so, and Try again reads that tab`() {
        val vm = viewModel()
        assertThat(vm.state.value.current.error).isEqualTo(TRENDING_UNAVAILABLE)

        api.enqueue("all", page(row("a", 1)))
        vm.load(more = false)

        assertThat(vm.state.value.current.error).isNull()
        assertThat(vm.state.value.current.sounds.map { it.sound.id }).containsExactly("a")
    }

    // ── paging ──────────────────────────────────────────────────────────

    @Test
    fun `Load more replays the cursor and follows on, nothing twice`() {
        api.enqueue("all", page(*ranked(20), next = "c2"))
        val vm = viewModel()
        assertThat(vm.state.value.current.hasMore).isTrue()

        api.enqueue("all", page(row("s20", 20), *ranked(5, start = 21)))
        vm.load(more = true)

        assertThat(api.calls.last()).isEqualTo("all:20:c2")
        assertThat(vm.state.value.current.sounds).hasSize(25)
        assertThat(vm.state.value.current.sounds.map { it.sound.id }.toSet()).hasSize(25)
        assertThat(vm.state.value.current.hasMore).isFalse()
    }

    @Test
    fun `with no cursor there is nothing more to ask for`() {
        api.enqueue("all", page(row("a", 1)))
        val vm = viewModel()

        vm.load(more = true)

        assertThat(api.calls).containsExactly("all:20:")
    }

    @Test
    fun `only the next page failing keeps the rows shown, and Try again asks the same cursor`() {
        api.enqueue("all", page(*ranked(20), next = "c2"))
        val vm = viewModel()

        vm.load(more = true)

        assertThat(vm.state.value.current.moreFailed).isTrue()
        assertThat(vm.state.value.current.sounds).hasSize(20)
        assertThat(vm.state.value.current.error).isNull()

        api.enqueue("all", page(row("x", 21)))
        vm.load(more = true)
        assertThat(api.calls.takeLast(2)).containsExactly("all:20:c2", "all:20:c2").inOrder()
        assertThat(vm.state.value.current.moreFailed).isFalse()
        assertThat(vm.state.value.current.sounds).hasSize(21)
    }

    // ── the shelf ───────────────────────────────────────────────────────

    @Test
    fun `the shelf is the top twenty of All, whichever tab is on screen`() {
        api.enqueue("all", page(*ranked(20), next = "c2"))
        api.enqueue("all", page(*ranked(5, start = 21)))
        api.enqueue("rising", page(row("r", 1, rising = true)))
        val vm = viewModel()
        vm.load(more = true)

        vm.onFilter(TrendingFilter.RISING)

        val shelf = vm.state.value.shelf
        assertThat(shelf).hasSize(20)
        assertThat(shelf.first().sound.id).isEqualTo("s1")
        assertThat(shelf.last().sound.id).isEqualTo("s20")
    }

    // ── the preview ─────────────────────────────────────────────────────

    @Test
    fun `a press plays that sound by its id, and another press moves the one player`() {
        api.enqueue("all", page(row("a", 1), row("b", 2)))
        val vm = viewModel()
        val (a, b) = vm.state.value.current.sounds

        vm.togglePreview(a)
        assertThat(vm.state.value.previewingId).isEqualTo("a")
        vm.togglePreview(b)

        assertThat(vm.state.value.previewingId).isEqualTo("b")
        assertThat(preview.calls).containsExactly("toggle:a", "toggle:b").inOrder()
    }

    @Test
    fun `a tab switch stops the preview, leaving the foreground pauses it, and closing forgets it`() {
        api.enqueue("all", page(row("a", 1)))
        api.enqueue("rising", page())
        val vm = viewModel()
        vm.togglePreview(vm.state.value.current.sounds.single())

        vm.pausePreview()
        assertThat(preview.calls.last()).isEqualTo("pause")
        assertThat(vm.state.value.previewingId).isEqualTo("a")

        vm.onFilter(TrendingFilter.RISING)
        assertThat(preview.calls.last()).isEqualTo("stop")
        assertThat(vm.state.value.previewingId).isNull()
        assertThat(vm.previewState.value).isEqualTo(SoundPreview.IDLE)
    }

    // ── "Use this sound" ────────────────────────────────────────────────

    @Test
    fun `Use leaves the sound's audio_tracks id in SoundEntry, with no server call, and stops the preview`() {
        api.enqueue("all", page(row("track-1", 1)))
        val vm = viewModel()
        val sound = vm.state.value.current.sounds.single()
        vm.togglePreview(sound)

        vm.use(sound)

        val chosen = entry.chosen.value
        assertThat(chosen?.id).isEqualTo("track-1")
        assertThat(chosen?.title).isEqualTo("Sound track-1")
        assertThat(chosen?.artist).isEqualTo("Asha")
        assertThat(chosen?.durationMs).isEqualTo(20_000L)
        assertThat(vm.state.value.chosenSoundId).isEqualTo("track-1")
        assertThat(api.calls).containsExactly("all:20:")
        assertThat(preview.calls).containsExactly("toggle:track-1", "stop").inOrder()

        vm.onChosenHandled()
        assertThat(vm.state.value.chosenSoundId).isNull()
    }

    private companion object {
        val TEST_CONFIG = ApiConfig(
            baseUrl = "https://api.test/",
            wsBaseUrl = "wss://ws.test",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        )
    }
}
