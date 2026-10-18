package com.us.android.feature.post.studio

import com.us.android.feature.post.createhub.ReelOption
import java.util.Locale

/**
 * The languages a post can be marked as, by name (2026-10-19).
 *
 * The row used to be a text field showing the raw BCP-47 code — "en" — which
 * nobody reads as a language. The value sent to the server is still the code;
 * only what people see is the name. Indian languages first (the pilot is in
 * India), each with its own-script name as the hint, then widely used others.
 */
internal val POST_LANGUAGE_OPTIONS: List<ReelOption> = listOf(
    ReelOption("en", "English"),
    ReelOption("hi", "Hindi", "हिन्दी"),
    ReelOption("te", "Telugu", "తెలుగు"),
    ReelOption("ta", "Tamil", "தமிழ்"),
    ReelOption("kn", "Kannada", "ಕನ್ನಡ"),
    ReelOption("ml", "Malayalam", "മലയാളം"),
    ReelOption("mr", "Marathi", "मराठी"),
    ReelOption("bn", "Bengali", "বাংলা"),
    ReelOption("gu", "Gujarati", "ગુજરાતી"),
    ReelOption("pa", "Punjabi", "ਪੰਜਾਬੀ"),
    ReelOption("or", "Odia", "ଓଡ଼ିଆ"),
    ReelOption("as", "Assamese", "অসমীয়া"),
    ReelOption("ur", "Urdu", "اردو"),
    ReelOption("ar", "Arabic", "العربية"),
    ReelOption("es", "Spanish", "Español"),
    ReelOption("fr", "French", "Français"),
    ReelOption("de", "German", "Deutsch"),
    ReelOption("pt", "Portuguese", "Português"),
    ReelOption("id", "Indonesian", "Bahasa Indonesia"),
    ReelOption("ja", "Japanese", "日本語"),
    ReelOption("ko", "Korean", "한국어"),
    ReelOption("zh", "Chinese", "中文"),
)

/**
 * The name to show for a post language code.
 *
 * A region-tagged code ("en-IN") shows its base language's name; a code not in
 * the list falls back to the platform's English name for it, and only a code
 * nothing recognises is shown as itself.
 */
internal fun postLanguageLabel(code: String): String {
    val tag = code.trim()
    if (tag.isEmpty()) return "Not set"
    val base = Locale.forLanguageTag(tag).language.ifEmpty { tag.lowercase(Locale.ROOT) }
    POST_LANGUAGE_OPTIONS.firstOrNull { it.value == base }?.let { return it.label }
    return Locale.forLanguageTag(tag).getDisplayLanguage(Locale.ENGLISH).ifEmpty { tag }
}

/** The list entry a code selects: its base language, so "en-IN" ticks English. */
internal fun postLanguageSelection(code: String): String =
    Locale.forLanguageTag(code.trim()).language.ifEmpty { code.trim() }
