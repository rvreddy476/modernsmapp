package com.us.android.core.feed.data.credits

import com.us.android.core.model.CreditStatus
import com.us.android.core.model.VideoCredit
import com.us.android.core.model.VideoCredits

/*
 * Video credits as the creator edits them (contract pinned 2026-10-23): the
 * upload's Credits step and the owner's Edit sheet share this one form, its
 * limits, its checks and its wire shape. Pure, so the rules are table tests.
 */

/** The contract's limits, mirrored so the editor stops where the server would refuse. */
object CreditLimits {
    const val MAX_CREDITS = 30
    const val MAX_GENRES = 3
    const val NAME_MAX = 80
    const val ROLE_MAX = 40
    const val PRODUCTION_MAX = 100
    const val FIRST_YEAR = 1900
    const val YEAR_DIGITS = 4
}

/** The roles the editor suggests; the server accepts any 1..40 characters. */
val SuggestedCreditRoles: List<String> = listOf(
    "Actor", "Director", "Writer", "Producer", "Music", "Singer",
    "Cinematographer", "Editor", "Choreographer", "Host", "Guest", "Narrator",
)

/**
 * The long-video categories the Credits step is offered for — post-service's
 * `categories.go` slugs for film & animation, entertainment, comedy, music,
 * documentary and podcasts. There is no series slug today. The server accepts
 * credits on any long video; which categories ASK for them is the client's call.
 */
val CreditCategories: Set<String> = setOf("film-animation", "entertainment", "comedy", "music", "documentary", "podcasts")

/** Whether a long video in [category] is offered the Credits step. */
fun creditsOffered(category: String): Boolean = category.trim().lowercase() in CreditCategories

/** A VChat account a credit can be linked to — what the people suggester answers. */
data class CreditPerson(
    val id: String,
    val name: String,
    val username: String = "",
    val avatarUrl: String? = null,
)

/**
 * One row of the editor. [key] is local (rows have no server id until saved,
 * and two rows may carry the same name); [status] is the server's, for a row
 * that came from a saved video, and is cleared when the link is changed.
 */
data class CreditEntry(
    val key: String,
    val name: String = "",
    val role: String = "",
    val person: CreditPerson? = null,
    val status: CreditStatus? = null,
) {
    /** Nothing typed and nobody linked: left off the wire rather than refused. */
    val isBlank: Boolean get() = name.isBlank() && role.isBlank() && person == null
}

/** The whole Credits section: people, genres, year and production house, as typed. */
data class CreditsForm(
    val entries: List<CreditEntry> = emptyList(),
    /** Genre slugs, in the order picked. */
    val genres: List<String> = emptyList(),
    /** Digits as typed; blank is "no year". */
    val releaseYear: String = "",
    val productionHouse: String = "",
) {
    val canAddCredit: Boolean get() = entries.size < CreditLimits.MAX_CREDITS
    val canAddGenre: Boolean get() = genres.size < CreditLimits.MAX_GENRES

    /** The rows that will be sent. */
    val filled: List<CreditEntry> get() = entries.filterNot { it.isBlank }

    val isEmpty: Boolean
        get() = filled.isEmpty() && genres.isEmpty() && releaseYear.isBlank() && productionHouse.isBlank()
}

// ── Edits ───────────────────────────────────────────────────────────────

/** A new empty row at the end, unless the list is full. */
fun CreditsForm.withNewEntry(key: String): CreditsForm =
    if (!canAddCredit) this else copy(entries = entries + CreditEntry(key = key))

fun CreditsForm.withName(key: String, name: String): CreditsForm =
    update(key) { it.copy(name = name.withoutControls().takeCodePoints(CreditLimits.NAME_MAX)) }

fun CreditsForm.withRole(key: String, role: String): CreditsForm =
    update(key) { it.copy(role = role.withoutControls().takeCodePoints(CreditLimits.ROLE_MAX)) }

/**
 * Links [key] to [person]. A blank name takes the person's; a different
 * person is a NEW tag, so the old status no longer applies.
 */
fun CreditsForm.withPerson(key: String, person: CreditPerson): CreditsForm = update(key) { entry ->
    entry.copy(
        name = entry.name.ifBlank { person.name.takeCodePoints(CreditLimits.NAME_MAX) },
        person = person,
        status = if (entry.person?.id == person.id) entry.status else null,
    )
}

/** The row stays, as plain text. */
fun CreditsForm.withoutPerson(key: String): CreditsForm = update(key) { it.copy(person = null, status = null) }

fun CreditsForm.without(key: String): CreditsForm = copy(entries = entries.filterNot { it.key == key })

/** Moves [key] by [delta] places (−1 up, +1 down), stopping at either end. */
fun CreditsForm.moved(key: String, delta: Int): CreditsForm {
    val from = entries.indexOfFirst { it.key == key }
    if (from < 0) return this
    val to = (from + delta).coerceIn(0, entries.lastIndex)
    if (to == from) return this
    val list = entries.toMutableList()
    list.add(to, list.removeAt(from))
    return copy(entries = list)
}

/** On when off; off when on; nothing when three are already picked. */
fun CreditsForm.toggledGenre(slug: String): CreditsForm = when {
    slug in genres -> copy(genres = genres - slug)
    !canAddGenre -> this
    else -> copy(genres = genres + slug)
}

/** Digits only, four at most. */
fun CreditsForm.withYear(typed: String): CreditsForm =
    copy(releaseYear = typed.filter(Char::isDigit).take(CreditLimits.YEAR_DIGITS))

fun CreditsForm.withProduction(typed: String): CreditsForm =
    copy(productionHouse = typed.withoutControls().takeCodePoints(CreditLimits.PRODUCTION_MAX))

private fun CreditsForm.update(key: String, change: (CreditEntry) -> CreditEntry): CreditsForm =
    copy(entries = entries.map { if (it.key == key) change(it) else it })

// ── Checks ──────────────────────────────────────────────────────────────

/**
 * What stops a save, in the order the form reads — the contract's rules,
 * checked before the round trip; the server's 400s are the backstop.
 */
fun creditsProblems(form: CreditsForm, currentYear: Int): List<String> = buildList {
    val filled = form.filled
    if (filled.size > CreditLimits.MAX_CREDITS) add("A video can credit up to ${CreditLimits.MAX_CREDITS} people.")
    if (filled.any { it.name.isBlank() }) add("Every credit needs a name.")
    if (filled.any { it.role.isBlank() }) add("Every credit needs a role.")
    if (form.genres.size > CreditLimits.MAX_GENRES) add("Pick up to ${CreditLimits.MAX_GENRES} genres.")
    val year = form.releaseYear.trim()
    if (year.isNotEmpty()) {
        val last = currentYear + 1
        val parsed = year.toIntOrNull()
        if (parsed == null || parsed < CreditLimits.FIRST_YEAR || parsed > last) {
            add("The release year must be between ${CreditLimits.FIRST_YEAR} and $last.")
        }
    }
}

// ── Refusals ────────────────────────────────────────────────────────────

/**
 * post-service's 400 codes for the credits fields (create and PATCH alike)
 * → what the Credits section says. Never the code, and never the server's
 * message, which names routes and slugs. `INVALID_GENRE` is also the answer
 * to more than three genres.
 */
val CreditRefusals: Map<String, String> = mapOf(
    "CREDITS_NOT_ALLOWED" to "Credits are for long videos only.",
    "TOO_MANY_CREDITS" to "A video can credit up to ${CreditLimits.MAX_CREDITS} people.",
    "INVALID_CREDIT" to
        "Every credit needs a name (up to ${CreditLimits.NAME_MAX} characters) and a role (up to ${CreditLimits.ROLE_MAX}).",
    "INVALID_GENRE" to "Pick up to ${CreditLimits.MAX_GENRES} genres from the list.",
    "INVALID_RELEASE_YEAR" to "The release year must be between ${CreditLimits.FIRST_YEAR} and next year.",
    "INVALID_PRODUCTION_HOUSE" to "The production house can be up to ${CreditLimits.PRODUCTION_MAX} characters.",
)

/** The Credits section's sentence for a refusal [code], or null when the code is not a credits one. */
fun creditRefusal(code: String?): String? = code?.let { CreditRefusals[it] }

// ── Wire ────────────────────────────────────────────────────────────────

/** The credits as the create request and the PATCH send them, in the creator's order. */
fun CreditsForm.toInput(): List<CreditInputDto> = filled.map {
    CreditInputDto(name = it.name.trim(), role = it.role.trim(), userId = it.person?.id?.takeIf { id -> id.isNotBlank() })
}

/** The year as a number, or null when none was typed. */
fun CreditsForm.releaseYearOrNull(): Int? = releaseYear.trim().toIntOrNull()

/**
 * A saved video's credits, as the owner's editor opens them. A link the
 * owner may see but that is not accepted yet keeps its user id, so a save
 * sends the same tag back and the server keeps its status.
 */
fun VideoCredits.toForm(keyFor: (Int) -> String): CreditsForm = CreditsForm(
    entries = credits.mapIndexed { index, credit -> credit.toEntry(keyFor(index)) },
    genres = genres,
    releaseYear = releaseYear?.toString().orEmpty(),
    productionHouse = productionHouse,
)

private fun VideoCredit.toEntry(key: String) = CreditEntry(
    key = key,
    name = name,
    role = role,
    person = user?.let { CreditPerson(id = it.id, name = it.displayName, username = it.username, avatarUrl = it.avatarUrl) }
        ?: linkedUserId?.let { CreditPerson(id = it, name = name) },
    status = status,
)

// ── Reading ─────────────────────────────────────────────────────────────

/** One role's people, as the watch page lists them: "Cast: A, B". */
data class CreditGroup(val label: String, val credits: List<VideoCredit>)

/**
 * The credits grouped by role, in the order each role first appears;
 * "Actor" is shown as "Cast". Roles group without regard to case or the
 * spaces around them, under the spelling that came first.
 */
fun creditGroups(credits: List<VideoCredit>): List<CreditGroup> {
    val order = LinkedHashMap<String, MutableList<VideoCredit>>()
    val labels = HashMap<String, String>()
    credits.filter { it.name.isNotBlank() }.forEach { credit ->
        val role = credit.role.trim().ifEmpty { OTHER_ROLE }
        val key = role.lowercase()
        labels.getOrPut(key) { roleLabel(role) }
        order.getOrPut(key) { mutableListOf() }.add(credit)
    }
    return order.map { (key, list) -> CreditGroup(label = labels.getValue(key), credits = list) }
}

/** What a role is called on the watch page: Actor is "Cast", everything else as written. */
fun roleLabel(role: String): String = if (role.trim().equals(ACTOR_ROLE, ignoreCase = true)) CAST_LABEL else role.trim()

/** "Cast: A, B · Director: D" — the groups on one line. */
fun creditsLine(groups: List<CreditGroup>): String =
    groups.joinToString(" · ") { group -> "${group.label}: ${group.credits.joinToString(", ") { it.name }}" }

/**
 * "Drama, Thriller · 2024 · Blue Hour Films" — the facts line above the
 * people. [genreLabel] turns a slug into its label (the server's list, when
 * it has loaded); an unknown slug is shown capitalised.
 */
fun creditFactsLine(credits: VideoCredits, genreLabel: (String) -> String? = { null }): String = listOfNotNull(
    credits.genres.takeIf { it.isNotEmpty() }?.joinToString(", ") { slug ->
        genreLabel(slug) ?: slug.replace('-', ' ').replaceFirstChar(Char::uppercaseChar)
    },
    credits.releaseYear?.toString(),
    credits.productionHouse.trim().takeIf { it.isNotEmpty() },
).joinToString(" · ")

/** What the upload's Credits row says beside its title: "3 people · Drama", or nothing. */
fun creditsSummary(form: CreditsForm, genreLabel: (String) -> String? = { null }): String = listOfNotNull(
    form.filled.size.takeIf { it > 0 }?.let { if (it == 1) "1 person" else "$it people" },
    form.genres.firstOrNull()?.let { genreLabel(it) ?: it.replaceFirstChar(Char::uppercaseChar) },
).joinToString(" · ")

private const val ACTOR_ROLE = "Actor"
private const val CAST_LABEL = "Cast"
private const val OTHER_ROLE = "Other"

// ── Text ────────────────────────────────────────────────────────────────

/** The first [max] code points: the server counts runes, not UTF-16 units. */
internal fun String.takeCodePoints(max: Int): String {
    if (codePointCount(0, length) <= max) return this
    return substring(0, offsetByCodePoints(0, max))
}

/** Control characters (a pasted tab or line break) are refused by the server; dropped as typed. */
internal fun String.withoutControls(): String = filterNot { it.isISOControl() }
