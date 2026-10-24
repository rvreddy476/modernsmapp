package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonElement
import org.junit.Test

/**
 * Trending sounds (2026-10-24): reading the ranking, the words the screens
 * say, the shelf and paging, and which badge a sound page wears.
 *
 * What this protects: the read being TOLERANT — the route is built beside
 * this client, so the old `{tracks}` shape, a row that is not an object, a
 * number sent as a string and a missing field must drop a row at most,
 * never the list; a sound with no id or one not ready never being offered;
 * the contract's Rising rule standing in only when the server said
 * nothing; and the badge being "#N trending" then "Rising", never one
 * invented from a rank of 0.
 */
class TrendingSoundsTest {

    private fun parse(body: String): JsonElement = Json.parseToJsonElement(body)

    /** The rows as one answer. */
    private fun decode(vararg rows: String) = decodeTrendingSounds(parse(rows.joinToString(",", "[", "]")))

    private fun row(
        id: String,
        rank: String = "1",
        uses: String = "42",
        previous: String = "10",
        rising: String = "true",
        extra: String = "",
    ) =
        """{"rank":$rank,"uses_7d":$uses,"uses_prev_7d":$previous,"rising":$rising,
        |"sound":{"id":"$id","title":"Sound $id","artist":"Asha","duration_ms":28400,"status":"ready",
        |"usage_count":7,"source_post_id":"p-$id","creator_user_id":"u-$id"$extra}}
        """.trimMargin()

    // ── decode ──────────────────────────────────────────────────────────

    @Test
    fun `a row decodes into its rank, sound, uses and rising`() {
        val sound = decodeTrendingSounds(parse("[${row("s1")}]")).single()

        assertThat(sound.rank).isEqualTo(1)
        assertThat(sound.uses7d).isEqualTo(42L)
        assertThat(sound.usesPrev7d).isEqualTo(10L)
        assertThat(sound.rising).isTrue()
        assertThat(sound.coverUrl).isNull()
        assertThat(sound.sound.id).isEqualTo("s1")
        assertThat(sound.sound.title).isEqualTo("Sound s1")
        assertThat(sound.sound.artist).isEqualTo("Asha")
        assertThat(sound.sound.durationMs).isEqualTo(28_400L)
        assertThat(sound.sound.useCount).isEqualTo(7)
        assertThat(sound.sound.sourcePostId).isEqualTo("p-s1")
        assertThat(sound.sound.creatorUserId).isEqualTo("u-s1")
        assertThat(sound.sound.startMs).isEqualTo(0L)
    }

    @Test
    fun `the order is the server's, and a sound twice keeps its first row`() {
        val sounds = decode(row("b", rank = "1"), row("a", rank = "2"), row("b", rank = "3"))

        assertThat(sounds.map { it.sound.id }).containsExactly("b", "a").inOrder()
        assertThat(sounds.first().rank).isEqualTo(1)
    }

    @Test
    fun `anything but an array is no rows, the route's old tracks shape included`() {
        assertThat(decodeTrendingSounds(null)).isEmpty()
        assertThat(decodeTrendingSounds(parse("""{"tracks":[{"id":"s1","title":"x"}]}"""))).isEmpty()
        assertThat(decodeTrendingSounds(parse("null"))).isEmpty()
        assertThat(decodeTrendingSounds(parse("\"oops\""))).isEmpty()
    }

    @Test
    fun `a row that cannot be read is dropped, and only that row`() {
        val body = "[1, \"x\", null, {\"rank\":2}, {\"rank\":3,\"sound\":\"s\"}, ${row("ok")}]"

        assertThat(decodeTrendingSounds(parse(body)).map { it.sound.id }).containsExactly("ok")
    }

    @Test
    fun `a sound with no id, or one not ready to play, is never offered`() {
        val body = "[${row(" ")}, " +
            "{\"rank\":2,\"sound\":{\"id\":\"p\",\"status\":\"processing\"}}, " +
            "{\"rank\":3,\"sound\":{\"id\":\"r\",\"status\":\"ACTIVE\"}}, " +
            "{\"rank\":4,\"sound\":{\"id\":\"blank\"}}]"

        assertThat(decodeTrendingSounds(parse(body)).map { it.sound.id }).containsExactly("r", "blank").inOrder()
    }

    @Test
    fun `numbers sent as strings or fractions are read, and nonsense reads as nothing`() {
        val sound = decodeTrendingSounds(
            parse(
                """[{"rank":"4","uses_7d":"12","uses_prev_7d":3.9,"rising":"false",
                  |"sound":{"id":"s","duration_ms":"1000"}}]
                """.trimMargin(),
            ),
        ).single()

        assertThat(sound.rank).isEqualTo(4)
        assertThat(sound.uses7d).isEqualTo(12L)
        assertThat(sound.usesPrev7d).isEqualTo(3L)
        assertThat(sound.rising).isFalse()
        assertThat(sound.sound.durationMs).isEqualTo(1_000L)

        val nonsense = decodeTrendingSounds(
            parse("""[{"rank":"first","uses_7d":-5,"uses_prev_7d":{"a":1},"rising":false,"sound":{"id":"s"}}]"""),
        ).single()
        assertThat(nonsense.rank).isNull()
        assertThat(nonsense.uses7d).isEqualTo(0L)
        assertThat(nonsense.usesPrev7d).isEqualTo(0L)
    }

    @Test
    fun `a rank of zero or below is no rank`() {
        val sounds = decode(row("a", rank = "0"), row("b", rank = "-2"), row("c", rank = "null"))

        assertThat(sounds.map { it.rank }).containsExactly(null, null, null)
    }

    @Test
    fun `go zero values fall through - an empty title reads Original sound, an empty source falls back to the reel`() {
        val sound = decodeTrendingSounds(
            parse(
                """[{"rank":1,"sound":{"id":"s","title":"","source_post_id":"",
                  |"source_reel_id":"r1","creator_user_id":""}}]
                """.trimMargin(),
            ),
        ).single().sound

        assertThat(sound.title).isEqualTo("Original sound")
        assertThat(sound.sourcePostId).isEqualTo("r1")
        assertThat(sound.creatorUserId).isNull()
    }

    @Test
    fun `use_count, a post read's spelling, is read before usage_count`() {
        val sound = decodeTrendingSounds(parse("""[{"rank":1,"sound":{"id":"s","use_count":9,"usage_count":2}}]"""))
            .single()

        assertThat(sound.sound.useCount).isEqualTo(9)
    }

    @Test
    fun `a cover is kept when the server sends one`() {
        val sound = decode(row("s", extra = ",\"cover_url\":\"https://img/c.jpg\"")).single()

        assertThat(sound.coverUrl).isEqualTo("https://img/c.jpg")
    }

    @Test
    fun `a cover resolves to https or the API origin, and nothing else`() {
        val base = "https://api.test/"
        assertThat(resolveSoundCover("/v1/media/m/serve/thumb_150", base))
            .isEqualTo("https://api.test/v1/media/m/serve/thumb_150")
        assertThat(resolveSoundCover(" https://cdn.test/a.jpg ", base)).isEqualTo("https://cdn.test/a.jpg")
        assertThat(resolveSoundCover("http://cdn.test/a.jpg", base)).isNull()
        assertThat(resolveSoundCover("//evil.test/a.jpg", base)).isNull()
        assertThat(resolveSoundCover("javascript:alert(1)", base)).isNull()
        assertThat(resolveSoundCover("", base)).isNull()
        assertThat(resolveSoundCover(null, base)).isNull()
        assertThat(resolveSoundCover("/v1/media/m", "")).isNull()
    }

    @Test
    fun `rising is the server's word, and the contract's rule only when it said nothing`() {
        // The server says not rising, though the numbers would say so: its word stands.
        assertThat(decode(row("a", uses = "9", previous = "0", rising = "false")).single().rising).isFalse()

        fun derived(uses: Long, previous: Long) = decodeTrendingSounds(
            parse("""[{"rank":1,"uses_7d":$uses,"uses_prev_7d":$previous,"sound":{"id":"s"}}]"""),
        ).single().rising
        assertThat(derived(3, 0)).isTrue()
        assertThat(derived(2, 0)).isFalse()
        assertThat(derived(10, 5)).isTrue()
        assertThat(derived(9, 5)).isFalse()
    }

    @Test
    fun `the contract's Rising rule at its edges`() {
        assertThat(isRising(3, 0)).isTrue()
        assertThat(isRising(2, 0)).isFalse()
        assertThat(isRising(6, 3)).isTrue()
        assertThat(isRising(5, 3)).isFalse()
        assertThat(isRising(3, 1)).isTrue()
    }

    @Test
    fun `a page keeps the cursor, and an empty cursor or time is the end, not a page`() {
        val page = trendingSoundsPage(parse("[${row("a")}]"), "c2", "2026-10-24T10:15:00Z")
        assertThat(page.nextCursor).isEqualTo("c2")
        assertThat(page.updatedAt).isEqualTo("2026-10-24T10:15:00Z")

        val last = trendingSoundsPage(parse("[]"), " ", "")
        assertThat(last.nextCursor).isNull()
        assertThat(last.updatedAt).isNull()
    }

    // ── a sound's place on the list, and its badges ─────────────────────

    @Test
    fun `the trending block of a sound read`() {
        assertThat(trendingMarkOfSound(parse("""{"id":"s","trending":{"rank":3,"rising":false}}""")))
            .isEqualTo(TrendingMark(rank = 3, rising = false))
        assertThat(trendingMarkOfSound(parse("""{"id":"s","trending":{"rank":"7","rising":"true"}}""")))
            .isEqualTo(TrendingMark(rank = 7, rising = true))
    }

    @Test
    fun `a sound off the list has no mark, however the server says it`() {
        assertThat(trendingMarkOfSound(parse("""{"id":"s"}"""))).isNull()
        assertThat(trendingMarkOfSound(parse("""{"id":"s","trending":null}"""))).isNull()
        assertThat(trendingMarkOfSound(parse("""{"id":"s","trending":true}"""))).isNull()
        assertThat(trendingMarkOfSound(parse("""{"id":"s","trending":{}}"""))).isNull()
        assertThat(trendingMarkOfSound(parse("""{"id":"s","trending":{"rank":0,"rising":false}}"""))).isNull()
        assertThat(trendingMarkOfSound(parse("[]"))).isNull()
        assertThat(trendingMarkOfSound(null)).isNull()
    }

    @Test
    fun `rising with no rank is still a mark`() {
        assertThat(trendingMark(parse("""{"rising":true}"""))).isEqualTo(TrendingMark(rank = null, rising = true))
    }

    @Test
    fun `the badges - the rank first, then Rising, nothing off the list`() {
        assertThat(trendingBadges(TrendingMark(rank = 3, rising = false))).containsExactly("#3 trending")
        assertThat(trendingBadges(TrendingMark(rank = 12, rising = true)))
            .containsExactly("#12 trending", "Rising").inOrder()
        assertThat(trendingBadges(TrendingMark(rank = null, rising = true))).containsExactly("Rising")
        assertThat(trendingBadges(TrendingMark(rank = null, rising = false))).isEmpty()
        assertThat(trendingBadges(null)).isEmpty()
    }

    // ── words ───────────────────────────────────────────────────────────

    @Test
    fun `reels this week, singular, plural and grouped, nothing at zero`() {
        assertThat(reelsThisWeek(42)).isEqualTo("42 reels this week")
        assertThat(reelsThisWeek(1)).isEqualTo("1 reel this week")
        assertThat(reelsThisWeek(1_200)).isEqualTo("1,200 reels this week")
        assertThat(reelsThisWeek(0)).isEmpty()
        assertThat(reelsThisWeek(-1)).isEmpty()
    }

    @Test
    fun `the rank label`() {
        assertThat(trendingRankLabel(1)).isEqualTo("#1")
        assertThat(trendingRankLabel(null)).isEmpty()
    }

    // ── shelf, paging, Use ──────────────────────────────────────────────

    private fun ranked(count: Int, prefix: String = "s") =
        decodeTrendingSounds(parse((1..count).joinToString(",", "[", "]") { row("$prefix$it", rank = "$it") }))

    @Test
    fun `the shelf is the top twenty of All`() {
        val all = ranked(25)

        assertThat(trendingShelf(all).map { it.rank }).containsExactlyElementsIn(1..TRENDING_SHELF_SIZE).inOrder()
        assertThat(trendingShelf(ranked(3))).hasSize(3)
    }

    @Test
    fun `a later page follows on, nothing twice, the earlier row kept`() {
        val first = ranked(2)
        val next = decodeTrendingSounds(parse("[${row("s2", rank = "9")},${row("s3", rank = "3")}]"))

        val merged = appendTrending(first, next)

        assertThat(merged.map { it.sound.id }).containsExactly("s1", "s2", "s3").inOrder()
        assertThat(merged[1].rank).isEqualTo(2)
    }

    @Test
    fun `Use leaves the audio_tracks id and what the sound is called`() {
        val chosen = ranked(1).single().toChosenSound()

        assertThat(chosen.id).isEqualTo("s1")
        assertThat(chosen.title).isEqualTo("Sound s1")
        assertThat(chosen.artist).isEqualTo("Asha")
        assertThat(chosen.durationMs).isEqualTo(28_400L)
    }
}
