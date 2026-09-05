package com.us.android.feature.search.data

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.emptyPreferences
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.map
import java.io.IOException
import javax.inject.Inject
import javax.inject.Singleton

/** The last few things searched for, newest first, shown while the field is empty. */
interface RecentSearchStore {
    val recent: Flow<List<String>>

    /** A search was made: it goes to the top; a repeat moves up rather than doubling. */
    suspend fun record(query: String)

    /** "Clear all". */
    suspend fun clear()
}

/**
 * The list's rules, pure so they are a table test: trimmed, no blanks,
 * newest first, one entry per query (case-insensitive — "Cats" and "cats"
 * are the same search), and at most [MAX].
 */
object RecentSearchList {
    const val MAX = 10

    /** Queries never contain a newline (the field is single-line), so one is a safe separator. */
    private const val SEPARATOR = "\n"

    fun push(current: List<String>, query: String): List<String> {
        val entry = query.trim()
        if (entry.isEmpty()) return current
        return (listOf(entry) + current.filterNot { it.equals(entry, ignoreCase = true) }).take(MAX)
    }

    fun encode(list: List<String>): String = list.joinToString(SEPARATOR)

    fun decode(encoded: String?): List<String> =
        encoded?.split(SEPARATOR)?.map { it.trim() }?.filter { it.isNotEmpty() }?.take(MAX).orEmpty()
}

private val Context.searchDataStore: DataStore<Preferences> by preferencesDataStore(name = "us_search")

/** The list on disk: its own small preferences file, cleared with the rest of the app's data. */
@Singleton
class DataStoreRecentSearches @Inject constructor(
    @ApplicationContext private val context: Context,
) : RecentSearchStore {

    private val store = context.searchDataStore

    override val recent: Flow<List<String>> = store.data
        .catch { cause -> if (cause is IOException) emit(emptyPreferences()) else throw cause }
        .map { RecentSearchList.decode(it[KEY_RECENT]) }

    override suspend fun record(query: String) {
        store.edit { prefs ->
            val next = RecentSearchList.push(RecentSearchList.decode(prefs[KEY_RECENT]), query)
            prefs[KEY_RECENT] = RecentSearchList.encode(next)
        }
    }

    override suspend fun clear() {
        store.edit { it.remove(KEY_RECENT) }
    }

    private companion object {
        val KEY_RECENT = stringPreferencesKey("recent_searches")
    }
}
