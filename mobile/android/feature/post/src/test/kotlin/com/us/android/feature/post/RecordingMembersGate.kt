package com.us.android.feature.post

import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.feature.post.data.PostMembersGate

/**
 * A [PostMembersGate] for tests (paid memberships, 2026-10-23): [levels] is
 * what the creator offers — none by default, so the picker is not offered
 * and every existing test is unchanged.
 */
class RecordingMembersGate(var levels: List<MembershipLevel> = emptyList()) : PostMembersGate {
    override suspend fun levels(): List<MembershipLevel> = levels
}

fun level(id: String = "tier-1", name: String = "Supporter", pricePaise: Long = 9_900L) = MembershipLevel(
    id = id,
    creatorId = "creator-1",
    name = name,
    pricePaise = pricePaise,
    perks = emptyList(),
    memberCount = 0,
    isActive = true,
)
