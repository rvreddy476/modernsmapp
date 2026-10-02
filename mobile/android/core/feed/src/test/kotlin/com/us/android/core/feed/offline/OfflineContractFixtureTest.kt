package com.us.android.core.feed.offline

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Offline copies against post-service's golden files
 * (`internal/http/testdata/contracts/mtube/offline_*.json`), copied byte
 * for byte into `src/test/resources/contracts/offline`.
 *
 * What this protects: a key renamed on either side. The DTOs claim each
 * shape WHOLE, so they decode STRICTLY: a key the server adds or renames
 * fails here rather than defaulting silently in production, where the
 * symptom would be a copy saved with no expiry or a reel with no sound.
 */
class OfflineContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }
    private val contractsDir = File("src/test/resources/contracts/offline")
    private val now = 0L
    private val resolve: (String) -> String? = { path -> "https://api.test$path" }

    private val parsers: Map<String, (raw: String) -> Unit> = mapOf(
        // POST /v1/posts/{id}/offline, a long video.
        "offline_grant.json" to { raw ->
            val grant = strict.decodeFromString(OfflineGrantDto.serializer(), raw).toGrant(resolve, now)!!

            assertThat(grant.postId).isEqualTo("11111111-1111-4111-8111-111111111111")
            assertThat(grant.kind).isEqualTo(OfflineKind.VIDEO)
            assertThat(grant.expiresAtMs).isEqualTo(parseInstantMs("2026-10-27T12:00:00Z"))
            assertThat(grant.recheckAfterSeconds).isEqualTo(172_800L)
            assertThat(grant.title).isEqualTo("Friday build")
            assertThat(grant.channelName).isEqualTo("Raghu Builds")
            assertThat(grant.durationMs).isEqualTo(725_000L)
            assertThat(grant.posterUrl)
                .isEqualTo("https://api.test/v1/media/55555555-5555-4555-8555-555555555555/serve")
            assertThat(grant.video).isEqualTo(
                OfflineGrantStream(
                    url = "https://api.test/v1/media/44444444-4444-4444-8444-444444444444/serve/720p",
                    mime = "video/mp4",
                    sizeBytes = 184_320_000L,
                ),
            )
            assertThat(grant.captions.single().language).isEqualTo("en")
            assertThat(grant.captions.single().label).isEqualTo("English")
            assertThat(grant.sound).isNull()
        },
        // POST /v1/posts/{id}/offline, a reel that plays an added sound with its own audio muted.
        "offline_grant_reel.json" to { raw ->
            val grant = strict.decodeFromString(OfflineGrantDto.serializer(), raw).toGrant(resolve, now)!!

            assertThat(grant.kind).isEqualTo(OfflineKind.REEL)
            assertThat(grant.video.url).endsWith("/serve/480p")
            assertThat(grant.video.sizeBytes).isEqualTo(6_144_000L)
            val sound = grant.sound!!
            assertThat(sound.stream.url)
                .isEqualTo("https://api.test/v1/audio/cccccccc-cccc-4ccc-8ccc-cccccccccccc/serve")
            assertThat(sound.stream.mime).isEqualTo("audio/mp4")
            // The fixture sends no size for the sound: unknown, not zero bytes expected.
            assertThat(sound.stream.sizeBytes).isEqualTo(0L)
            assertThat(sound.startMs).isEqualTo(1_500L)
            // 0 is the creator's mute, a real value.
            assertThat(sound.originalVolume).isEqualTo(0.0)
            assertThat(sound.overlayVolume).isEqualTo(1.0)
        },
        // POST /v1/posts/offline/check.
        "offline_check.json" to { raw ->
            val answers = strict.decodeFromString(ListSerializer(OfflineCheckDto.serializer()), raw).toAnswers()

            assertThat(answers).containsExactly(
                "11111111-1111-4111-8111-111111111111",
                OfflineCheckAnswer.Valid(parseInstantMs("2026-10-27T12:00:00Z")),
                "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
                OfflineCheckAnswer.Invalid("not_allowed"),
                "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
                OfflineCheckAnswer.Invalid("expired"),
                "88888888-8888-4888-8888-888888888888",
                OfflineCheckAnswer.Invalid("unknown"),
            )
        },
        // GET /v1/posts/offline: the grant's cards without `media.path`.
        "offline_list.json" to { raw ->
            val rows = strict.decodeFromString(ListSerializer(OfflineGrantDto.serializer()), raw)

            assertThat(rows.postIds()).containsExactly(
                "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1",
                "11111111-1111-4111-8111-111111111111",
            ).inOrder()
            assertThat(rows.map { it.contentType }).containsExactly("flick", "long_video").inOrder()
            // No path, by contract: a list row is not something to store.
            assertThat(rows.map { it.media?.path }).containsExactly("", "")
            assertThat(rows.mapNotNull { it.toGrant(resolve, now) }).isEmpty()
        },
    )

    private fun fixtures(): Set<String> =
        contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()

    @Test
    fun `every fixture has a parser and every parser a fixture`() {
        assertThat(fixtures()).isNotEmpty()
        assertThat(fixtures() - parsers.keys).isEmpty()
        assertThat(parsers.keys - fixtures()).isEmpty()
    }

    @Test
    fun `every fixture decodes strictly into its DTO and maps into the domain`() {
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
    fun `the copies are byte-identical to post-service's goldens`() {
        val source = File("../../../../Architecture/services/post-service/internal/http/testdata/contracts/mtube")
        assumeTrue("post-service is not checked out beside the app", source.isDirectory)

        for (name in parsers.keys) {
            val original = File(source, name)
            assertThat(original.exists()).isTrue()
            assertThat(File(contractsDir, name).readBytes()).isEqualTo(original.readBytes())
        }
    }
}
