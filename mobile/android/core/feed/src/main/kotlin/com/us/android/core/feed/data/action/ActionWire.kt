package com.us.android.core.feed.data.action

import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.serverCode
import com.us.android.core.model.ActionButtons
import com.us.android.core.model.ActionDraft
import com.us.android.core.model.ActionPatch
import com.us.android.core.model.ActionProblem
import com.us.android.core.model.PostAction
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put

/**
 * The Action button on the wire (contract 23 Oct 2026). Every post, channel
 * and library DTO carries `action` as a raw [JsonElement] and reads it here,
 * tolerantly: absent, `null`, `""`, an array or an object without an id or a
 * label all read as no button, and a status this build does not know is not
 * live (fail closed, [com.us.android.core.model.ActionStatus.fromWire]).
 *
 * Writes: the create carries `{label, url}` only when there is a button
 * ([ActionWriteDto]); the PATCH carries the key only when it moved, with an
 * explicit JSON `null` to remove ([toJson]) — the app's `Json` drops a Kotlin
 * null (`explicitNulls` off), so the removal must be a [JsonNull] value.
 */
fun JsonElement?.toPostAction(): PostAction? {
    val o = this as? JsonObject ?: return null
    return PostAction.fromWire(
        id = o.text("id"),
        label = o.text("label"),
        labelText = o.text("label_text"),
        domain = o.text("domain"),
        href = o.text("href"),
        status = o.text("status"),
        url = o.text("url"),
        statusReason = o.text("status_reason"),
    )
}

/** A string field, or null for anything else (a number, an object, JSON null). */
private fun JsonObject.text(key: String): String? = (this[key] as? JsonPrimitive)?.takeIf { it.isString }?.content

/** The create body's `action`: `{label, url}`. */
@Serializable
data class ActionWriteDto(
    val label: String,
    val url: String,
)

/** The create's action: the trimmed button, or null — left off the body — when there is none. */
fun ActionDraft?.toWriteDto(): ActionWriteDto? = ActionButtons.createWrite(this)?.let { ActionWriteDto(it.label, it.url) }

/** The PATCH's action: null (key left off) when unchanged, JSON `null` to remove, `{label, url}` to set. */
fun ActionPatch.toJson(): JsonElement? = when (this) {
    ActionPatch.Unchanged -> null
    ActionPatch.Remove -> JsonNull
    is ActionPatch.Set -> buildJsonObject {
        put("label", label)
        put("url", url)
    }
}

/** A saved draft's action read back (`{label, url}` in a draft payload); anything else is no button. */
fun JsonElement?.toActionDraft(): ActionDraft? {
    val o = this as? JsonObject ?: return null
    val label = (o["label"] as? JsonPrimitive)?.takeIf { it.isString }?.content?.trim().orEmpty()
    if (label.isEmpty()) return null
    val url = (o["url"] as? JsonPrimitive)?.takeIf { it.isString }?.content?.trim().orEmpty()
    return ActionDraft(label, url)
}

/** `GET /v1/links/:id` → `{id, url, domain, label, status}`. */
@Serializable
data class ActionLinkDto(
    val id: String = "",
    val url: String = "",
    val domain: String = "",
    val label: String = "",
    val status: String = "",
)

/** `GET /v1/posts/:id/action/stats` → `{clicks_total, clicks_7d}`. */
@Serializable
data class ActionStatsDto(
    @SerialName("clicks_total") val clicksTotal: Long = 0,
    @SerialName("clicks_7d") val clicks7d: Long = 0,
)

/** One row of `GET /v1/posts/action-labels`. */
@Serializable
data class ActionLabelDto(
    val value: String? = null,
    val label: String? = null,
)

/**
 * A refusal of the button, in our words, or null when [this] is about
 * something else. 400s arrive as [AppError.Unknown], the 503 as
 * [AppError.Server], ACTION_NOT_ELIGIBLE as [AppError.Forbidden] whose
 * `details.missing` the mapper kept as the array's JSON text.
 */
fun AppError.actionProblem(json: Json = LENIENT): ActionProblem? {
    val code = serverCode() ?: return null
    val details = (this as? AppError.Forbidden)?.details.orEmpty()
    val missing = details["missing"]?.let { raw -> missingOf(raw, json) }.orEmpty()
    // An account too new may be told from when (2026-10-24): `details.eligible_at`, RFC 3339.
    return ActionButtons.problemOf(code, missing, eligibleAt = details["eligible_at"])
}

private fun missingOf(raw: String, json: Json): List<String> {
    val parsed = runCatching { json.parseToJsonElement(raw) }.getOrNull()
    return when (parsed) {
        is JsonArray -> parsed.mapNotNull { (it as? JsonPrimitive)?.takeIf { p -> p.isString }?.content }
        // A single name the mapper flattened to its content.
        else -> listOf(raw).filter { it.isNotBlank() && !it.startsWith("[") }
    }
}

private val LENIENT = Json { ignoreUnknownKeys = true }
