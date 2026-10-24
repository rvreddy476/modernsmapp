package com.us.android.core.feed.data.saved

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.network.di.NetworkModule
import kotlinx.serialization.encodeToString
import org.junit.Test

/**
 * The client's half of saved collections: the tabs from the list, which
 * call "Save to collection" makes, the name rules (service/saved_collections.go
 * :59-79 plus the store's unique and 50 rules), what a move or a delete does
 * to the counts and to the collection on screen, and the refusal sentences.
 */
class SavedCollectionsRulesTest {

    private val json = NetworkModule.provideJson()

    private val all = SavedCollection(null, "All Saved", 5)
    private val recipes = SavedCollection("c-1", "Recipes", 2)
    private val trips = SavedCollection("c-2", "Trips", 0)
    private val tabs = listOf(all, recipes, trips)

    // ── The list ────────────────────────────────────────────────────────

    @Test
    fun `the default comes first exactly once, even when the server sends it late or twice`() {
        val rows = listOf(
            SavedCollectionDto(id = "c-1", name = "Recipes", count = 2),
            SavedCollectionDto(id = null, name = "All Saved", count = 5, isDefault = true),
            SavedCollectionDto(id = null, name = "All Saved", count = 9, isDefault = true),
        )
        assertThat(rows.toCollections()).containsExactly(all, recipes).inOrder()
    }

    @Test
    fun `a list without the default still opens on All Saved, and junk rows are dropped`() {
        val rows = listOf(
            SavedCollectionDto(id = "c-1", name = "Recipes", count = 2),
            SavedCollectionDto(id = "c-1", name = "Recipes again", count = 7),
            SavedCollectionDto(id = "c-3", name = " ", count = 1),
            SavedCollectionDto(id = "c-2", name = "Trips", count = -4),
        )
        assertThat(rows.toCollections()).containsExactly(EMPTY_DEFAULT_COLLECTION, recipes, trips).inOrder()
    }

    // ── Save to collection ──────────────────────────────────────────────

    @Test
    fun `save to collection posts the id, posts default for a fresh save, and moves a saved post to All Saved`() {
        assertThat(saveToCollectionCall("c-1", alreadySaved = false))
            .isEqualTo(SaveToCollectionCall.SaveInto(CollectionRefRequest("c-1")))
        // The upsert moves an already-saved post into a named collection.
        assertThat(saveToCollectionCall("c-1", alreadySaved = true))
            .isEqualTo(SaveToCollectionCall.SaveInto(CollectionRefRequest("c-1")))
        assertThat(saveToCollectionCall(null, alreadySaved = false))
            .isEqualTo(SaveToCollectionCall.SaveInto(CollectionRefRequest("default")))
        assertThat(saveToCollectionCall(null, alreadySaved = true))
            .isEqualTo(SaveToCollectionCall.MoveTo(CollectionRefRequest("default")))
    }

    @Test
    fun `the collection body always carries the key`() {
        assertThat(json.encodeToString(CollectionRefRequest(collectionRef(null)))).isEqualTo("""{"collection_id":"default"}""")
        assertThat(json.encodeToString(CollectionRefRequest(collectionRef("c-1")))).isEqualTo("""{"collection_id":"c-1"}""")
        assertThat(json.encodeToString(CollectionNameRequest("Recipes"))).isEqualTo("""{"name":"Recipes"}""")
    }

    // ── Names ───────────────────────────────────────────────────────────

    private fun problem(raw: String, collections: List<SavedCollection> = tabs, renaming: String? = null) =
        (collectionNameCheck(raw, collections, renaming) as? CollectionNameCheck.Refused)?.problem

    @Test
    fun `a name is trimmed and its inner spaces folded, as the server stores it`() {
        assertThat(collectionNameCheck("  Summer \t  reads ", tabs)).isEqualTo(CollectionNameCheck.Ok("Summer reads"))
        assertThat(normalizeCollectionName(" a  b ")).isEqualTo("a b")
    }

    @Test
    fun `empty, too long, reserved and control characters are refused before sending`() {
        assertThat(problem("   ")).isEqualTo(CollectionNameProblem.EMPTY)
        assertThat(problem("x".repeat(MAX_COLLECTION_NAME_LENGTH))).isNull()
        assertThat(problem("x".repeat(MAX_COLLECTION_NAME_LENGTH + 1))).isEqualTo(CollectionNameProblem.TOO_LONG)
        // Characters, not UTF-16 units: 40 emoji is 40 characters.
        assertThat(problem("🍕".repeat(MAX_COLLECTION_NAME_LENGTH))).isNull()
        assertThat(problem("all SAVED")).isEqualTo(CollectionNameProblem.RESERVED)
        assertThat(problem("Default")).isEqualTo(CollectionNameProblem.RESERVED)
        assertThat(problem("bad\u0007name")).isEqualTo(CollectionNameProblem.INVALID)
        // A tab or a newline is space to the server, not a refused control character.
        assertThat(problem("tab\tname")).isNull()
    }

    @Test
    fun `a taken name is refused case-insensitively, except the collection's own on a rename`() {
        assertThat(problem("recipes")).isEqualTo(CollectionNameProblem.TAKEN)
        assertThat(problem("RECIPES", renaming = "c-1")).isNull()
        assertThat(problem("trips", renaming = "c-1")).isEqualTo(CollectionNameProblem.TAKEN)
    }

    @Test
    fun `the fifty-first collection is refused, a rename at fifty is not`() {
        val fifty = listOf(all) + (1..MAX_SAVED_COLLECTIONS).map { SavedCollection("c$it", "Box $it", 0) }
        val fortyNine = fifty.dropLast(1)

        assertThat(problem("New one", fifty)).isEqualTo(CollectionNameProblem.LIMIT)
        assertThat(problem("New one", fortyNine)).isNull()
        assertThat(problem("Renamed", fifty, renaming = "c1")).isNull()
        assertThat(canCreateCollection(fifty)).isFalse()
        assertThat(canCreateCollection(fortyNine)).isTrue()
    }

    // ── Moves, deletes, the collection on screen ────────────────────────

    @Test
    fun `a move takes one from where it was and adds one where it went`() {
        assertThat(tabs.afterMove(fromCollectionId = "c-1", toCollectionId = "c-2"))
            .containsExactly(all, recipes.copy(count = 1), trips.copy(count = 1)).inOrder()
        assertThat(tabs.afterMove(fromCollectionId = null, toCollectionId = "c-1"))
            .containsExactly(all.copy(count = 4), recipes.copy(count = 3), trips).inOrder()
        // Same collection: nothing moves. An empty source never goes negative.
        assertThat(tabs.afterMove("c-1", "c-1")).isEqualTo(tabs)
        assertThat(tabs.afterMove("c-2", null)).containsExactly(all.copy(count = 6), recipes, trips).inOrder()
    }

    @Test
    fun `a save adds one to its collection only`() {
        assertThat(tabs.afterSave("c-2")).containsExactly(all, recipes, trips.copy(count = 1)).inOrder()
        assertThat(tabs.afterSave(null)).containsExactly(all.copy(count = 6), recipes, trips).inOrder()
    }

    @Test
    fun `a delete drops the collection and counts its posts into All Saved`() {
        assertThat(tabs.afterDelete("c-1", movedToDefault = 2)).containsExactly(all.copy(count = 7), trips).inOrder()
        assertThat(tabs.afterRename(SavedCollection("c-2", "Holidays", 99)))
            .containsExactly(all, recipes, trips.copy(name = "Holidays")).inOrder()
    }

    @Test
    fun `the collection on screen falls back to All Saved once it is gone`() {
        assertThat(resolveSelectedCollection("c-1", tabs)).isEqualTo("c-1")
        assertThat(resolveSelectedCollection("c-9", tabs)).isNull()
        assertThat(resolveSelectedCollection(null, tabs)).isNull()
    }

    @Test
    fun `a moved post stays on screen only when it went to the collection on screen`() {
        assertThat(stillShownAfterMove(onScreenCollectionId = "c-1", toCollectionId = "c-1")).isTrue()
        assertThat(stillShownAfterMove(onScreenCollectionId = "c-1", toCollectionId = "c-2")).isFalse()
        assertThat(stillShownAfterMove(onScreenCollectionId = null, toCollectionId = "c-2")).isFalse()
        assertThat(stillShownAfterMove(onScreenCollectionId = null, toCollectionId = null)).isTrue()
    }

    // ── Refusals ────────────────────────────────────────────────────────

    @Test
    fun `a refusal with no code of ours reads as the action that failed`() {
        assertThat(savedCollectionRefusal(AppError.NoNetwork(), SavedAction.SAVE))
            .isEqualTo("You seem to be offline. Check your connection and try again.")
        assertThat(savedCollectionRefusal(AppError.NotFound(), SavedAction.MOVE)).isEqualTo(SavedAction.MOVE.fallback)
        assertThat(savedCollectionRefusal(AppError.NotFound(code = "NOT_SAVED"), SavedAction.MOVE))
            .isEqualTo("This post isn't in your saved items any more.")
        assertThat(savedCollectionRefusal(AppError.Unknown(code = "INVALID_COLLECTION_ID", statusCode = 400), SavedAction.MOVE))
            .isEqualTo("That collection no longer exists. It may have been deleted on another device.")
        assertThat(savedCollectionRefusal(AppError.AuthFailed(), SavedAction.LOAD)).isEqualTo("Log in again to manage your saved items.")
    }
}
