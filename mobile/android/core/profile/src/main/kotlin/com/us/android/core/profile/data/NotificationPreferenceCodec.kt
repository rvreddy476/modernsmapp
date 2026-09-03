package com.us.android.core.profile.data

import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive

/**
 * Reads and writes `/v1/notifications/preferences/detailed` by category key.
 *
 * The wire shape is a flat object: a global block plus `inapp_<category>` and
 * `push_<category>` for every [NotificationCategory]. Encoding is a FULL
 * snapshot of every known key, so the server's partial merge sees the whole
 * form each time and a stale field can never survive a save unnoticed.
 *
 * A pure object with no Android or DI dependency, so the round trip is
 * testable with plain JSON.
 */
object NotificationPreferenceCodec {

    fun decode(body: JsonObject): NotificationSettings = NotificationSettings(
        pushEnabled = body.bool(KEY_PUSH_ENABLED, default = true),
        emailEnabled = body.bool(KEY_EMAIL_ENABLED, default = false),
        quietHoursEnabled = body.bool(KEY_QUIET_ENABLED, default = false),
        quietHoursStart = body.string(KEY_QUIET_START),
        quietHoursEnd = body.string(KEY_QUIET_END),
        quietHoursTimeZone = body.string(KEY_QUIET_TZ),
        emailDigest = body.string(KEY_EMAIL_DIGEST).ifBlank { DEFAULT_DIGEST },
        channels = NotificationCategory.entries.associateWith { category ->
            NotificationChannels(
                inApp = body.bool(inAppKey(category), default = true),
                push = body.bool(pushKey(category), default = true),
            )
        },
    )

    fun encode(value: NotificationSettings): JsonObject {
        val fields = linkedMapOf(
            KEY_PUSH_ENABLED to JsonPrimitive(value.pushEnabled),
            KEY_EMAIL_ENABLED to JsonPrimitive(value.emailEnabled),
            KEY_QUIET_ENABLED to JsonPrimitive(value.quietHoursEnabled),
            KEY_QUIET_START to JsonPrimitive(value.quietHoursStart),
            KEY_QUIET_END to JsonPrimitive(value.quietHoursEnd),
            KEY_QUIET_TZ to JsonPrimitive(value.quietHoursTimeZone),
            KEY_EMAIL_DIGEST to JsonPrimitive(value.emailDigest),
        )
        NotificationCategory.entries.forEach { category ->
            val channels = value.channels(category)
            fields[inAppKey(category)] = JsonPrimitive(channels.inApp)
            fields[pushKey(category)] = JsonPrimitive(channels.push)
        }
        return JsonObject(fields)
    }

    fun inAppKey(category: NotificationCategory) = "inapp_${category.key}"
    fun pushKey(category: NotificationCategory) = "push_${category.key}"

    private fun JsonObject.bool(key: String, default: Boolean): Boolean {
        val element = this[key] ?: return default
        if (element is JsonNull) return default
        return element.jsonPrimitive.booleanOrNull ?: default
    }

    private fun JsonObject.string(key: String): String {
        val element = this[key] ?: return ""
        if (element is JsonNull) return ""
        return element.jsonPrimitive.contentOrNull.orEmpty()
    }

    private const val KEY_PUSH_ENABLED = "push_enabled"
    private const val KEY_EMAIL_ENABLED = "email_enabled"
    private const val KEY_QUIET_ENABLED = "quiet_hours_enabled"
    private const val KEY_QUIET_START = "quiet_hours_start"
    private const val KEY_QUIET_END = "quiet_hours_end"
    private const val KEY_QUIET_TZ = "quiet_hours_tz"
    private const val KEY_EMAIL_DIGEST = "email_digest"
    private const val DEFAULT_DIGEST = "weekly"
}
