package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.toDomain
import kotlinx.serialization.json.Json
import org.junit.Test

/** DM consent (2026-10-20): a requests-list row's `preview` becomes the row's message text. */
class RequestPreviewMappingTest {
    private val json = Json { ignoreUnknownKeys = true }

    @Test
    fun `the requests-list preview reaches the conversation`() {
        val dto = json.decodeFromString(
            ConversationDto.serializer(),
            """{"id":"c1","type":"direct","is_request":true,"members":[],"updated_at":"2026-10-20T00:00:00Z","preview":"Hello from a stranger","direction":"incoming"}""",
        )
        assertThat(dto.toDomain().lastMessagePreview).isEqualTo("Hello from a stranger")
    }
}
