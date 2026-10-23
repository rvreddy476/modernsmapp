package com.us.android.core.feed.data.credits

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.CreditStatus
import com.us.android.core.model.CreditedUser
import com.us.android.core.model.VideoCredit
import com.us.android.core.model.VideoCredits
import org.junit.Test

/** The Credits form's rules (contract pinned 2026-10-23): limits, order, checks, the wire, and how the watch page reads it. */
class CreditsFormTest {

    private fun credit(name: String, role: String, user: CreditedUser? = null) =
        VideoCredit(id = name, name = name, role = role, user = user)

    // ── Where it is offered ─────────────────────────────────────────────

    @Test
    fun `credits are offered for the contract's categories only`() {
        listOf("film-animation", "entertainment", "comedy", "music", "documentary", "podcasts", " Music ").forEach {
            assertThat(creditsOffered(it)).isTrue()
        }
        listOf("", "gaming", "education", "kids", "other", "tech").forEach {
            assertThat(creditsOffered(it)).isFalse()
        }
    }

    @Test
    fun `the suggested roles are the contract's twelve, in order`() {
        assertThat(SuggestedCreditRoles).containsExactly(
            "Actor", "Director", "Writer", "Producer", "Music", "Singer",
            "Cinematographer", "Editor", "Choreographer", "Host", "Guest", "Narrator",
        ).inOrder()
    }

    // ── Limits ──────────────────────────────────────────────────────────

    @Test
    fun `no thirty-first credit`() {
        val full = (0 until CreditLimits.MAX_CREDITS).fold(CreditsForm()) { form, i -> form.withNewEntry("k$i") }

        assertThat(full.entries).hasSize(30)
        assertThat(full.canAddCredit).isFalse()
        assertThat(full.withNewEntry("extra").entries).hasSize(30)
    }

    @Test
    fun `no fourth genre, and a picked genre toggles off`() {
        val three = CreditsForm().toggledGenre("drama").toggledGenre("comedy").toggledGenre("action")

        assertThat(three.toggledGenre("horror").genres).containsExactly("drama", "comedy", "action").inOrder()
        assertThat(three.toggledGenre("comedy").genres).containsExactly("drama", "action").inOrder()
    }

    @Test
    fun `names stop at 80 characters, roles at 40, the house at 100, counted in code points`() {
        val emoji = "🎬" // one code point, two UTF-16 units
        val form = CreditsForm().withNewEntry("a")
            .withName("a", emoji.repeat(90))
            .withRole("a", "r".repeat(50))
            .withProduction("p".repeat(120))

        val entry = form.entries.single()
        assertThat(entry.name.codePointCount(0, entry.name.length)).isEqualTo(80)
        assertThat(entry.role).hasLength(40)
        assertThat(form.productionHouse).hasLength(100)
    }

    @Test
    fun `control characters are dropped as typed`() {
        val form = CreditsForm().withNewEntry("a").withName("a", "Asha\tRao\n")

        assertThat(form.entries.single().name).isEqualTo("AshaRao")
    }

    @Test
    fun `the year takes four digits and nothing else`() {
        assertThat(CreditsForm().withYear("20a24x9").releaseYear).isEqualTo("2024")
    }

    // ── Order ───────────────────────────────────────────────────────────

    @Test
    fun `move up and down stop at either end, and remove takes the row`() {
        val form = CreditsForm(entries = listOf(CreditEntry("a"), CreditEntry("b"), CreditEntry("c")))

        assertThat(form.moved("c", -1).entries.map { it.key }).containsExactly("a", "c", "b").inOrder()
        assertThat(form.moved("a", 1).entries.map { it.key }).containsExactly("b", "a", "c").inOrder()
        assertThat(form.moved("a", -1)).isEqualTo(form)
        assertThat(form.moved("c", 1)).isEqualTo(form)
        assertThat(form.without("b").entries.map { it.key }).containsExactly("a", "c").inOrder()
    }

    // ── Links ───────────────────────────────────────────────────────────

    @Test
    fun `linking fills a blank name, and a different person drops the old status`() {
        val form = CreditsForm(entries = listOf(CreditEntry("a", role = "Actor")))
        val asha = CreditPerson("u1", "Asha Rao", "asha")

        val linked = form.withPerson("a", asha)
        assertThat(linked.entries.single().name).isEqualTo("Asha Rao")

        val saved = CreditsForm(entries = listOf(CreditEntry("a", "Asha", "Actor", asha, CreditStatus.ACCEPTED)))
        assertThat(saved.withPerson("a", asha).entries.single().status).isEqualTo(CreditStatus.ACCEPTED)
        assertThat(saved.withPerson("a", CreditPerson("u9", "Other")).entries.single().status).isNull()
        assertThat(saved.withPerson("a", CreditPerson("u9", "Other")).entries.single().name).isEqualTo("Asha")
        assertThat(saved.withoutPerson("a").entries.single().person).isNull()
        assertThat(saved.withoutPerson("a").entries.single().status).isNull()
    }

    // ── Checks and wire ─────────────────────────────────────────────────

    @Test
    fun `a blank row is left off, a half-filled row is refused`() {
        val form = CreditsForm(
            entries = listOf(CreditEntry("a", "Asha", "Director"), CreditEntry("b"), CreditEntry("c", "Ravi", " ")),
        )

        assertThat(creditsProblems(form, currentYear = 2026)).containsExactly("Every credit needs a role.")
        assertThat(form.filled.map { it.key }).containsExactly("a", "c").inOrder()
    }

    @Test
    fun `the year must be 1900 to next year`() {
        fun problems(year: String) = creditsProblems(CreditsForm(releaseYear = year), currentYear = 2026)

        assertThat(problems("1900")).isEmpty()
        assertThat(problems("2027")).isEmpty()
        assertThat(problems("")).isEmpty()
        assertThat(problems("1899")).containsExactly("The release year must be between 1900 and 2027.")
        assertThat(problems("2028")).hasSize(1)
    }

    @Test
    fun `the wire carries names and roles trimmed, in order, with user_id only for a link`() {
        val form = CreditsForm(
            entries = listOf(
                CreditEntry("a", " Asha ", " Director ", CreditPerson("u1", "Asha")),
                CreditEntry("b"),
                CreditEntry("c", "Crew", "Editor"),
            ),
        )

        assertThat(form.toInput()).containsExactly(
            CreditInputDto("Asha", "Director", "u1"),
            CreditInputDto("Crew", "Editor", null),
        ).inOrder()
    }

    // ── Reading: the watch page ─────────────────────────────────────────

    @Test
    fun `credits group by role in first-seen order, and Actor reads as Cast`() {
        val groups = creditGroups(
            listOf(
                credit("A", "Actor"),
                credit("D", "Director"),
                credit("B", "actor "),
                credit("M", "Music"),
            ),
        )

        assertThat(groups.map { it.label }).containsExactly("Cast", "Director", "Music").inOrder()
        assertThat(groups[0].credits.map { it.name }).containsExactly("A", "B").inOrder()
        assertThat(creditsLine(groups)).isEqualTo("Cast: A, B · Director: D · Music: M")
    }

    @Test
    fun `a role keeps its first spelling and a blank role reads as Other`() {
        val groups = creditGroups(listOf(credit("A", "Host"), credit("B", "HOST"), credit("C", "")))

        assertThat(groups.map { it.label }).containsExactly("Host", "Other").inOrder()
        assertThat(roleLabel(" actor ")).isEqualTo("Cast")
        assertThat(roleLabel("Director")).isEqualTo("Director")
    }

    @Test
    fun `the facts line joins genres, year and production house, labels first`() {
        val credits = VideoCredits(genres = listOf("drama", "sci-fi"), releaseYear = 2024, productionHouse = "Blue Hour Films")

        assertThat(creditFactsLine(credits) { if (it == "drama") "Drama" else null })
            .isEqualTo("Drama, Sci fi · 2024 · Blue Hour Films")
        assertThat(creditFactsLine(VideoCredits(releaseYear = 2024))).isEqualTo("2024")
        assertThat(creditFactsLine(VideoCredits.None)).isEmpty()
    }

    @Test
    fun `the upload row summarises people and the first genre`() {
        val form = CreditsForm(entries = listOf(CreditEntry("a", "Asha", "Director"), CreditEntry("b")), genres = listOf("drama"))

        assertThat(creditsSummary(form) { "Drama" }).isEqualTo("1 person · Drama")
        assertThat(creditsSummary(CreditsForm())).isEmpty()
    }
}
