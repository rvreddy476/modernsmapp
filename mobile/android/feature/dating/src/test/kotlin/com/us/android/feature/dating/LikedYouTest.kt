package com.us.android.feature.dating

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.home.IncomingSparkUi
import com.us.android.feature.dating.home.LikedYouCopy
import com.us.android.feature.dating.home.LikedYouState
import com.us.android.feature.dating.home.LikedYouTile
import com.us.android.feature.dating.home.LikedYouViewModel
import com.us.android.feature.dating.home.ListState
import com.us.android.feature.dating.home.SparksViewModel
import com.us.android.feature.dating.network.LikedYouDto
import com.us.android.feature.dating.network.SparkCreatedDto
import com.us.android.feature.dating.network.SparkDto
import com.us.android.feature.dating.photos.PhotoRules
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.ReportReason
import com.us.android.feature.dating.safety.SafetyActions
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.builtins.ListSerializer
import org.junit.Rule
import org.junit.Test

/**
 * Mechanic M4, "who liked you": the Sparks tab's grid. Locked or unlocked is
 * the server's `unlocked` flag; a locked tile never carries a person; a 403
 * LIKED_YOU_LOCKED from accept locks the grid on the spot.
 */
class LikedYouTest {

    @get:Rule
    val main = MainDispatcherRule()

    private val api = FakeDatingApi()
    private val session = DatingSession().also { it.setProfile(profile()) }
    private val repository = api.repository()
    private val safety = SafetyActions(repository, session)
    private val urls = photoUrls()

    private fun grid() = LikedYouViewModel(repository, session, safety, urls)

    private fun loaded(viewModel: LikedYouViewModel) = viewModel.state.value as LikedYouState.Loaded

    private fun open(viewModel: LikedYouViewModel, sparkId: String) =
        loaded(viewModel).tiles.first { it.sparkId == sparkId } as LikedYouTile.Open

    private fun serve(dto: LikedYouDto) {
        api.likedYouResponse = { _, _ -> ok(dto) }
    }

    /** A golden fixture, with distinct spark ids (the goldens redact every id to the same placeholder). */
    private fun fixtureGrid(name: String): LikedYouDto {
        val dto = fixture(name, LikedYouDto.serializer())
        return dto.copy(items = dto.items.mapIndexed { i, item -> item.copy(sparkId = "s-$i") })
    }

    // ── Locked ──────────────────────────────────────────────────────────────

    @Test
    fun `the locked fixture is a locked grid with the total and Super Spark first`() = runTest {
        serve(fixtureGrid("liked_you_get_200_locked.json"))

        val state = loaded(grid())

        assertThat(state.unlocked).isFalse()
        assertThat(state.total).isEqualTo(2)
        assertThat(LikedYouCopy.header(state.total)).isEqualTo("2 people sparked you")
        assertThat(state.tiles.map { it.superSpark }).containsExactly(true, false).inOrder()
        assertThat(state.tiles).containsExactly(
            LikedYouTile.Locked("s-0", superSpark = true, photoUrl = "https://api.test/v1/dating/liked-you/<uuid>/photo"),
            LikedYouTile.Locked("s-1", superSpark = false, photoUrl = "https://api.test/v1/dating/liked-you/<uuid>/photo"),
        ).inOrder()
    }

    @Test
    fun `a locked response never shows a person, even when the server sent one`() = runTest {
        // A server bug: a person, a note and a FULL photo route on a locked item.
        val leaked = likedYouOpen("s-1", "u-1", superSpark = true).copy(photoUrl = "/v1/dating/photos/photo-u-1/full")
        serve(LikedYouDto(total = 1, unlocked = false, items = listOf(leaked)))

        val tile = loaded(grid()).tiles.single()

        assertThat(tile).isInstanceOf(LikedYouTile.Locked::class.java)
        tile as LikedYouTile.Locked
        assertThat(tile.superSpark).isTrue()
        // Not the full route it was handed: the blurred route for this spark.
        assertThat(tile.photoUrl).isEqualTo("https://api.test/v1/dating/liked-you/s-1/photo")
        assertThat(tile.toString()).doesNotContain("Person u-1")
        assertThat(tile.toString()).doesNotContain("u-1/")
    }

    @Test
    fun `a locked tile opens the upsell`() = runTest {
        serve(fixtureGrid("liked_you_get_200_locked.json"))
        val grid = grid()
        assertThat(grid.upsell.value).isFalse()

        grid.showUpsell()
        assertThat(grid.upsell.value).isTrue()
        grid.dismissUpsell()
        assertThat(grid.upsell.value).isFalse()
        // Looking is not deciding: nothing was sent.
        assertThat(api.accepts).isEmpty()
        assertThat(api.declines).isEmpty()
    }

    @Test
    fun `only the blurred liked-you route is accepted for a locked card`() {
        assertThat(PhotoRules.likedYouPath("/v1/dating/liked-you/abc/photo")).isEqualTo("/v1/dating/liked-you/abc/photo")
        assertThat(PhotoRules.likedYouPath("/v1/dating/photos/abc/full")).isNull()
        assertThat(PhotoRules.likedYouPath("/v1/dating/photos/abc/blurred")).isNull()
        assertThat(PhotoRules.likedYouPath("/v1/dating/liked-you/abc/photo?full=1")).isNull()
        assertThat(PhotoRules.likedYouPath("")).isNull()
        assertThat(PhotoRules.likedYouPathFor("abc")).isEqualTo("/v1/dating/liked-you/abc/photo")
        assertThat(PhotoRules.likedYouPathFor("a/b")).isNull()
        assertThat(PhotoRules.likedYouPathFor(" ")).isNull()
    }

    // ── Unlocked ────────────────────────────────────────────────────────────

    @Test
    fun `the unlocked fixture shows each person with their photo, name, age and note`() = runTest {
        serve(fixtureGrid("liked_you_get_200_unlocked.json"))

        val state = loaded(grid())

        assertThat(state.unlocked).isTrue()
        assertThat(state.total).isEqualTo(2)
        val sparks = state.tiles.map { (it as LikedYouTile.Open).spark }
        assertThat(sparks.map { it.fromUserId }).containsExactly("<super_sender>", "<sender>").inOrder()
        assertThat(sparks.map { it.superSpark }).containsExactly(true, false).inOrder()
        sparks.forEach { spark ->
            assertThat(spark.name).isEqualTo("Asha")
            assertThat(spark.age).isEqualTo(30)
            assertThat(spark.note).isEqualTo("Loved your answer")
            assertThat(spark.photoUrl).isEqualTo("https://api.test/v1/dating/photos/<uuid>/full")
        }
    }

    @Test
    fun `the gate off is an unlocked grid, as before`() = runTest {
        serve(LikedYouDto(total = 1, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"))))

        val state = loaded(grid())

        assertThat(state.unlocked).isTrue()
        assertThat((state.tiles.single() as LikedYouTile.Open).spark.name).isEqualTo("Person u-1")
        assertThat(LikedYouCopy.header(state.total)).isEqualTo("1 person sparked you")
    }

    @Test
    fun `a blurred photo_state stays blurred on an unlocked tile`() = runTest {
        serve(LikedYouDto(total = 1, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1", card = person("u-1", photoState = "blurred")))))

        assertThat(open(grid(), "s-1").spark.photoUrl).isEqualTo("https://api.test/v1/dating/photos/photo-u-1/blurred")
    }

    @Test
    fun `Super Sparks come first, and the server's order is kept within each group`() = runTest {
        serve(
            LikedYouDto(
                total = 4,
                unlocked = true,
                items = listOf(
                    likedYouOpen("a", "u-a"),
                    likedYouOpen("b", "u-b", superSpark = true),
                    likedYouOpen("c", "u-c"),
                    likedYouOpen("d", "u-d", superSpark = true),
                ),
            ),
        )

        assertThat(loaded(grid()).tiles.map { it.sparkId }).containsExactly("b", "d", "a", "c").inOrder()
    }

    @Test
    fun `an unlocked item without a person card is left out`() = runTest {
        serve(LikedYouDto(total = 2, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"), likedYouOpen("s-2", "u-2", card = null))))

        val state = loaded(grid())

        assertThat(state.tiles.map { it.sparkId }).containsExactly("s-1")
        // The count is the server's.
        assertThat(state.total).isEqualTo(2)
    }

    @Test
    fun `an empty grid has no header`() = runTest {
        val state = loaded(grid())

        assertThat(state.tiles).isEmpty()
        assertThat(state.total).isEqualTo(0)
        assertThat(LikedYouCopy.header(state.total)).isNull()
    }

    // ── Accept and decline (unlocked) ───────────────────────────────────────

    @Test
    fun `spark back accepts by spark id and celebrates the match`() = runTest {
        serve(LikedYouDto(total = 2, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"), likedYouOpen("s-2", "u-2"))))
        api.acceptResponse = { ok(SparkCreatedDto(matchId = "match-1", matched = true)) }
        api.matches = listOf(match("match-1", "u-1", person("u-1", name = "Asha")))
        val grid = grid()

        assertThat(grid.accept(open(grid, "s-1"))).isTrue()

        assertThat(api.accepts).containsExactly("s-1")
        assertThat(grid.celebration.value?.matchId).isEqualTo("match-1")
        assertThat(grid.celebration.value?.name).isEqualTo("Asha")
        val state = loaded(grid)
        assertThat(state.tiles.map { it.sparkId }).containsExactly("s-2")
        assertThat(state.total).isEqualTo(1)
        assertThat(grid.busy.value).isNull()
    }

    @Test
    fun `spark back without a match yet says so`() = runTest {
        serve(LikedYouDto(total = 1, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"))))
        val grid = grid()

        grid.accept(open(grid, "s-1"))

        assertThat(grid.celebration.value).isNull()
        assertThat(grid.message.value?.text).isEqualTo("Spark sent back. Your match will appear soon.")
        assertThat(loaded(grid).tiles).isEmpty()
    }

    @Test
    fun `decline calls decline by spark id and takes the tile and one from the count`() = runTest {
        serve(LikedYouDto(total = 3, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"), likedYouOpen("s-2", "u-2"))))
        val grid = grid()

        grid.decline(open(grid, "s-1"))

        assertThat(api.declines).containsExactly("s-1")
        assertThat(api.accepts).isEmpty()
        val state = loaded(grid)
        assertThat(state.tiles.map { it.sparkId }).containsExactly("s-2")
        assertThat(state.total).isEqualTo(2)
    }

    @Test
    fun `a refused decline keeps the tile and says why`() = runTest {
        serve(LikedYouDto(total = 1, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"))))
        api.declineResponse = { refused(500, "INTERNAL") }
        val grid = grid()

        grid.decline(open(grid, "s-1"))

        assertThat(loaded(grid).tiles.map { it.sparkId }).containsExactly("s-1")
        assertThat(grid.message.value?.text).isEqualTo(DatingCopy.GENERIC)
    }

    @Test
    fun `accepting a spark that is gone drops it`() = runTest {
        serve(LikedYouDto(total = 2, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"), likedYouOpen("s-2", "u-2"))))
        api.acceptResponse = { refused(404, "NOT_FOUND") }
        val grid = grid()

        grid.accept(open(grid, "s-1"))

        assertThat(loaded(grid).tiles.map { it.sparkId }).containsExactly("s-2")
    }

    @Test
    fun `a reported person leaves the grid and the count`() = runTest {
        serve(LikedYouDto(total = 2, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"), likedYouOpen("s-2", "u-2"))))
        val grid = grid()

        grid.report(ReportDraft(targetId = "u-1", reason = ReportReason.HARASSMENT, sparkIds = listOf("s-1")))

        val state = loaded(grid)
        assertThat(state.tiles.map { it.sparkId }).containsExactly("s-2")
        assertThat(state.total).isEqualTo(1)
    }

    // ── 403 LIKED_YOU_LOCKED ────────────────────────────────────────────────

    @Test
    fun `LIKED_YOU_LOCKED from accept locks the grid at once and shows the upsell`() = runTest {
        serve(LikedYouDto(total = 2, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1", superSpark = true), likedYouOpen("s-2", "u-2"))))
        api.acceptResponse = { refusedWithFixture(403, "spark_accept_403_liked_you_locked.json") }
        val grid = grid()
        val tile = open(grid, "s-1")
        // The reload fails, so what is on screen is the app's own lock.
        api.likedYouResponse = { _, _ -> offline() }

        grid.accept(tile)

        val state = loaded(grid)
        assertThat(state.unlocked).isFalse()
        assertThat(state.total).isEqualTo(2)
        assertThat(state.tiles).containsExactly(
            LikedYouTile.Locked("s-1", superSpark = true, photoUrl = "https://api.test/v1/dating/liked-you/s-1/photo"),
            LikedYouTile.Locked("s-2", superSpark = false, photoUrl = "https://api.test/v1/dating/liked-you/s-2/photo"),
        ).inOrder()
        assertThat(grid.upsell.value).isTrue()
        assertThat(grid.celebration.value).isNull()
        // The upsell speaks; no error line on top of it.
        assertThat(grid.message.value).isNull()
        // And the server's locked view was asked for.
        assertThat(api.likedYouReads).hasSize(2)
    }

    @Test
    fun `after LIKED_YOU_LOCKED the server's locked view replaces the grid`() = runTest {
        serve(LikedYouDto(total = 1, unlocked = true, items = listOf(likedYouOpen("s-1", "u-1"))))
        api.acceptResponse = { refused(403, "LIKED_YOU_LOCKED") }
        val grid = grid()
        val tile = open(grid, "s-1")
        serve(LikedYouDto(total = 3, unlocked = false, items = listOf(likedYouLocked("s-1"), likedYouLocked("s-9", superSpark = true))))

        grid.accept(tile)

        val state = loaded(grid)
        assertThat(state.unlocked).isFalse()
        assertThat(state.total).isEqualTo(3)
        assertThat(state.tiles.map { it.sparkId }).containsExactly("s-9", "s-1").inOrder()
        assertThat(state.tiles.all { it is LikedYouTile.Locked }).isTrue()
    }

    // ── Paging ──────────────────────────────────────────────────────────────

    @Test
    fun `the next page is read from the offset of what is held, until the total is reached`() = runTest {
        val first = (0 until LikedYouViewModel.PAGE_SIZE).map { likedYouLocked("s-$it") }
        api.likedYouResponse = { _, offset ->
            if (offset == 0) {
                ok(LikedYouDto(total = 52, unlocked = false, items = first))
            } else {
                ok(LikedYouDto(total = 52, unlocked = false, items = listOf(likedYouLocked("s-50"), likedYouLocked("s-51"))))
            }
        }
        val grid = grid()
        assertThat(loaded(grid).canLoadMore).isTrue()

        grid.loadMore()

        assertThat(api.likedYouReads).containsExactly(50 to 0, 50 to 50).inOrder()
        val state = loaded(grid)
        assertThat(state.tiles).hasSize(52)
        assertThat(state.canLoadMore).isFalse()
        // Nothing more to ask for.
        grid.loadMore()
        assertThat(api.likedYouReads).hasSize(2)
    }

    @Test
    fun `a page that comes back locked after an unlocked one reloads the whole grid locked`() = runTest {
        var locked = false
        api.likedYouResponse = { _, offset ->
            when {
                locked -> ok(LikedYouDto(total = 51, unlocked = false, items = (0 until 51).map { likedYouLocked("s-$it") }))
                offset == 0 -> ok(LikedYouDto(total = 51, unlocked = true, items = (0 until 50).map { likedYouOpen("s-$it", "u-$it") }))
                else -> {
                    locked = true
                    ok(LikedYouDto(total = 51, unlocked = false, items = listOf(likedYouLocked("s-50"))))
                }
            }
        }
        val grid = grid()
        assertThat(loaded(grid).unlocked).isTrue()

        grid.loadMore()

        assertThat(api.likedYouReads).containsExactly(50 to 0, 50 to 50, 50 to 0).inOrder()
        val state = loaded(grid)
        assertThat(state.unlocked).isFalse()
        assertThat(state.tiles.all { it is LikedYouTile.Locked }).isTrue()
    }

    // ── The incoming list stays tolerant of the locked shape ────────────────

    @Test
    fun `locked incoming sparks are left out of the incoming list rather than crashing it`() = runTest {
        val locked = testJson.decodeFromString(
            ApiEnvelope.serializer(ListSerializer(SparkDto.serializer())),
            fixtureText("sparks_incoming_get_200_locked.json"),
        ).data.orEmpty()
        api.incoming = locked + spark("s-open", "u-open")

        val sparks = SparksViewModel(repository, session, safety, urls)

        val items = (sparks.state.value as ListState.Items<IncomingSparkUi>).items
        assertThat(items.map { it.sparkId }).containsExactly("s-open")
    }
}
