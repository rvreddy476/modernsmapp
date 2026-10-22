package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.JoinMethod
import com.us.android.core.chat.data.MyJoin
import com.us.android.core.chat.data.toDomain
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * A group newcomer sees only what was sent after they joined (founder,
 * 2026-10-22); GET /conversations/:id says how they joined as `my_join`.
 */
class MyJoinMappingTest {
    private val json = Json { ignoreUnknownKeys = true }

    private fun conversation(type: String, myJoin: String?) = json.decodeFromString(
        ConversationDto.serializer(),
        """{"id":"c1","type":"$type","members":[],"updated_at":"2026-10-22T00:00:00Z"""" +
            (myJoin?.let { ""","my_join":$it""" } ?: "") + "}",
    ).toDomain()

    @Test
    fun `each way in reaches the conversation`() {
        assertThat(conversation("group", """{"joined_at":"2026-10-22T09:30:00Z","method":"added","added_by_user_id":"u1"}""").myJoin)
            .isEqualTo(MyJoin("2026-10-22T09:30:00Z", JoinMethod.Added, "u1"))
        assertThat(conversation("group", """{"joined_at":"2026-10-22T09:30:00.123456+05:30","method":"invitation","added_by_user_id":"u1"}""").myJoin)
            .isEqualTo(MyJoin("2026-10-22T09:30:00.123456+05:30", JoinMethod.Invitation, "u1"))
        assertThat(conversation("group", """{"joined_at":"2026-10-22T09:30:00Z","method":"invite_link"}""").myJoin)
            .isEqualTo(MyJoin("2026-10-22T09:30:00Z", JoinMethod.InviteLink, null))
    }

    @Test
    fun `no note when the server sends none, or nothing usable`() {
        assertThat(conversation("group", null).myJoin).isNull()
        assertThat(conversation("group", "null").myJoin).isNull()
        assertThat(conversation("group", """{"joined_at":"2026-10-22T09:30:00Z","method":"created"}""").myJoin).isNull()
        assertThat(conversation("group", """{"joined_at":"2026-10-22T09:30:00Z","method":"teleported"}""").myJoin).isNull()
        assertThat(conversation("group", """{"joined_at":"","method":"added"}""").myJoin).isNull()
        assertThat(conversation("group", """{"joined_at":"yesterday","method":"added"}""").myJoin).isNull()
        // Only a group has one.
        assertThat(conversation("direct", """{"joined_at":"2026-10-22T09:30:00Z","method":"added"}""").myJoin).isNull()
    }

    @Test
    fun `an empty adder is unknown, and a link never names one`() {
        assertThat(conversation("group", """{"joined_at":"2026-10-22T09:30:00Z","method":"added","added_by_user_id":""}""").myJoin?.addedByUserId)
            .isNull()
        assertThat(conversation("group", """{"joined_at":"2026-10-22T09:30:00Z","method":"invite_link","added_by_user_id":"u1"}""").myJoin?.addedByUserId)
            .isNull()
    }
}
