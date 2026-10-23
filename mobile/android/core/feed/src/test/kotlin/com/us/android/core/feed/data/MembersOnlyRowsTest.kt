package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.model.MembersAccess
import com.us.android.core.model.MembersOnly
import com.us.android.core.model.membersAccess
import com.us.android.core.model.membersPlayback
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import kotlinx.serialization.builtins.ListSerializer
import org.junit.Test
import java.io.File

/**
 * Members-only rows (paid memberships, 2026-10-23), read the way the paging
 * source reads them. Today a gated post decoded as an empty post: the keys
 * were dropped by `ignoreUnknownKeys`.
 *
 * `feed_rows_members_only.json` is written from the server's rule, as no
 * golden capture exists yet: post-service members_read.go (the redacted row
 * — text and media cleared, title kept, `body_redacted: true`, `duration_ms`)
 * and posts.go:131-144 (`tier_required_id`, `body_redacted`, `duration_ms`,
 * `tier_required_price_paise`), passed through by feed-service
 * hydration.go:177-182. Row 1 is redacted for this viewer, row 2 is the same
 * creator's gated video sent whole (a member), row 3 is open.
 */
class MembersOnlyRowsTest {

    private val app = NetworkModule.provideJson()
    private val rows = app.decodeFromString(
        ApiEnvelope.serializer(ListSerializer(FeedItemDto.serializer())),
        File("src/test/resources/contracts/memberships/feed_rows_members_only.json").readText(),
    ).data.orEmpty().map { it.toDomain() }

    @Test
    fun `a redacted row keeps its title and level and is the lock`() {
        val redacted = rows[0]
        assertThat(redacted.title).isEqualTo("Episode 4")
        assertThat(redacted.text).isEmpty()
        assertThat(redacted.media).isEmpty()
        assertThat(redacted.membersOnly).isEqualTo(
            MembersOnly(
                tierRequiredId = "7b1d3c2e-0000-4000-8000-000000000001",
                bodyRedacted = true,
                redactedDurationMs = 754_000,
                requiredPricePaise = 9_900,
            ),
        )
        assertThat(redacted.membersAccess(viewerId = "viewer")).isEqualTo(MembersAccess.Locked)
        assertThat(redacted.membersPlayback(viewerId = "viewer")).isEqualTo(MembersAccess.Locked)
    }

    @Test
    fun `a member's gated row is whole, plays, and is still marked members-only`() {
        val member = rows[1]
        assertThat(member.membersOnly.isGated).isTrue()
        assertThat(member.membersOnly.bodyRedacted).isFalse()
        assertThat(member.media).hasSize(1)
        assertThat(member.membersPlayback(viewerId = "viewer")).isEqualTo(MembersAccess.Member)
    }

    @Test
    fun `an open row carries no gate`() {
        assertThat(rows[2].membersOnly).isEqualTo(MembersOnly.None)
        assertThat(rows[2].membersAccess(viewerId = "viewer")).isEqualTo(MembersAccess.Open)
    }
}
