package com.us.android.feature.chat.ui.home

import com.us.android.core.chat.data.CommunityRepository
import com.us.android.core.chat.data.SuggestionsRepository
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.profile.data.ProfileRepository
import javax.inject.Inject

/**
 * The two new tabs' data, bundled: Communities reads [communities],
 * Suggestions reads [suggestions] and writes follows through [followGraph],
 * naming nameless candidates from [profiles]. One injectable so the screen's
 * ViewModel keeps the inbox's dependencies at the front and these behind it.
 */
class ChatHomeSources @Inject constructor(
    val communities: CommunityRepository,
    val suggestions: SuggestionsRepository,
    val followGraph: FollowGraph,
    val profiles: ProfileRepository,
)
