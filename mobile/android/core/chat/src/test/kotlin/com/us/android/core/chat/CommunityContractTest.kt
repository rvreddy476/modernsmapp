package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CommunityDto
import com.us.android.core.chat.data.CommunityEventDto
import com.us.android.core.chat.data.CommunitySuggestionsDto
import com.us.android.core.chat.data.CommunityUpdateDto
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.InviteLinkDto
import com.us.android.core.chat.data.PeopleSuggestionsDto
import com.us.android.core.chat.data.PostUpdateRequest
import com.us.android.core.chat.data.UpdateGroupInfoRequest
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Pins the 2026-09-05 DTOs to the contract memo's shapes: a community row,
 * an update with its event and reactions, the discover page cursor, the
 * suggestion pages, the invite link, and the group PUT body.
 */
class CommunityContractTest {

    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `a community decodes with the viewer's role and counts`() {
        val envelope: ApiEnvelope<CommunityDto> = json.decodeFromString(COMMUNITY)
        val community = envelope.data!!
        assertThat(community.id).isEqualTo("riders_1788614077")
        assertThat(community.handle).isEqualTo("riders")
        assertThat(community.memberCount).isEqualTo(42)
        assertThat(community.viewerRole).isEqualTo("subscriber")
        assertThat(community.viewerMuted).isFalse()
        assertThat(community.canPost).isFalse()
        assertThat(community.avatarMediaId).isNull()
    }

    @Test
    fun `an update decodes its event, reactions and the viewer's own`() {
        val envelope: ApiEnvelope<List<CommunityUpdateDto>> = json.decodeFromString(UPDATES_PAGE)
        val update = envelope.data!!.single()
        assertThat(update.id).isEqualTo("u-1")
        assertThat(update.title).isEqualTo("Sunday ride")
        assertThat(update.mediaIds).containsExactly("m-1", "m-2")
        assertThat(update.event?.title).isEqualTo("Sunday ride")
        assertThat(update.event?.startsAt).isEqualTo("2026-09-12T09:00:00Z")
        assertThat(update.event?.location).isEqualTo("Cubbon Park gate")
        assertThat(update.reactions.map { it.emoji to it.count }).containsExactly("👍" to 3, "🔥" to 1)
        assertThat(update.viewerReaction).isEqualTo("🔥")
        assertThat(update.reactionCount).isEqualTo(4)
        assertThat(update.viewCount).isEqualTo(17)
        assertThat(envelope.meta?.nextCursor).isEqualTo("c2")
    }

    @Test
    fun `an update without an event or reactions decodes with empties`() {
        val update: CommunityUpdateDto = json.decodeFromString("""{"id":"u-2","body":"hi"}""")
        assertThat(update.event).isNull()
        assertThat(update.reactions).isEmpty()
        assertThat(update.viewerReaction).isNull()
        assertThat(update.mediaIds).isEmpty()
    }

    @Test
    fun `the suggestion pages decode their typed items`() {
        val people: ApiEnvelope<PeopleSuggestionsDto> = json.decodeFromString(PEOPLE)
        assertThat(people.data!!.type).isEqualTo("friend")
        val person = people.data!!.items.single()
        assertThat(person.candidateUserId).isEqualTo("u-9")
        assertThat(person.displayName).isEmpty()
        assertThat(person.mutualFriendCount).isEqualTo(1)
        assertThat(person.explainText).isEqualTo("1 mutual friend")

        val communities: ApiEnvelope<CommunitySuggestionsDto> = json.decodeFromString(COMMUNITY_SUGGESTIONS)
        val row = communities.data!!.items.single()
        assertThat(row.communityId).isEqualTo("riders_1788614209")
        assertThat(row.memberCount).isEqualTo(5)
        assertThat(row.joinPath).isEqualTo("open")
    }

    @Test
    fun `an invite link decodes its cap and uses`() {
        val envelope: ApiEnvelope<InviteLinkDto> = json.decodeFromString(INVITE_LINK)
        val link = envelope.data!!
        assertThat(link.code).isEqualTo("k7Qm")
        assertThat(link.url).isEqualTo("https://atpost.app/chat/join/k7Qm")
        assertThat(link.maxUses).isEqualTo(10)
        assertThat(link.uses).isEqualTo(3)
    }

    @Test
    fun `a conversation carries its description and signed avatar url`() {
        val conversation: ConversationDto = json.decodeFromString(
            """{"id":"c","type":"group","title":"Riders","description":"Weekend rides","avatar_url":"https://s/x.jpg",
               "members":[{"user_id":"u1","role":"owner","display_name":"A","avatar_media_id":"m-a"}]}""",
        )
        assertThat(conversation.description).isEqualTo("Weekend rides")
        assertThat(conversation.avatarUrl).isEqualTo("https://s/x.jpg")
        assertThat(conversation.members.single().avatarMediaId).isEqualTo("m-a")
    }

    @Test
    fun `the group PUT sends only the fields that were set`() {
        assertThat(json.encodeToString(UpdateGroupInfoRequest.serializer(), UpdateGroupInfoRequest(description = "Hi")))
            .isEqualTo("""{"description":"Hi"}""")
        assertThat(
            json.encodeToString(UpdateGroupInfoRequest.serializer(), UpdateGroupInfoRequest(avatarMediaId = "m"))
        )
            .isEqualTo("""{"avatar_media_id":"m"}""")
    }

    @Test
    fun `the post body carries the event under the contract's keys`() {
        val encoded = json.encodeToString(
            PostUpdateRequest.serializer(),
            PostUpdateRequest(
                body = "Ride!",
                mediaIds = listOf("m-1"),
                event = CommunityEventDto("Sunday ride", "2026-09-12T09:00:00Z", "", "Gate"),
            ),
        )
        assertThat(encoded).contains(""""media_ids":["m-1"]""")
        assertThat(encoded).contains(""""starts_at":"2026-09-12T09:00:00Z"""")
        // The optional top-level title is OMITTED, not sent as null; the event keeps its own.
        assertThat(encoded).startsWith("""{"body":"Ride!"""")
        assertThat(encoded).doesNotContain("null")
    }

    private companion object {
        const val COMMUNITY = """{"data":{"id":"riders_1788614077","owner_id":"o1","handle":"riders","name":"Riders",
            "description":"Weekend rides","visibility":"public","member_count":42,"update_count":7,
            "is_verified":false,"viewer_role":"subscriber","viewer_muted":false,"can_post":false,
            "created_at":"2026-09-05T10:00:00Z"}}"""

        const val UPDATES_PAGE = """{"data":[{"id":"u-1","channel_id":"riders_1788614077","author_id":"o1",
            "update_type":"event","title":"Sunday ride","body":"Meet at nine.","media_ids":["m-1","m-2"],
            "event":{"title":"Sunday ride","starts_at":"2026-09-12T09:00:00Z","ends_at":"2026-09-12T12:00:00Z",
            "location":"Cubbon Park gate"},"is_pinned":true,"published_at":"2026-09-05T10:00:00Z","view_count":17,
            "reaction_count":4,"reactions":[{"emoji":"👍","count":3},{"emoji":"🔥","count":1}],
            "viewer_reaction":"🔥","can_edit":false,"created_at":"2026-09-05T10:00:00Z"}],
            "meta":{"next_cursor":"c2"}}"""

        const val PEOPLE = """{"data":{"type":"friend","items":[{"candidate_user_id":"u-9","display_name":"",
            "score":0.7,"reason_codes":["mutual"],"explain_text":"1 mutual friend","mutual_friend_count":1,
            "mutual_friend_ids":["u-2"],"source_bucket":"fof"}]}}"""

        const val COMMUNITY_SUGGESTIONS = """{"data":{"type":"community","items":[{"community_id":"riders_1788614209",
            "owner_id":"o2","handle":"riders2","name":"Riders 2","description":"","member_count":5,"update_count":1,
            "reason_codes":["popular"],"explain_text":"Popular near you","join_path":"open"}]}}"""

        const val INVITE_LINK = """{"data":{"code":"k7Qm","url":"https://atpost.app/chat/join/k7Qm",
            "conversation_id":"c1","expires_at":"2026-09-12T00:00:00Z","max_uses":10,"uses":3}}"""
    }
}
