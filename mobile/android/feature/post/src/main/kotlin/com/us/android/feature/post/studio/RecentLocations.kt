package com.us.android.feature.post.studio

import android.content.Context
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The places this person tagged most recently, on this device, offered as
 * chips under the details screen's Location row (2026-10-18). There is no
 * place-search endpoint on the server; until the Places key is configured a
 * location is typed, and these save typing it twice.
 */
@Singleton
class RecentLocations @Inject constructor(@ApplicationContext private val context: Context) {

    private val prefs by lazy { context.getSharedPreferences(PREFS, Context.MODE_PRIVATE) }

    suspend fun load(): List<String> = withContext(Dispatchers.IO) {
        prefs.getString(KEY, null)?.split(SEPARATOR)?.filter { it.isNotBlank() }.orEmpty()
    }

    /** Remembers [name] as the newest; returns the list as it now stands. */
    suspend fun remember(name: String): List<String> = withContext(Dispatchers.IO) {
        val next = rememberLocation(load(), name)
        prefs.edit().putString(KEY, next.joinToString(SEPARATOR)).apply()
        next
    }

    private companion object {
        const val PREFS = "post_details"
        const val KEY = "recent_locations"
        const val SEPARATOR = "\u001F"
    }
}

/**
 * [existing] with [name] put first: trimmed, once (case-insensitively), at
 * most [MAX_RECENT_LOCATIONS]. A blank name changes nothing. Pure.
 */
fun rememberLocation(existing: List<String>, name: String): List<String> {
    val place = name.trim()
    if (place.isEmpty()) return existing
    return (listOf(place) + existing.filterNot { it.equals(place, ignoreCase = true) }).take(MAX_RECENT_LOCATIONS)
}

const val MAX_RECENT_LOCATIONS = 5
