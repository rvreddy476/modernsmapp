package com.us.android.feature.dating

import com.us.android.feature.dating.network.ConsentsDto
import com.us.android.feature.dating.network.DatingProfileDto
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What Dating's screens share for the life of the process.
 *
 * ## People
 *
 * Nothing about other people is cached here any more. Matches, match detail and
 * incoming sparks each carry the server's own compact `person`, and
 * `GET /people/:userId` serves the same card on demand, so a name or photo is
 * never guessed from a deck this process happened to load.
 *
 * ## Removed people
 *
 * A block — and a report, which blocks — removes the person from EVERY list at
 * once: the deck, incoming sparks, matches, trusted contacts and share
 * recipients. The server excludes blocked pairs from its lists, but a list
 * already on screen, or a stale response in flight, must not show them again,
 * so every list is filtered through [removed] for the rest of the session.
 */
@Singleton
class DatingSession @Inject constructor() {

    private val _removed = MutableStateFlow<Set<String>>(emptySet())
    val removed: StateFlow<Set<String>> = _removed.asStateFlow()

    private val _profile = MutableStateFlow<DatingProfileDto?>(null)
    val profile: StateFlow<DatingProfileDto?> = _profile.asStateFlow()

    private val _consents = MutableStateFlow<ConsentsDto?>(null)
    val consents: StateFlow<ConsentsDto?> = _consents.asStateFlow()

    val myUserId: String? get() = _profile.value?.userId?.takeIf { it.isNotBlank() }

    fun setProfile(profile: DatingProfileDto?) {
        _profile.value = profile
    }

    fun setConsents(consents: ConsentsDto?) {
        if (consents != null) _consents.value = consents
    }

    /** Blocked or reported: gone from every list for the rest of the session. */
    fun removePerson(userId: String) {
        _removed.update { it + userId }
    }

    fun isRemoved(userId: String?): Boolean = userId != null && userId in _removed.value

    /** The other participant of a match. */
    fun otherOf(userA: String, userB: String): String = if (userA == myUserId) userB else userA

    /** Sign-out or profile deletion: nothing about anyone survives. */
    fun clear() {
        _profile.value = null
        _consents.value = null
    }
}
