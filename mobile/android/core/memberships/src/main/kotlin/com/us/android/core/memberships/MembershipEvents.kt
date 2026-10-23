package com.us.android.core.memberships

import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Says, app-wide, that the viewer's access to a creator's members-only posts
 * changed — a join or a renewal the SERVER confirmed paid, or a level change.
 * Every surface that drew a lock from a cached row (a feed page, a reel, the
 * watch page) re-reads on it: post-service redacts per viewer at read time,
 * so a re-read is all an unlock takes. Never a source of access itself.
 */
@Singleton
class MembershipEvents @Inject constructor() {

    private val _accessChanged = MutableSharedFlow<String>(extraBufferCapacity = 8)

    /** The creator id whose members-only posts this viewer may now see differently. */
    val accessChanged: SharedFlow<String> = _accessChanged.asSharedFlow()

    fun accessChanged(creatorId: String) {
        if (creatorId.isNotBlank()) _accessChanged.tryEmit(creatorId)
    }
}
