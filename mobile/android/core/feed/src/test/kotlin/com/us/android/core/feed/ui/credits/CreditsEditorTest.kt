package com.us.android.core.feed.ui.credits

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.credits.CreditGenre
import com.us.android.core.feed.data.credits.CreditLimits
import com.us.android.core.feed.data.credits.CreditPerson
import com.us.android.core.feed.data.credits.CreditsForm
import com.us.android.core.feed.data.credits.creditRefusal
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * The Credits editor both the upload's step and the Edit sheet own
 * (2026-10-23): the thirty-row limit, reordering, the account search
 * (debounced, newest query wins), the genre list and its retry, and the
 * checks a save runs.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CreditsEditorTest {

    private val queries = mutableListOf<String>()
    private var genresAnswer: List<CreditGenre>? = listOf(CreditGenre("drama", "Drama"))
    private var genreCalls = 0

    private fun TestScope.editor() = CreditsEditor(
        // The test scope itself, not backgroundScope: advanceUntilIdle skips background work.
        scope = this,
        searchPeople = { query ->
            queries += query
            listOf(CreditPerson("u1", "Asha Rao", "asha"))
        },
        loadGenres = {
            genreCalls++
            genresAnswer
        },
        currentYear = { 2026 },
    )

    @Test
    fun `adds rows up to thirty and no more`() = runTest {
        val editor = editor()

        repeat(CreditLimits.MAX_CREDITS + 3) { editor.addCredit() }

        assertThat(editor.state.value.form.entries).hasSize(30)
        assertThat(editor.state.value.form.entries.map { it.key }.toSet()).hasSize(30)
    }

    @Test
    fun `reorders and removes by row`() = runTest {
        val editor = editor()
        repeat(3) { editor.addCredit() }
        val (a, b, c) = editor.state.value.form.entries.map { it.key }

        editor.moveUp(c)
        editor.moveDown(a)
        assertThat(editor.state.value.form.entries.map { it.key }).containsExactly(c, a, b).inOrder()

        editor.remove(a)
        assertThat(editor.state.value.form.entries.map { it.key }).containsExactly(c, b).inOrder()
    }

    @Test
    fun `the account search waits for the typing to settle and the newest query wins`() = runTest {
        val editor = editor()
        editor.addCredit()
        val key = editor.state.value.form.entries.single().key

        editor.startLinking(key)
        editor.onQuery("a")
        assertThat(editor.state.value.searching).isFalse()
        editor.onQuery("as")
        editor.onQuery("ash")
        advanceTimeBy(CreditsEditor.SEARCH_DEBOUNCE_MILLIS - 1)
        runCurrent()
        assertThat(queries).isEmpty()
        advanceUntilIdle()

        assertThat(queries).containsExactly("ash")
        assertThat(editor.state.value.results.map { it.id }).containsExactly("u1")

        editor.link(editor.state.value.results.single())
        val entry = editor.state.value.form.entries.single()
        assertThat(entry.person?.id).isEqualTo("u1")
        assertThat(entry.name).isEqualTo("Asha Rao")
        assertThat(editor.state.value.linkingKey).isNull()
    }

    @Test
    fun `linking a row that already has a name searches for it`() = runTest {
        val editor = editor()
        editor.addCredit()
        val key = editor.state.value.form.entries.single().key
        editor.setName(key, "Ravi")

        editor.startLinking(key)
        advanceUntilIdle()

        assertThat(queries).containsExactly("Ravi")
    }

    @Test
    fun `the genre list loads once, and a failure offers retry`() = runTest {
        genresAnswer = null
        val editor = editor()

        editor.ensureGenres()
        advanceUntilIdle()
        assertThat(editor.state.value.genresFailed).isTrue()

        genresAnswer = listOf(CreditGenre("drama", "Drama"))
        editor.retryGenres()
        advanceUntilIdle()
        editor.ensureGenres()
        advanceUntilIdle()

        assertThat(editor.state.value.genresFailed).isFalse()
        assertThat(editor.state.value.genreLabel("drama")).isEqualTo("Drama")
        assertThat(genreCalls).isEqualTo(2)
    }

    @Test
    fun `validate reports what a save would be refused for, and an edit clears it`() = runTest {
        val editor = editor()
        editor.addCredit()
        val key = editor.state.value.form.entries.single().key
        editor.setName(key, "Asha")
        editor.setYear("1850")

        val problems = editor.validate()

        assertThat(problems).containsExactly(
            "Every credit needs a role.",
            "The release year must be between 1900 and 2027.",
        ).inOrder()
        assertThat(editor.state.value.problems).isEqualTo(problems)
        editor.setRole(key, "Director")
        assertThat(editor.state.value.problems).isEmpty()
    }

    @Test
    fun `reset replaces the form and closes any search`() = runTest {
        val editor = editor()
        editor.addCredit()
        editor.startLinking(editor.state.value.form.entries.single().key)

        editor.reset(CreditsForm(genres = listOf("drama")))

        assertThat(editor.state.value.form.entries).isEmpty()
        assertThat(editor.state.value.form.genres).containsExactly("drama")
        assertThat(editor.state.value.linkingKey).isNull()
    }

    /** A save the server refused over the credits says why under the section, in words, until the next edit. */
    @Test
    fun `a server refusal shows under the section and goes with the next edit`() = runTest {
        val editor = editor()
        val said = creditRefusal("INVALID_GENRE")!!

        editor.showRefusal(said)

        assertThat(editor.state.value.problems).containsExactly("Pick up to 3 genres from the list.")
        editor.toggleGenre("drama")
        assertThat(editor.state.value.problems).isEmpty()
    }
}
