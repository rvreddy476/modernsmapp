package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The Action button's pure rules (contract 23 Oct 2026). The URL checks and
 * the error words are the web's (`features/action-button/model.ts`), so a
 * link refused on one client is refused, in the same words, on the other.
 */
class ActionButtonTest {

    private fun bad(url: String): UrlCheck.Bad = ActionButtons.checkUrl(url) as UrlCheck.Bad

    private fun ok(url: String): UrlCheck.Ok = ActionButtons.checkUrl(url) as UrlCheck.Ok

    // ---- URL checks ----

    @Test
    fun `a plain https link passes and names its domain without www`() {
        assertThat(ok("https://www.epfindia.gov.in/site_en/index.php")).isEqualTo(
            UrlCheck.Ok("https://www.epfindia.gov.in/site_en/index.php", "epfindia.gov.in"),
        )
        assertThat(ok("  https://example.com  ").url).isEqualTo("https://example.com")
        assertThat(ok("HTTPS://Example.COM/x").domain).isEqualTo("example.com")
        assertThat(ok("https://example.com:443/x").domain).isEqualTo("example.com")
    }

    @Test
    fun `every shortener is refused, and its subdomains`() {
        assertThat(ActionButtons.SHORTENER_HOSTS).hasSize(15)
        ActionButtons.SHORTENER_HOSTS.forEach { host ->
            val check = bad("https://$host/abc")
            assertThat(check.code).isEqualTo(ActionErrorCode.ACTION_LINK_SHORTENER)
            assertThat(check.message).isEqualTo("Short links like bit.ly hide where they go. Use the full link instead.")
            assertThat(bad("https://www.$host/abc").code).isEqualTo(ActionErrorCode.ACTION_LINK_SHORTENER)
            assertThat(bad("https://${host.uppercase()}./abc").code).isEqualTo(ActionErrorCode.ACTION_LINK_SHORTENER)
        }
        // A host that only ends in the same letters is not one.
        assertThat(ok("https://habit.ly/x").domain).isEqualTo("habit.ly")
        assertThat(ok("https://not-t.co/x").domain).isEqualTo("not-t.co")
    }

    @Test
    fun `a shortener hidden behind a browser's own parsing is still a shortener`() {
        listOf(
            "https:///bit.ly/abc",
            "https://bit.ly\\@example.com",
            "https://bit%2Ely/abc",
            "https://ｂｉｔ．ｌｙ/abc",
        ).forEach { assertThat(ActionButtons.checkUrl(it)).isInstanceOf(UrlCheck.Bad::class.java) }
        assertThat(bad("https:///bit.ly/abc").code).isEqualTo(ActionErrorCode.ACTION_LINK_SHORTENER)
        assertThat(bad("https://bit%2Ely/abc").code).isEqualTo(ActionErrorCode.ACTION_LINK_SHORTENER)
        assertThat(bad("https://ｂｉｔ．ｌｙ/abc").code).isEqualTo(ActionErrorCode.ACTION_LINK_SHORTENER)
    }

    @Test
    fun `every group invite form is refused`() {
        listOf(
            "https://chat.whatsapp.com/AbCdEf123",
            "https://www.chat.whatsapp.com/AbCdEf123",
            "https://t.me/+AbCdEf123",
            "https://t.me/%2BAbCdEf123",
            "https://t.me/joinchat/AbCdEf123",
            "https://t.me/JoinChat/AbCdEf123",
            "https://www.t.me/+AbCdEf123",
            "https://t.me/./+AbCdEf123",
            "https://telegram.me/joinchat/AbCdEf123",
            "https://www.telegram.me/joinchat/AbCdEf123",
        ).forEach { url ->
            val check = bad(url)
            assertThat(check.code).isEqualTo(ActionErrorCode.ACTION_LINK_GROUP_INVITE)
            assertThat(check.message)
                .isEqualTo("WhatsApp and Telegram group invites can't be used. A wa.me link to a number is fine.")
        }
    }

    @Test
    fun `wa me to a number and other telegram links are allowed`() {
        assertThat(ok("https://wa.me/919876543210").domain).isEqualTo("wa.me")
        assertThat(ok("https://wa.me/919876543210?text=Hi").domain).isEqualTo("wa.me")
        assertThat(ok("https://t.me/epfo_official").domain).isEqualTo("t.me")
        assertThat(ok("https://telegram.me/epfo_official").domain).isEqualTo("telegram.me")
        assertThat(ok("https://whatsapp.com/channel/abc").domain).isEqualTo("whatsapp.com")
    }

    @Test
    fun `the structural rules refuse in the web's words`() {
        val cases = mapOf(
            "" to "Add the link people should open.",
            "   " to "Add the link people should open.",
            "https://example.com/" + "a".repeat(500) to "A link can be at most 500 characters.",
            "https://exa mple.com" to "A link can't have spaces in it.",
            "https://example.com/\u0007" to "A link can't have spaces in it.",
            "http://example.com" to "Use a full link that starts with https://",
            "example.com" to "Use a full link that starts with https://",
            "javascript:alert(1)" to "Use a full link that starts with https://",
            "https://" to "That isn't a link we can open. Check it and try again.",
            "https://exa<mple.com" to "That isn't a link we can open. Check it and try again.",
            "https://example.com:99999" to "That isn't a link we can open. Check it and try again.",
            "https://user@example.com" to "Remove the name and @ before the website's address.",
            "https://user:pw@example.com" to "Remove the name and @ before the website's address.",
            "https://@example.com" to "Remove the name and @ before the website's address.",
            "https://127.0.0.1/x" to "Use the website's name, not a number address.",
            "https://2130706433/x" to "Use the website's name, not a number address.",
            "https://0x7f.1/x" to "Use the website's name, not a number address.",
            "https://[::1]/x" to "Use the website's name, not a number address.",
            "https://localhost/x" to "That link only works on your own computer.",
            "https://app.localhost/x" to "That link only works on your own computer.",
            "https://intranet/x" to "That link has no website in it.",
            "https://example.com:8443/x" to "Use a normal https:// link, without a port number.",
        )
        cases.forEach { (url, message) ->
            val check = ActionButtons.checkUrl(url)
            assertThat(check).isInstanceOf(UrlCheck.Bad::class.java)
            assertThat((check as UrlCheck.Bad).message).isEqualTo(message)
            assertThat(check.code).isEqualTo(ActionErrorCode.INVALID_ACTION_URL)
        }
        // 500 is allowed; 501 is not.
        val base = "https://example.com/"
        assertThat(ActionButtons.checkUrl(base + "a".repeat(500 - base.length))).isInstanceOf(UrlCheck.Ok::class.java)
        assertThat(ActionButtons.checkUrl(base + "a".repeat(501 - base.length))).isInstanceOf(UrlCheck.Bad::class.java)
    }

    @Test
    fun `our own domains are allowed`() {
        assertThat(ok("https://cleestudio.com/post/abc").domain).isEqualTo("cleestudio.com")
    }

    // ---- labels ----

    @Test
    fun `the label list falls back to the contract's and reads unknown values as words`() {
        assertThat(ActionButtons.LABELS.map { it.value }).containsExactly(
            "apply_now", "learn_more", "sign_up", "register", "book_now", "shop_now",
            "order_now", "download", "contact_us", "visit_website", "watch_more",
        ).inOrder()
        assertThat(ActionButtons.labelsFrom(emptyList())).isEqualTo(ActionButtons.LABELS)
        assertThat(ActionButtons.labelsFrom(listOf(null to "x", " " to "y"))).isEqualTo(ActionButtons.LABELS)
        assertThat(
            ActionButtons.labelsFrom(listOf("apply_now" to "Apply", "apply_now" to "Again", "book_now" to null, "new_one" to "")),
        ).containsExactly(
            ActionLabel("apply_now", "Apply"),
            ActionLabel("book_now", "Book now"),
            ActionLabel("new_one", "New one"),
        ).inOrder()
        assertThat(ActionButtons.labelText("apply_now")).isEqualTo("Apply now")
        assertThat(ActionButtons.labelText("join-the_club")).isEqualTo("Join the club")
    }

    // ---- the read shape ----

    @Test
    fun `the decoder needs an id and a label and never makes the raw url a link`() {
        assertThat(PostAction.fromWire(null, "apply_now", null, null, null, null, null)).isNull()
        assertThat(PostAction.fromWire("a1", " ", null, null, null, null, null)).isNull()
        val read = PostAction.fromWire("a1", "apply_now", "", "WWW.EPFIndia.gov.in", "/l/a1", "", null)!!
        assertThat(read).isEqualTo(
            PostAction("a1", "apply_now", "Apply now", "epfindia.gov.in", "/l/a1", ActionStatus.ACTIVE, null),
        )
        assertThat(read.pillText).isEqualTo("Apply now · epfindia.gov.in")
        // A server href that is not one of our /l/ paths is rebuilt from the id.
        assertThat(PostAction.fromWire("a 1", "apply_now", null, "x.in", "https://evil.example/", null, null)!!.href)
            .isEqualTo("/l/a%201")
        assertThat(PostAction.fromWire("a1", "apply_now", null, "x.in", "/l/a1?u=https://x", null, null)!!.href)
            .isEqualTo("/l/a1")
        // No domain field: the url's host, never a path.
        assertThat(PostAction.fromWire("a1", "book_now", "Book", null, null, null, "https://www.Shop.in/x")!!.domain)
            .isEqualTo("shop.in")
    }

    @Test
    fun `statuses fail closed`() {
        assertThat(ActionStatus.fromWire(null)).isEqualTo(ActionStatus.ACTIVE)
        assertThat(ActionStatus.fromWire("active")).isEqualTo(ActionStatus.ACTIVE)
        assertThat(ActionStatus.fromWire("blocked")).isEqualTo(ActionStatus.BLOCKED)
        assertThat(ActionStatus.fromWire("removed")).isEqualTo(ActionStatus.BLOCKED)
        assertThat(ActionStatus.fromWire("under_review")).isEqualTo(ActionStatus.UNDER_REVIEW)
        assertThat(ActionStatus.fromWire("pending_something")).isEqualTo(ActionStatus.UNDER_REVIEW)
    }

    @Test
    fun `a viewer only gets a live button and never its raw url`() {
        val live = PostAction("a1", "apply_now", "Apply now", "x.in", "/l/a1", ActionStatus.ACTIVE, "https://x.in/apply")
        val blocked = live.copy(status = ActionStatus.BLOCKED)
        val review = live.copy(status = ActionStatus.UNDER_REVIEW)
        assertThat(ActionButtons.visible(live, isAuthor = false)).isEqualTo(live.copy(url = null))
        assertThat(ActionButtons.visible(live, isAuthor = false)!!.url).isNull()
        assertThat(ActionButtons.visible(blocked, isAuthor = false)).isNull()
        assertThat(ActionButtons.visible(review, isAuthor = false)).isNull()
        // The author sees theirs whatever its status, url included.
        assertThat(ActionButtons.visible(blocked, isAuthor = true)).isEqualTo(blocked)
        assertThat(ActionButtons.visible(live, isAuthor = true)!!.url).isEqualTo("https://x.in/apply")
        // Locked (members-only): nobody.
        assertThat(ActionButtons.visible(live, isAuthor = false, locked = true)).isNull()
        assertThat(ActionButtons.visible(live, isAuthor = true, locked = true)).isNull()
        assertThat(ActionButtons.visible(null, isAuthor = true)).isNull()
    }

    @Test
    fun `the author is told why a button is not live`() {
        assertThat(ActionButtons.statusNote(ActionStatus.ACTIVE)).isNull()
        assertThat(ActionButtons.statusNote(ActionStatus.BLOCKED)!!.title).isEqualTo("Link blocked: unsafe")
        assertThat(ActionButtons.statusNote(ActionStatus.UNDER_REVIEW)!!.title).isEqualTo("Hidden while we review reports")
    }

    @Test
    fun `the server's reason picks the words, and a live button has none whatever the reason`() {
        assertThat(ActionButtons.statusNote(ActionStatus.BLOCKED, "unsafe")!!.title).isEqualTo("Link blocked: unsafe")
        assertThat(ActionButtons.statusNote(ActionStatus.BLOCKED, "moderator")!!.title).isEqualTo("Removed by our team")
        assertThat(ActionButtons.statusNote(ActionStatus.UNDER_REVIEW, "reports")!!.title).isEqualTo("Hidden while we review reports")
        assertThat(ActionButtons.statusNote(ActionStatus.BLOCKED, " Moderator ")!!.title).isEqualTo("Removed by our team")
        // A reason this build does not know: the status decides.
        assertThat(ActionButtons.statusNote(ActionStatus.BLOCKED, "new_reason")!!.title).isEqualTo("Link blocked: unsafe")
        assertThat(ActionButtons.statusNote(ActionStatus.ACTIVE, "unsafe")).isNull()
        val read = PostAction.fromWire("a1", "apply_now", null, "x.in", "/l/a1", "blocked", "https://x.in", statusReason = "MODERATOR")!!
        assertThat(read.statusReason).isEqualTo("moderator")
        assertThat(ActionButtons.statusNote(read)!!.title).isEqualTo("Removed by our team")
        assertThat(ActionButtons.statusNote(null as PostAction?)).isNull()
    }

    @Test
    fun `an account too new is told from which day it may add a button`() {
        val utc = java.time.ZoneOffset.UTC
        val problem = ActionButtons.problemOf(
            "ACTION_NOT_ELIGIBLE",
            listOf("account_age", "email_verified"),
            eligibleAt = "2026-10-30T09:00:00Z",
            zone = utc,
        )!!
        assertThat(problem.eligibleAt).isEqualTo("2026-10-30T09:00:00Z")
        assertThat(problem.message).isEqualTo(
            "You can't add an action button yet. You can add an action button from 30 Oct 2026. Verify your email address.",
        )
        assertThat(ActionButtons.missingSentences(problem.missing, problem.eligibleAt, utc))
            .containsExactly("You can add an action button from 30 Oct 2026.", "Verify your email address.").inOrder()
        // The date alone, with no list, still says it.
        assertThat(ActionButtons.problemOf("ACTION_NOT_ELIGIBLE", emptyList(), "2026-10-30T23:30:00-02:00", utc)!!.message)
            .isEqualTo("You can't add an action button yet. You can add an action button from 31 Oct 2026.")
        // Not a date: the plain sentence.
        assertThat(ActionButtons.problemOf("ACTION_NOT_ELIGIBLE", listOf("account_age"), "soon", utc)!!.message)
            .isEqualTo("You can't add an action button yet. Your account must be at least 7 days old.")
        // Only the eligibility refusal carries it.
        assertThat(ActionButtons.problemOf("ACTION_LINK_UNSAFE", emptyList(), "2026-10-30T09:00:00Z")!!.eligibleAt).isNull()
    }

    // ---- request builders ----

    @Test
    fun `the patch leaves the key off when nothing moved, removes with null, sends a change`() {
        val saved = ActionDraft("apply_now", "https://x.in/apply")
        assertThat(ActionButtons.patch(null, null)).isEqualTo(ActionPatch.Unchanged)
        assertThat(ActionButtons.patch(saved, saved)).isEqualTo(ActionPatch.Unchanged)
        assertThat(ActionButtons.patch(saved, saved.copy(url = "  https://x.in/apply "))).isEqualTo(ActionPatch.Unchanged)
        assertThat(ActionButtons.patch(saved, null)).isEqualTo(ActionPatch.Remove)
        assertThat(ActionButtons.patch(null, saved)).isEqualTo(ActionPatch.Set("apply_now", "https://x.in/apply"))
        assertThat(ActionButtons.patch(saved, saved.copy(label = "book_now")))
            .isEqualTo(ActionPatch.Set("book_now", "https://x.in/apply"))
        assertThat(ActionButtons.patch(saved, saved.copy(url = " https://x.in/new ")))
            .isEqualTo(ActionPatch.Set("apply_now", "https://x.in/new"))
    }

    @Test
    fun `the create sends the trimmed button only when there is one`() {
        assertThat(ActionButtons.createWrite(null)).isNull()
        assertThat(ActionButtons.createWrite(ActionDraft("apply_now", " https://x.in "))).isEqualTo(ActionDraft("apply_now", "https://x.in"))
    }

    @Test
    fun `a draft's problem is the label first, then the link`() {
        assertThat(ActionButtons.draftProblem(null)).isNull()
        assertThat(ActionButtons.draftProblem(ActionDraft("", "https://x.in"))).isEqualTo("Choose what the button says from the list.")
        assertThat(ActionButtons.draftProblem(ActionDraft("gone", "https://x.in"), ActionButtons.LABELS))
            .isEqualTo("Choose what the button says from the list.")
        assertThat(ActionButtons.draftProblem(ActionDraft("apply_now", "https://bit.ly/x")))
            .isEqualTo("Short links like bit.ly hide where they go. Use the full link instead.")
        assertThat(ActionButtons.draftProblem(ActionDraft("apply_now", "https://x.in"), ActionButtons.LABELS)).isNull()
        assertThat(ActionButtons.draftFrom(PostAction("a", "apply_now", "Apply now", "x.in", "/l/a", url = null)))
            .isEqualTo(ActionDraft("apply_now", ""))
    }

    // ---- errors ----

    @Test
    fun `each code reads as one sentence`() {
        val words = ActionErrorCode.entries.associateWith { ActionButtons.problemOf(it.name)!!.message }
        assertThat(words).containsExactly(
            ActionErrorCode.INVALID_ACTION_LABEL, "Choose what the button says from the list.",
            ActionErrorCode.INVALID_ACTION_URL, "Use a full https:// link to a website.",
            ActionErrorCode.ACTION_LINK_SHORTENER, "Short links like bit.ly hide where they go. Use the full link instead.",
            ActionErrorCode.ACTION_LINK_GROUP_INVITE,
            "WhatsApp and Telegram group invites can't be used. A wa.me link to a number is fine.",
            ActionErrorCode.ACTION_LINK_UNSAFE, "That link was flagged as unsafe, so it can't be used.",
            ActionErrorCode.ACTION_LINK_CHECK_UNAVAILABLE, "We couldn't check that link right now. Try again in a few minutes.",
            ActionErrorCode.ACTION_NOT_ELIGIBLE,
            "You can't add an action button yet. Accounts need to be 7 days old, with a verified email and no active strikes.",
        )
        assertThat(ActionButtons.problemOf("POST_NOT_FOUND")).isNull()
        assertThat(ActionButtons.problemOf(null)).isNull()
    }

    @Test
    fun `not eligible names each missing item once, in the server's order`() {
        val problem = ActionButtons.problemOf(
            "ACTION_NOT_ELIGIBLE",
            listOf("email_verified", "account_age", "no_active_strikes", "account_age", " "),
        )!!
        assertThat(problem.missing).containsExactly("email_verified", "account_age", "no_active_strikes", "account_age").inOrder()
        assertThat(ActionButtons.missingSentences(problem.missing)).containsExactly(
            "Verify your email address.",
            "Your account must be at least 7 days old.",
            "You can't have an active strike on your account.",
        ).inOrder()
        assertThat(problem.message).isEqualTo(
            "You can't add an action button yet. Verify your email address. Your account must be at least 7 days old. " +
                "You can't have an active strike on your account.",
        )
        assertThat(ActionButtons.missingSentences(listOf("phone_verified"))).containsExactly("Phone verified.")
    }

    // ---- links and the leaving page ----

    @Test
    fun `a link target is only an active https link, named by its real host`() {
        assertThat(ActionLinks.targetOf("l1", "https://www.EPFindia.gov.in/a", "apply_now", "active"))
            .isEqualTo(ActionLinks.Target("l1", "https://www.EPFindia.gov.in/a", "epfindia.gov.in", "apply_now"))
        assertThat(ActionLinks.targetOf("l1", "https://x.in", null, null)!!.domain).isEqualTo("x.in")
        assertThat(ActionLinks.targetOf("l1", "http://x.in", null, "active")).isNull()
        assertThat(ActionLinks.targetOf("l1", "javascript:alert(1)", null, "active")).isNull()
        assertThat(ActionLinks.targetOf("l1", "intent://x#Intent;end", null, "active")).isNull()
        assertThat(ActionLinks.targetOf("", "https://x.in", null, "active")).isNull()
        assertThat(ActionLinks.targetOf("l1", "https://x.in", null, "blocked")).isNull()
        assertThat(ActionLinks.targetOf("l1", "https://x.in", null, "under_review")).isNull()
    }

    @Test
    fun `the leaving page shows once per domain`() {
        val target = ActionLinks.targetOf("l1", "https://www.x.in/a", null, "active")!!
        assertThat(ActionLinks.decide(target, emptyList())).isEqualTo(ActionLinks.Visit.Interstitial("https://www.x.in/a", "x.in"))
        val seen = ActionLinks.remember(emptyList(), "WWW.X.IN.")
        assertThat(seen).containsExactly("x.in")
        assertThat(ActionLinks.decide(target, seen)).isEqualTo(ActionLinks.Visit.Open("https://www.x.in/a"))
        // Another domain still asks.
        val other = ActionLinks.targetOf("l2", "https://y.in/", null, "active")!!
        assertThat(ActionLinks.decide(other, seen)).isInstanceOf(ActionLinks.Visit.Interstitial::class.java)
        assertThat(ActionLinks.hasSeen(seen, "")).isFalse()
        assertThat(ActionLinks.remember(seen, " ")).isEqualTo(seen)
    }

    @Test
    fun `the memory keeps the newest 500 and moves a repeat to the end`() {
        val full = (1..500).map { "d$it.in" }
        val next = ActionLinks.remember(full, "new.in")
        assertThat(next).hasSize(500)
        assertThat(next.first()).isEqualTo("d2.in")
        assertThat(next.last()).isEqualTo("new.in")
        val repeat = ActionLinks.remember(full, "d1.in")
        assertThat(repeat).hasSize(500)
        assertThat(repeat.last()).isEqualTo("d1.in")
        assertThat(repeat.count { it == "d1.in" }).isEqualTo(1)
    }

    @Test
    fun `taps read in Indian grouping`() {
        assertThat(ActionStats(1, 0).tapsText).isEqualTo("1 tap")
        assertThat(ActionStats(0, 0).tapsText).isEqualTo("0 taps")
        assertThat(ActionStats(123_456, 12).tapsText).isEqualTo("1,23,456 taps · 12 this week")
        assertThat(ActionStats(1_234, 0).tapsText).isEqualTo("1,234 taps")
        assertThat(ActionStats(12_345_678, 0).tapsText).isEqualTo("1,23,45,678 taps")
    }
}
