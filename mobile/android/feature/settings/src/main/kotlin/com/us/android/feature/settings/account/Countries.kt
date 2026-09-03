package com.us.android.feature.settings.account

import com.us.android.core.ui.UsSettingsOption

/**
 * A bundled ISO 3166-1 alpha-2 country list for the region picker.
 *
 * Not exhaustive — there is no server endpoint to fetch the full list from,
 * and `PUT /v1/users/me/region` validates the code server-side regardless
 * (`400 INVALID_REGION`), so a client omission here is a missing menu entry,
 * not a way to send an invalid code.
 */
object Countries {
    val all: List<UsSettingsOption> = listOf(
        "IN" to "India",
        "US" to "United States",
        "GB" to "United Kingdom",
        "SG" to "Singapore",
        "AE" to "United Arab Emirates",
        "AU" to "Australia",
        "CA" to "Canada",
        "DE" to "Germany",
        "FR" to "France",
        "AR" to "Argentina",
        "AT" to "Austria",
        "BD" to "Bangladesh",
        "BE" to "Belgium",
        "BR" to "Brazil",
        "CH" to "Switzerland",
        "CL" to "Chile",
        "CN" to "China",
        "CO" to "Colombia",
        "CZ" to "Czechia",
        "DK" to "Denmark",
        "EG" to "Egypt",
        "ES" to "Spain",
        "FI" to "Finland",
        "GH" to "Ghana",
        "GR" to "Greece",
        "HK" to "Hong Kong",
        "HU" to "Hungary",
        "ID" to "Indonesia",
        "IE" to "Ireland",
        "IL" to "Israel",
        "IT" to "Italy",
        "JP" to "Japan",
        "KE" to "Kenya",
        "KR" to "South Korea",
        "LK" to "Sri Lanka",
        "MX" to "Mexico",
        "MY" to "Malaysia",
        "NG" to "Nigeria",
        "NL" to "Netherlands",
        "NO" to "Norway",
        "NP" to "Nepal",
        "NZ" to "New Zealand",
        "PH" to "Philippines",
        "PK" to "Pakistan",
        "PL" to "Poland",
        "PT" to "Portugal",
        "QA" to "Qatar",
        "RO" to "Romania",
        "RU" to "Russia",
        "SA" to "Saudi Arabia",
        "SE" to "Sweden",
        "TH" to "Thailand",
        "TR" to "Turkey",
        "TW" to "Taiwan",
        "UA" to "Ukraine",
        "VN" to "Vietnam",
        "ZA" to "South Africa",
        "MA" to "Morocco",
        "BH" to "Bahrain",
        "KW" to "Kuwait",
        "OM" to "Oman",
    ).map { (code, name) -> UsSettingsOption(code, name) }

    fun label(code: String): String = all.firstOrNull { it.value == code }?.label ?: code
}
