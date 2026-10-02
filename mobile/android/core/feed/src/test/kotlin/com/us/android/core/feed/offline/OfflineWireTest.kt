package com.us.android.core.feed.offline

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import kotlinx.serialization.builtins.ListSerializer
import org.junit.Test

/**
 * The offline routes' wire shapes, read leniently, against inline bodies
 * shaped like the contract.
 *
 * post-service is Go: it sends `""`, `0` and `null` where it has nothing to
 * say and may leave a key out. What this protects is each of those reading
 * as "not said" with a stated fallback, the two that are NOT "nothing" (a
 * volume of 0 is the creator's mute; an expiry that is missing is thirty
 * days, never forever), and a grant with nothing to store being refused
 * rather than stored empty.
 */
class OfflineWireTest {

    private val json = NetworkModule.provideJson()
    private val now = 1_000_000L
    private val resolve: (String) -> String? = { path -> "https://api.test$path" }

    private fun grantOf(data: String): OfflineGrant? =
        json.decodeFromString(ApiEnvelope.serializer(OfflineGrantDto.serializer()), """{"data":$data}""")
            .data!!
            .toGrant(resolve, now)

    private fun answersOf(data: String): Map<String, OfflineCheckAnswer> =
        json.decodeFromString(
            ApiEnvelope.serializer(ListSerializer(OfflineCheckDto.serializer())),
            """{"data":$data}""",
        ).data.orEmpty().toAnswers()

    private val media = """"media":{"media_id":"m1","variant":"720p","path":"/v1/media/m1/serve/720p",""" +
        """"mime":"video/mp4","size_bytes":123}"""

    // ── The grant ───────────────────────────────────────────────────────

    @Test
    fun `a full grant reads as the contract says`() {
        val grant = grantOf(
            """{"post_id":"p1","content_type":"flick","expires_at":"2026-10-27T12:00:00Z","recheck_after_seconds":3600,
               "title":"Friday build","channel_name":"Raghu Builds","duration_ms":725000,
               "poster_path":"/v1/media/c1/serve",$media,
               "captions":[{"lang":"en","label":"English","path":"/v1/subtitles/m1/track/en.vtt"}],
               "sound":{"path":"/v1/audio/s1/serve","mime":"audio/mp4","size_bytes":77,"start_ms":1500,
                        "original_volume":0.2,"overlay_volume":0.8}}""",
        )!!

        assertThat(grant.postId).isEqualTo("p1")
        assertThat(grant.kind).isEqualTo(OfflineKind.REEL)
        assertThat(grant.expiresAtMs).isEqualTo(parseInstantMs("2026-10-27T12:00:00Z"))
        assertThat(grant.recheckAfterSeconds).isEqualTo(3_600L)
        assertThat(grant.title).isEqualTo("Friday build")
        assertThat(grant.channelName).isEqualTo("Raghu Builds")
        assertThat(grant.durationMs).isEqualTo(725_000L)
        assertThat(grant.posterUrl).isEqualTo("https://api.test/v1/media/c1/serve")
        assertThat(grant.video).isEqualTo(
            OfflineGrantStream("https://api.test/v1/media/m1/serve/720p", "video/mp4", 123L),
        )
        assertThat(grant.captions)
            .containsExactly(OfflineGrantCaption("en", "English", "https://api.test/v1/subtitles/m1/track/en.vtt"))
        assertThat(grant.sound).isEqualTo(
            OfflineGrantSound(
                stream = OfflineGrantStream("https://api.test/v1/audio/s1/serve", "audio/mp4", 77L),
                startMs = 1_500L,
                originalVolume = 0.2,
                overlayVolume = 0.8,
            ),
        )
    }

    @Test
    fun `absent, null, empty and zero all read as not said`() {
        val bare = grantOf("""{"post_id":"p1",$media}""")!!
        val empties = grantOf(
            """{"post_id":"p1","content_type":"","expires_at":"","recheck_after_seconds":0,"title":"",
               "channel_name":"","duration_ms":0,"poster_path":"",$media,"captions":null,"sound":null}""",
        )!!

        for (grant in listOf(bare, empties)) {
            assertThat(grant.kind).isNull()
            assertThat(grant.expiresAtMs).isEqualTo(now + DEFAULT_LIFETIME_MS)
            assertThat(grant.recheckAfterSeconds).isEqualTo(DEFAULT_RECHECK_SECONDS)
            assertThat(grant.title).isEmpty()
            assertThat(grant.posterUrl).isNull()
            assertThat(grant.captions).isEmpty()
            assertThat(grant.sound).isNull()
        }
        assertThat(empties).isEqualTo(bare)
    }

    /** A missing expiry must never mean "keep forever": the contract's thirty days stand in. */
    @Test
    fun `a missing or unreadable expiry is thirty days from now`() {
        assertThat(grantOf("""{"post_id":"p1","expires_at":"soon",$media}""")!!.expiresAtMs)
            .isEqualTo(now + 30L * 24 * 60 * 60 * 1000)
    }

    @Test
    fun `a grant with no post id or no media path grants nothing`() {
        assertThat(grantOf("""{"post_id":"",$media}""")).isNull()
        assertThat(grantOf("""{"post_id":"p1"}""")).isNull()
        assertThat(grantOf("""{"post_id":"p1","media":null}""")).isNull()
        // A list row: the card without the path.
        assertThat(grantOf("""{"post_id":"p1","media":{"media_id":"m1","variant":"720p","size_bytes":5}}""")).isNull()
    }

    @Test
    fun `a size the server did not send is unknown, not zero bytes expected`() {
        val grant = grantOf("""{"post_id":"p1","media":{"path":"/v1/media/m1/serve/480p"}}""")!!

        assertThat(grant.video.sizeBytes).isEqualTo(0L)
        assertThat(grant.video.mime).isEmpty()
    }

    /** `original_volume: 0` is the creator's mute, a real value; an absent level is full. */
    @Test
    fun `a volume of zero stays zero and an absent volume is full`() {
        val muted = grantOf(
            """{"post_id":"p1",$media,"sound":{"path":"/v1/audio/s1/serve","original_volume":0,"overlay_volume":1}}""",
        )!!.sound!!
        val unsaid = grantOf("""{"post_id":"p1",$media,"sound":{"path":"/v1/audio/s1/serve"}}""")!!.sound!!

        assertThat(muted.originalVolume).isEqualTo(0.0)
        assertThat(muted.overlayVolume).isEqualTo(1.0)
        assertThat(unsaid.originalVolume).isEqualTo(1.0)
        assertThat(unsaid.overlayVolume).isEqualTo(1.0)
        assertThat(unsaid.startMs).isEqualTo(0L)
        assertThat(unsaid.stream.sizeBytes).isEqualTo(0L)
    }

    @Test
    fun `a sound with no path is no sound`() {
        assertThat(grantOf("""{"post_id":"p1",$media,"sound":{"path":"","mime":"audio/mp4"}}""")!!.sound).isNull()
    }

    @Test
    fun `a caption without a language or a path is dropped, and one without a label is named by its language`() {
        val grant = grantOf(
            """{"post_id":"p1",$media,"captions":[
                 {"lang":"en","label":"","path":"/v1/subtitles/m1/track/en.vtt"},
                 {"lang":"","label":"Nameless","path":"/v1/subtitles/m1/track/x.vtt"},
                 {"lang":"hi","label":"Hindi","path":""},
                 {"lang":"en","label":"Again","path":"/v1/subtitles/m1/track/en2.vtt"}]}""",
        )!!

        assertThat(grant.captions)
            .containsExactly(OfflineGrantCaption("en", "en", "https://api.test/v1/subtitles/m1/track/en.vtt"))
    }

    @Test
    fun `the content type decides the kind`() {
        assertThat(offlineKindOf("flick")).isEqualTo(OfflineKind.REEL)
        assertThat(offlineKindOf("reel")).isEqualTo(OfflineKind.REEL)
        assertThat(offlineKindOf("long_video")).isEqualTo(OfflineKind.VIDEO)
        assertThat(offlineKindOf("video")).isEqualTo(OfflineKind.VIDEO)
        assertThat(offlineKindOf("")).isNull()
        assertThat(offlineKindOf("post")).isNull()
    }

    // ── The check ───────────────────────────────────────────────────────

    @Test
    fun `the check's answers are read by post`() {
        val answers = answersOf(
            """[{"post_id":"a","valid":true,"expires_at":"2026-10-27T12:00:00Z"},
                {"post_id":"b","valid":false,"reason":"not_allowed"},
                {"post_id":"c","valid":false},
                {"post_id":"d","valid":true},
                {"post_id":"","valid":false,"reason":"deleted"}]""",
        )

        assertThat(answers).containsExactlyEntriesIn(
            mapOf(
                "a" to OfflineCheckAnswer.Valid(parseInstantMs("2026-10-27T12:00:00Z")),
                "b" to OfflineCheckAnswer.Invalid("not_allowed"),
                // No reason given is still "not valid".
                "c" to OfflineCheckAnswer.Invalid("unknown"),
                "d" to OfflineCheckAnswer.Valid(null),
            ),
        )
    }

    /** `renewable` arrives on valid rows from 2 Oct 2026; an older server sends none. */
    @Test
    fun `a valid answer is renewable unless the server says it is not`() {
        val answers = answersOf(
            """[{"post_id":"said-yes","valid":true,"renewable":true},
                {"post_id":"said-no","valid":true,"renewable":false},
                {"post_id":"not-said","valid":true},
                {"post_id":"null","valid":true,"renewable":null}]""",
        )

        assertThat(answers.mapValues { (it.value as OfflineCheckAnswer.Valid).renewable }).containsExactlyEntriesIn(
            mapOf("said-yes" to true, "said-no" to false, "not-said" to true, "null" to true),
        )
    }

    @Test
    fun `an empty or null check answers for nobody`() {
        assertThat(answersOf("[]")).isEmpty()
        assertThat(answersOf("null")).isEmpty()
    }

    @Test
    fun `timestamps with an offset read the same instant as zulu`() {
        assertThat(parseInstantMs("2026-10-27T17:30:00+05:30")).isEqualTo(parseInstantMs("2026-10-27T12:00:00Z"))
        assertThat(parseInstantMs("")).isNull()
        assertThat(parseInstantMs("27 Oct")).isNull()
    }
}
