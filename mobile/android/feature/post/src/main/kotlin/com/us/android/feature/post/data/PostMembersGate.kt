package com.us.android.feature.post.data

import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.data.MembershipsRepository
import javax.inject.Inject

/**
 * "Members only" for what this module posts (paid memberships, 2026-10-23).
 *
 * The level rides the create itself (`tier_required_id` on POST /v1/posts,
 * post-service handler.go:556), so a members-only post is gated from birth
 * and never public for a moment. This port only answers which levels the
 * creator may pick; a port so the composers are tested without the
 * membership stack. Refusals read with `membersOnlyRefusal`.
 */
fun interface PostMembersGate {
    /** The creator's ACTIVE levels, cheapest first; empty when none (the picker is then not offered). */
    suspend fun levels(): List<MembershipLevel>
}

class ApiPostMembersGate @Inject constructor(
    private val memberships: MembershipsRepository,
) : PostMembersGate {
    override suspend fun levels(): List<MembershipLevel> = memberships.ownActiveLevels()
}
