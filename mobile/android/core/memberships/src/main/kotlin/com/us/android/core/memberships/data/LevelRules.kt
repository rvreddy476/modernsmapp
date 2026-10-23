package com.us.android.core.memberships.data

/**
 * The level rules, exactly as monetization-service enforces them
 * (service/membership.go: `normaliseTierName`, `validateTierPrice`,
 * `normaliseTierPerks`, `MaxActiveTiers`), so the form refuses here, in
 * words, what the server would refuse with a code.
 *
 *  - name: trimmed, 1 to 60 characters (counted as the server counts runes);
 *  - price: ₹29 to ₹2,999 a month (2,900 to 2,99,900 paise), GST included;
 *  - perks: blank lines dropped, at most 10, each at most 100 characters;
 *  - at most 3 ACTIVE levels.
 */
object LevelRules {
    const val MIN_PRICE_PAISE = 2_900L
    const val MAX_PRICE_PAISE = 299_900L
    const val MAX_NAME_LENGTH = 60
    const val MAX_PERKS = 10
    const val MAX_PERK_LENGTH = 100
    const val MAX_ACTIVE_LEVELS = 3

    /** Characters as Go's `utf8.RuneCountInString` counts them: code points, not UTF-16 units. */
    fun length(text: String): Int = text.codePointCount(0, text.length)

    fun nameProblem(name: String): String? {
        val n = length(name.trim())
        return when {
            n < 1 -> "Give the level a name."
            n > MAX_NAME_LENGTH -> "Keep the name to $MAX_NAME_LENGTH characters."
            else -> null
        }
    }

    /**
     * Whole rupees as typed ("99", "₹1,499", " 2999 ") to paise; null for
     * anything that is not a whole number of rupees.
     */
    fun parseRupees(text: String): Long? {
        val digits = text.trim().removePrefix("₹").replace(",", "").trim()
        if (digits.isEmpty() || digits.length > MAX_RUPEE_DIGITS || !digits.all(Char::isDigit)) return null
        return digits.toLong() * PAISE_PER_RUPEE
    }

    fun priceProblem(pricePaise: Long?): String? = when {
        pricePaise == null -> "Enter a price in whole rupees."
        pricePaise < MIN_PRICE_PAISE || pricePaise > MAX_PRICE_PAISE ->
            "Price must be between ${rupees(MIN_PRICE_PAISE)} and ${rupees(MAX_PRICE_PAISE)} a month."
        else -> null
    }

    /** The perks the server would store: trimmed, blanks dropped, in order. */
    fun normalisePerks(perks: List<String>): List<String> = perks.map { it.trim() }.filter { it.isNotEmpty() }

    fun perksProblem(perks: List<String>): String? {
        val kept = normalisePerks(perks)
        return when {
            kept.size > MAX_PERKS -> "Up to $MAX_PERKS perks."
            kept.any { length(it) > MAX_PERK_LENGTH } -> "Keep each perk to $MAX_PERK_LENGTH characters."
            else -> null
        }
    }

    /**
     * Whether one more ACTIVE level may be added (a new level, or turning an
     * inactive one back on) given the creator's current levels.
     */
    fun canAddActive(levels: List<MembershipLevel>): Boolean = levels.count { it.isActive } < MAX_ACTIVE_LEVELS

    /** Every problem with a level as typed, in the form's order; empty when the server would take it. */
    fun problems(name: String, priceText: String, perks: List<String>): List<String> =
        listOfNotNull(nameProblem(name), priceProblem(parseRupees(priceText)), perksProblem(perks))

    private const val PAISE_PER_RUPEE = 100L

    /** Enough digits for any legal price and then some, without a Long overflow. */
    private const val MAX_RUPEE_DIGITS = 9
}
