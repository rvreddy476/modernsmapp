package com.us.android.core.feed.ui.credits

import com.us.android.core.feed.data.credits.CreditGenre
import com.us.android.core.feed.data.credits.CreditPerson
import com.us.android.core.feed.data.credits.CreditsForm
import com.us.android.core.feed.data.credits.creditsProblems
import com.us.android.core.feed.data.credits.moved
import com.us.android.core.feed.data.credits.toggledGenre
import com.us.android.core.feed.data.credits.withName
import com.us.android.core.feed.data.credits.withNewEntry
import com.us.android.core.feed.data.credits.withPerson
import com.us.android.core.feed.data.credits.withProduction
import com.us.android.core.feed.data.credits.withRole
import com.us.android.core.feed.data.credits.withYear
import com.us.android.core.feed.data.credits.without
import com.us.android.core.feed.data.credits.withoutPerson
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Year

/** The Credits section, as it draws. */
data class CreditsEditorState(
    val form: CreditsForm = CreditsForm(),
    /** The server's genres; empty until they load. */
    val genres: List<CreditGenre> = emptyList(),
    val genresLoading: Boolean = false,
    /** The genre list could not be read: the section offers Retry. */
    val genresFailed: Boolean = false,
    /** The row whose "Link a VChat account" search is open, or null. */
    val linkingKey: String? = null,
    val query: String = "",
    val results: List<CreditPerson> = emptyList(),
    val searching: Boolean = false,
    /** The checks a save ran, until the next edit. */
    val problems: List<String> = emptyList(),
) {
    /** The label for a genre slug, when the list has it. */
    fun genreLabel(slug: String): String? = genres.firstOrNull { it.slug == slug }?.label
}

/**
 * The Credits editor's state and verbs, shared by the upload's Credits step
 * and the owner's Edit sheet: each ViewModel owns one on its own scope, so
 * neither needs the other's screen. The rules themselves are the pure
 * functions in `CreditsForm.kt`; this holds the form, runs the people
 * search (debounced, newest query wins) and loads the genre list once.
 */
// One verb per control of the section; a smaller surface would push the rules into the screen.
@Suppress("TooManyFunctions")
class CreditsEditor(
    private val scope: CoroutineScope,
    /** The existing user suggester (people search); empty on any failure. */
    private val searchPeople: suspend (String) -> List<CreditPerson>,
    /** The server's genre list; null when it could not be read. */
    private val loadGenres: suspend () -> List<CreditGenre>?,
    private val currentYear: () -> Int = { Year.now().value },
    private val debounceMillis: Long = SEARCH_DEBOUNCE_MILLIS,
) {

    private val _state = MutableStateFlow(CreditsEditorState())
    val state: StateFlow<CreditsEditorState> = _state.asStateFlow()

    private var nextKey = 0
    private var searchJob: Job? = null
    private var genresJob: Job? = null

    /** A fresh local key for a new row. */
    fun newKey(): String = "credit-${nextKey++}"

    /** The form as a saved video has it (the Edit sheet), replacing whatever was there. */
    fun reset(form: CreditsForm) {
        searchJob?.cancel()
        _state.update { it.copy(form = form, linkingKey = null, query = "", results = emptyList(), problems = emptyList()) }
    }

    /** Reads the genre list unless it is loaded or loading. */
    fun ensureGenres() {
        val now = _state.value
        if (now.genres.isNotEmpty() || genresJob?.isActive == true) return
        _state.update { it.copy(genresLoading = true, genresFailed = false) }
        genresJob = scope.launch {
            val loaded = loadGenres()
            _state.update {
                it.copy(genres = loaded.orEmpty(), genresLoading = false, genresFailed = loaded == null)
            }
        }
    }

    fun retryGenres() = ensureGenres()

    fun addCredit() = edit { it.withNewEntry(newKey()) }

    fun setName(key: String, name: String) = edit { it.withName(key, name) }

    fun setRole(key: String, role: String) = edit { it.withRole(key, role) }

    fun remove(key: String) {
        if (_state.value.linkingKey == key) stopLinking()
        edit { it.without(key) }
    }

    fun moveUp(key: String) = edit { it.moved(key, -1) }

    fun moveDown(key: String) = edit { it.moved(key, 1) }

    fun toggleGenre(slug: String) = edit { it.toggledGenre(slug) }

    fun setYear(typed: String) = edit { it.withYear(typed) }

    fun setProduction(typed: String) = edit { it.withProduction(typed) }

    // ── Linking an account ──────────────────────────────────────────────

    /** Opens the account search under [key]; one row searches at a time. */
    fun startLinking(key: String) {
        searchJob?.cancel()
        val name = _state.value.form.entries.firstOrNull { it.key == key }?.name.orEmpty()
        _state.update { it.copy(linkingKey = key, query = "", results = emptyList(), searching = false) }
        if (name.isNotBlank()) onQuery(name)
    }

    fun stopLinking() {
        searchJob?.cancel()
        _state.update { it.copy(linkingKey = null, query = "", results = emptyList(), searching = false) }
    }

    /** From two characters, asks the suggester; the newest query wins. */
    fun onQuery(query: String) {
        _state.update { it.copy(query = query) }
        searchJob?.cancel()
        val trimmed = query.trim().removePrefix("@")
        if (trimmed.length < MIN_QUERY) {
            _state.update { it.copy(results = emptyList(), searching = false) }
            return
        }
        _state.update { it.copy(searching = true) }
        searchJob = scope.launch {
            delay(debounceMillis)
            val found = searchPeople(trimmed)
            _state.update { it.copy(results = found, searching = false) }
        }
    }

    /** The account picked from the results becomes the row's link, and the search closes. */
    fun link(person: CreditPerson) {
        val key = _state.value.linkingKey ?: return
        edit { it.withPerson(key, person) }
        stopLinking()
    }

    fun unlink(key: String) = edit { it.withoutPerson(key) }

    // ── Saving ──────────────────────────────────────────────────────────

    /** Runs the checks; what they find is shown under the section and returned. */
    fun validate(): List<String> {
        val problems = creditsProblems(_state.value.form, currentYear())
        _state.update { it.copy(problems = problems) }
        return problems
    }

    /** The server's refusal of the credits, in the section's own words (`creditRefusal`), until the next edit. */
    fun showRefusal(message: String) = _state.update { it.copy(problems = listOf(message)) }

    private fun edit(change: (CreditsForm) -> CreditsForm) =
        _state.update { it.copy(form = change(it.form), problems = emptyList()) }

    companion object {
        const val MIN_QUERY = 2
        const val SEARCH_DEBOUNCE_MILLIS = 300L
    }
}
