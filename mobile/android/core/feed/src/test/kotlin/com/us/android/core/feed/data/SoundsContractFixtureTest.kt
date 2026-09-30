package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.dto.FeedSoundDto
import com.us.android.core.model.ReelSound
import com.us.android.core.model.toReelSound
import com.us.android.core.network.di.NetworkModule
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Original sounds, against the backend's golden files (contract 2.5).
 *
 * `post_with_sound.json`, `use_sound.json` and `by_sound.json` are
 * post-service's handler goldens
 * (`internal/http/testdata/contracts/sounds`), copied byte for byte into
 * `src/test/resources/contracts/sounds`. `sound.json` is media-service's row:
 * that service publishes no golden, so the copy is the web's
 * (`postbook-ui/src/features/reels/__tests__/contracts/sounds/sound.json`),
 * which is the real handler output.
 *
 * What this protects: a key renamed on either side. Where a DTO claims the
 * WHOLE shape — the sound object, the `{sound}` answer, the media-service
 * row — decoding is STRICT, so a key the server adds or renames fails here
 * rather than defaulting silently in production, where the platform Json
 * ignores unknown keys. A post row is decoded with the platform Json:
 * `FeedItemDto` reads a post, it does not claim every key of one.
 */
class SoundsContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }
    private val app = NetworkModule.provideJson()

    private val contractsDir = File("src/test/resources/contracts/sounds")

    private val parsers: Map<String, (raw: String) -> Unit> = mapOf(
        // A LISTING row carrying contract 2.1: the three new keys beside the three that were already there.
        "post_with_sound.json" to { raw ->
            val keys = strict.parseToJsonElement(raw).jsonObject
            assertThat(keys.keys).containsAtLeast(
                "audio_track_id",
                "audio_start_ms",
                "sound",
                "remix_setting",
                "original_audio_volume",
                "overlay_audio_volume",
            )
            val wire = strictSound(keys.getValue("sound").jsonObject)

            val dto = app.decodeFromString(FeedItemDto.serializer(), raw)
            assertThat(dto.audioTrackId).isEqualTo(wire.id)
            assertThat(dto.audioStartMs).isEqualTo(1_500L)
            assertThat(dto.sound).isEqualTo(wire)
            assertThat(dto.originalAudioVolume).isEqualTo(0.2)
            assertThat(dto.overlayAudioVolume).isEqualTo(1.0)
            assertThat(dto.remixSetting).isEqualTo("allow")
            // post-service's listing counts likes and comments, and nothing else.
            assertThat(dto.counts.shares).isNull()
            assertThat(dto.counts.saves).isNull()
            assertThat(dto.counts.bookmarks).isNull()

            val item = dto.toDomain()
            assertThat(item.sound).isEqualTo(
                ReelSound(
                    id = "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
                    title = "Original sound - Asha",
                    artist = "Asha",
                    startMs = 1_500L,
                    durationMs = 28_400L,
                    useCount = 3,
                    sourcePostId = "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
                    creatorUserId = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
                ),
            )
            assertThat(item.originalVolume).isEqualTo(0.2)
            assertThat(item.overlayVolume).isEqualTo(1.0)
            assertThat(item.soundReuseAllowed).isTrue()
            assertThat(item.title).isEqualTo("Monsoon walk, my take")
            assertThat(item.hashtags).containsExactly("reels")
            assertThat(item.counts.shares).isNull()
            assertThat(item.counts.saves).isNull()
        },
        // POST /v1/posts/{id}/sound 200 — the `data` of contract 2.2: `{sound}`, start 0.
        "use_sound.json" to { raw ->
            val dto = strict.decodeFromString(UseSoundDto.serializer(), raw)
            val sound = checkNotNull(dto.sound?.toWire().toReelSound())
            assertThat(sound.id).isEqualTo("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
            assertThat(sound.title).isEqualTo("Original sound - Asha")
            assertThat(sound.artist).isEqualTo("Asha")
            assertThat(sound.startMs).isEqualTo(0L)
            assertThat(sound.durationMs).isEqualTo(28_400L)
            assertThat(sound.useCount).isEqualTo(0)
            assertThat(sound.sourcePostId).isEqualTo("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
            assertThat(sound.creatorUserId).isEqualTo("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee")
        },
        // GET /v1/posts/by-sound/{id} 200 — the `data` of contract 2.3: the origin and two reels.
        "by_sound.json" to { raw ->
            val keys = strict.parseToJsonElement(raw).jsonObject
            assertThat(keys.keys).containsExactly("sound", "origin", "items")
            strictSound(keys.getValue("sound").jsonObject)
            keys.getValue("items").jsonArray.forEach { strictSound(it.jsonObject.getValue("sound").jsonObject) }

            val page = app.decodeFromString(SoundReelsDto.serializer(), raw).toPage(meta = null)
            assertThat(page.sound?.id).isEqualTo("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
            assertThat(page.sound?.useCount).isEqualTo(3)
            assertThat(page.origin?.id).isEqualTo("dddddddd-dddd-4ddd-8ddd-dddddddddddd")
            // The origin plays its OWN audio: it is where the sound came from.
            assertThat(page.origin?.sound).isNull()
            assertThat(page.items.map { it.id }).containsExactly(
                "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1",
                "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1",
            ).inOrder()
            assertThat(page.items.map { it.sound?.startMs }).containsExactly(1_500L, 0L).inOrder()
            assertThat(page.items.map { it.originalVolume }).containsExactly(0.2, 0.2).inOrder()
            assertThat(page.nextCursor).isNull()
            assertThat(soundReelTiles(listOf(page)).map { it.reel.id to it.isOrigin }).containsExactly(
                "dddddddd-dddd-4ddd-8ddd-dddddddddddd" to true,
                "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1" to false,
                "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1" to false,
            ).inOrder()
        },
        // GET /v1/audio/{id} 200 — media-service's row (contract 1.4), in ITS spelling.
        "sound.json" to { raw ->
            val keys = strict.parseToJsonElement(raw).jsonObject
            assertThat(keys.keys).containsNoneOf("audio_key", "waveform_key")
            assertThat(keys["source_reel_id"]).isEqualTo(keys["source_post_id"])

            val dto = strict.decodeFromString(SoundRowDto.serializer(), raw)
            assertThat(dto.status).isEqualTo("ready")
            assertThat(dto.isOriginal).isTrue()
            assertThat(dto.toReelSound()).isEqualTo(
                ReelSound(
                    id = "085f7b72-eba4-43f4-b303-ddf92b2d64ca",
                    title = "Kitchen take",
                    artist = "Asha",
                    startMs = 0L,
                    durationMs = 11_901L,
                    useCount = 0,
                    sourcePostId = "38f38afa-b916-4b5a-8772-240ae9804f3c",
                    creatorUserId = "0396c23a-b772-48de-a766-3b20424ce4e1",
                ),
            )
        },
    )

    /** The sound object decodes STRICTLY: exactly the eight keys of contract 2.1, no more. */
    private fun strictSound(sound: JsonObject): FeedSoundDto {
        assertThat(sound.keys).containsExactly(
            "id",
            "title",
            "artist",
            "duration_ms",
            "start_ms",
            "use_count",
            "source_post_id",
            "creator_user_id",
        )
        return strict.decodeFromJsonElement(FeedSoundDto.serializer(), sound)
    }

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
    fun `the post-service copies are byte-identical to its goldens`() {
        val source = File("../../../../Architecture/services/post-service/internal/http/testdata/contracts/sounds")
        assumeTrue("post-service is not checked out beside the app", source.isDirectory)

        for (name in POST_SERVICE_FIXTURES) {
            val original = File(source, name)
            assertThat(original.exists()).isTrue()
            assertThat(File(contractsDir, name).readBytes()).isEqualTo(original.readBytes())
        }
        // Every golden the backend publishes for sounds has a copy here.
        assertThat(source.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name })
            .containsExactlyElementsIn(POST_SERVICE_FIXTURES)
    }

    @Test
    fun `the sound row is byte-identical to the web's copy of the handler output`() {
        val original = File("../../../../../postbook-ui/src/features/reels/__tests__/contracts/sounds/sound.json")
        assumeTrue("postbook-ui is not checked out beside the repository", original.isFile)

        assertThat(File(contractsDir, "sound.json").readBytes()).isEqualTo(original.readBytes())
    }

    private companion object {
        val POST_SERVICE_FIXTURES = listOf("post_with_sound.json", "use_sound.json", "by_sound.json")
    }
}
