package com.us.android.core.memberships

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.data.EligibilityItem
import com.us.android.core.memberships.data.LevelChange
import com.us.android.core.memberships.data.MembershipStatus
import com.us.android.core.memberships.data.MembershipsApi
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.membershipCode
import com.us.android.core.memberships.data.membershipRefusal
import com.us.android.core.memberships.data.notEligibleItems
import com.us.android.core.memberships.payment.toReading
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.payments.PaymentStatusReading
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonObject
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.File

/**
 * Paid memberships' wire, both ways, through the real Retrofit stack and the
 * platform `Json` (contract pinned 2026-10-23).
 *
 * There are no golden captures for these routes yet, so the fixtures in
 * `src/test/resources/contracts/memberships` are written from the Go
 * handlers, each shape from the struct it mirrors (paths under
 * `Architecture/services/monetization-service/internal/`):
 *
 *  - `tiers_*.json` — `postgres.CreatorTier` (store/postgres/store.go:144), the
 *    `perks` raw message as `normaliseTierPerks` writes it (service/membership.go);
 *  - `eligibility_*.json` — `service.Eligibility` (service/membership_payments.go:572);
 *  - `checkout_*.json`, `upgrade_checkout.json` — `service.Checkout`
 *    (service/membership_payments.go:88) with `payments.ClientSession`
 *    (payments/client.go:69); the paid replay per `Subscribe` (service/membership.go:333);
 *  - `upgrade_scheduled.json`, `upgrade_upgraded.json` — `service.TierChange`
 *    (service/subscription.go:169);
 *  - `payment_*.json` — `service.PaymentView` (service/membership_payments.go:156);
 *  - `my_memberships.json` — `service.MembershipView` (service/membership_payments.go:462);
 *  - `error_*.json` — `writeMembershipError` (http/membership_payments_handler.go:117);
 *  - `members_*.json` — `service.MemberView` (service/membership_reads.go:254), paged by `meta.next_cursor`.
 */
class MembershipsContractTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repository: MembershipsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(MembershipsApi::class.java)
        repository = MembershipsRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun fixture(name: String) = File("src/test/resources/contracts/memberships/$name").readText()

    private fun answer(name: String, code: Int = 200) {
        server.enqueue(MockResponse.Builder().code(code).body(fixture(name)).build())
    }

    private fun bodyOf(): JsonObject = json.parseToJsonElement(server.takeRequest().body!!.utf8()).jsonObject

    // ── Reads ────────────────────────────────────────────────────────────

    @Test
    fun `the creator's levels decode with their perks, and a null perks is none`() {
        answer("tiers_mine.json")
        val levels = (runBlocking { repository.myLevels() } as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/tiers")
        assertThat(levels).hasSize(2)
        assertThat(levels[0].name).isEqualTo("Supporter")
        assertThat(levels[0].pricePaise).isEqualTo(9_900)
        assertThat(levels[0].priceLabel).isEqualTo("₹99 a month")
        assertThat(
            levels[0].perks
        ).containsExactly("Early access to videos", "A members-only post every week").inOrder()
        assertThat(levels[0].memberCount).isEqualTo(12)
        assertThat(levels[1].isActive).isFalse()
        assertThat(levels[1].perks).isEmpty()
    }

    @Test
    fun `anyone's levels are the active ones, cheapest first`() {
        answer("tiers_public.json")
        val levels = (runBlocking { repository.levelsOf("creator-a1") } as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/creators/creator-a1/tiers")
        assertThat(levels.map { it.name }).containsExactly("Supporter", "Superfan").inOrder()
    }

    @Test
    fun `eligibility names what is missing, in plain words, and fails closed`() {
        answer("eligibility_missing.json")
        val missing = (runBlocking { repository.eligibility("me") } as AppResult.Success).data
        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/creators/me/membership-eligibility")
        assertThat(missing.eligible).isFalse()
        assertThat(missing.missing).containsExactly(
            EligibilityItem.Age,
            EligibilityItem.Pan,
            EligibilityItem.Followers,
            EligibilityItem.Strikes,
        ).inOrder()
        assertThat(missing.missing.map { it.sentence }).doesNotContain("")

        answer("eligibility_ok.json")
        assertThat((runBlocking { repository.eligibility("me") } as AppResult.Success).data.eligible).isTrue()
        // "eligible" with something still missing is not eligible.
        server.enqueue(MockResponse.Builder().body("""{"data":{"eligible":true,"missing":["pan"]}}""").build())
        assertThat((runBlocking { repository.eligibility("me") } as AppResult.Success).data.eligible).isFalse()
    }

    @Test
    fun `a join's checkout carries the server's amount and session`() {
        answer("checkout_join.json", code = 201)
        val checkout = (runBlocking { repository.join("creator-a1", "tier-1", "key-1") } as AppResult.Success).data

        assertThat(checkout.paymentId).isEqualTo("9c4e0000-0000-4000-8000-0000000ab101")
        assertThat(checkout.subscriptionId).isEqualTo("5a3f0000-0000-4000-8000-0000000ab001")
        assertThat(checkout.kind).isEqualTo("join")
        assertThat(checkout.amountPaise).isEqualTo(9_900)
        assertThat(checkout.payable).isTrue()
        assertThat(checkout.clientSession).containsExactly(
            "provider",
            "razorpay",
            "order_id",
            "order_NmBr8xZ1",
            "key_id",
            "rzp_test_1DP5mmOlF5G5ag",
            "merchant_display_name",
            "Momentum",
        )
    }

    @Test
    fun `a replayed join that is already paid has no session and is not payable`() {
        answer("checkout_join_paid_replay.json", code = 201)
        val checkout = (runBlocking { repository.join("creator-a1", "tier-1", "key-1") } as AppResult.Success).data
        assertThat(checkout.status).isEqualTo("active")
        assertThat(checkout.clientSession).isEmpty()
        assertThat(checkout.payable).isFalse()
    }

    @Test
    fun `a level change is a payment, a scheduled downgrade or an upgrade at once`() {
        answer("upgrade_checkout.json", code = 201)
        val pay = (runBlocking { repository.changeLevel("sub-1", "tier-3") } as AppResult.Success).data
        assertThat(pay).isInstanceOf(LevelChange.Pay::class.java)
        assertThat((pay as LevelChange.Pay).checkout.amountPaise).isEqualTo(26_666)

        answer("upgrade_scheduled.json")
        val down = (runBlocking { repository.changeLevel("sub-1", "tier-1") } as AppResult.Success).data
        assertThat(down).isEqualTo(LevelChange.Scheduled("2026-11-23T09:30:00Z"))

        answer("upgrade_upgraded.json")
        assertThat((runBlocking { repository.changeLevel("sub-1", "tier-3") } as AppResult.Success).data)
            .isEqualTo(LevelChange.Upgraded)
    }

    @Test
    fun `the payment poll reads in the coordinator's vocabulary, a refund outranking everything`() {
        answer("payment_confirming.json")
        answer("payment_paid.json")
        answer("payment_late_refund.json")
        val confirming = (runBlocking { repository.paymentStatus("pay-1") } as AppResult.Success).data
        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/memberships/payments/pay-1")
        val paid = (runBlocking { repository.paymentStatus("pay-1") } as AppResult.Success).data
        val late = (runBlocking { repository.paymentStatus("pay-1") } as AppResult.Success).data

        assertThat(confirming.toReading()).isEqualTo(PaymentStatusReading.Confirming)
        assertThat(paid.toReading()).isEqualTo(PaymentStatusReading.Paid)
        assertThat(late.lateCapture).isTrue()
        assertThat(late.toReading()).isEqualTo(PaymentStatusReading.RefundPending)
        assertThat(paid.copy(refundStatus = "refunded").toReading()).isEqualTo(PaymentStatusReading.Refunded)
        assertThat(paid.copy(refundStatus = "failed").toReading()).isEqualTo(PaymentStatusReading.RefundPending)
        assertThat(paid.copy(status = "failed").toReading()).isEqualTo(PaymentStatusReading.Failed(retryable = true))
        assertThat(paid.copy(status = "settling_somehow").toReading()).isEqualTo(PaymentStatusReading.Confirming)
    }

    @Test
    fun `my memberships decode their dates, access and renewal`() {
        answer("my_memberships.json")
        val rows = (runBlocking { repository.myMemberships() } as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/memberships")
        val (active, ended) = rows
        assertThat(active.creatorId).isEqualTo("00000000-0000-4000-8000-0000000000b2")
        assertThat(active.levelName).isEqualTo("Supporter")
        assertThat(active.status).isEqualTo(MembershipStatus.Active)
        assertThat(active.hasAccess).isTrue()
        assertThat(active.canCancel).isTrue()
        assertThat(active.canChangeLevel).isTrue()
        assertThat(ended.status).isEqualTo(MembershipStatus.Expired)
        assertThat(ended.accessUntil).isNull()
        assertThat(ended.hasAccess).isFalse()
        assertThat(ended.renewAvailable).isTrue()
        assertThat(ended.canCancel).isFalse()
        // A status newer than this app is shown, never treated as access.
        assertThat(MembershipStatus.fromWire("frozen")).isEqualTo(MembershipStatus.Unknown)
        assertThat(MembershipStatus.Unknown.grantsAccess).isFalse()
    }

    @Test
    fun `the list names the creator when the server's lookup answered, and only the id when not`() {
        answer("my_memberships.json")
        val (named, unnamed) = (runBlocking { repository.myMemberships() } as AppResult.Success).data
        assertThat(named.creatorName).isEqualTo("Call UserB")
        assertThat(unnamed.creatorName).isEmpty()
        assertThat(unnamed.creatorId).isEqualTo("00000000-0000-4000-8000-0000000000c3")
    }

    /** `service.MembershipDetailView` (service/membership_reads.go:182): the row, the perks, the scheduled move down. */
    @Test
    fun `one membership carries its perks and any downgrade waiting for next month`() {
        answer("my_membership_detail.json")
        val sub = "5a3f0000-0000-4000-8000-0000000ab001"
        val detail = (runBlocking { repository.myMembership(sub) } as AppResult.Success).data

        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/memberships/$sub")
        assertThat(detail.creatorName).isEqualTo("Call UserB")
        assertThat(detail.levelName).isEqualTo("Superfan")
        assertThat(detail.perks).containsExactly("Everything in Supporter", "Monthly live Q&A").inOrder()
        assertThat(detail.pendingDowngrade?.levelName).isEqualTo("Supporter")
        assertThat(detail.pendingDowngrade?.pricePaise).isEqualTo(9_900)

        val notFound = """{"error":{"code":"NOT_FOUND","message":"Membership not found"}}"""
        server.enqueue(MockResponse.Builder().code(404).body(notFound).build())
        val missing = (runBlocking { repository.myMembership("someone-elses") } as AppResult.Failure).error
        assertThat(missing).isInstanceOf(AppError.NotFound::class.java)
    }

    // ── Refusals ─────────────────────────────────────────────────────────

    @Test
    fun `the first level refused for eligibility names what is missing`() {
        answer("error_not_eligible.json", code = 403)
        val error = (runBlocking { repository.createLevel("Supporter", 9_900, emptyList()) } as AppResult.Failure).error

        assertThat(error.membershipCode()).isEqualTo("MEMBERSHIPS_NOT_ELIGIBLE")
        assertThat(error.notEligibleItems()).containsExactly(EligibilityItem.Pan, EligibilityItem.Followers).inOrder()
        assertThat(membershipRefusal(error)).isEqualTo("You can't offer memberships yet.")
    }

    @Test
    fun `already a member is a 409 with its own code and words`() {
        answer("error_already_subscribed.json", code = 409)
        val error = (runBlocking { repository.join("c", "t", "k") } as AppResult.Failure).error
        assertThat(error.membershipCode()).isEqualTo("ALREADY_SUBSCRIBED")
        assertThat(membershipRefusal(error)).isEqualTo("You're already a member.")
        assertThat(membershipRefusal(AppError.NoNetwork())).contains("offline")
        assertThat(membershipRefusal(AppError.Unknown(code = "NEW_CODE", statusCode = 409))).contains("try again")
    }

    // ── The creator's members ─────────────────────────────────────────────

    @Test
    fun `the members page decodes each row, drops one with no member, and carries the cursor`() {
        answer("members_page.json")
        val page = (runBlocking { repository.members() } as AppResult.Success).data
        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/monetization/members?limit=30")
        assertThat(page.nextCursor).isEqualTo("MjAyNi0wOS0zMFQwODowMDowMFp8NWEzZg")
        assertThat(page.items.map { it.subscriptionId })
            .containsExactly("5a3f0000-0000-4000-8000-0000000ac001", "5a3f0000-0000-4000-8000-0000000ac002").inOrder()
        val first = page.items[0]
        assertThat(first.userId).isEqualTo("00000000-0000-4000-8000-0000000000a1")
        assertThat(first.nameForDisplay).isEqualTo("Call UserA")
        assertThat(first.username).isEqualTo("call_a")
        assertThat(first.avatarUrl).isEqualTo("https://cdn.example/a.jpg")
        assertThat(first.levelName).isEqualTo("Supporter")
        assertThat(first.pricePaise).isEqualTo(9900)
        assertThat(first.status).isEqualTo(MembershipStatus.Active)
        assertThat(first.since).isEqualTo("2026-10-20T09:30:00Z")
        assertThat(first.accessUntil).isEqualTo("2026-11-20T09:30:00Z")
        // No profile fields: a neutral word, never the raw id.
        val second = page.items[1]
        assertThat(second.nameForDisplay).isEqualTo("A member")
        assertThat(second.avatarUrl).isNull()
        assertThat(second.status).isEqualTo(MembershipStatus.CancelledAtPeriodEnd)
    }

    @Test
    fun `the next page asks with the cursor, and the last page has none`() {
        answer("members_last.json")
        val page = (runBlocking { repository.members(cursor = "abc", limit = 50) } as AppResult.Success).data
        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/members?cursor=abc&limit=50")
        assertThat(page.nextCursor).isNull()
        assertThat(page.hasMore).isFalse()
        assertThat(page.items.single().nameForDisplay).isEqualTo("lapsed")
        assertThat(page.items.single().status).isEqualTo(MembershipStatus.Expired)
        assertThat(page.items.single().accessUntil).isNull()

        // A blank cursor is the first page; Go's nil slice is an empty page, not a failure.
        answer("members_empty.json")
        val empty = (runBlocking { repository.members(cursor = " ") } as AppResult.Success).data
        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/members?limit=30")
        assertThat(empty.items).isEmpty()
    }

    @Test
    fun `a cursor the server did not mint is a 400 with its code`() {
        server.enqueue(
            MockResponse.Builder().code(400)
                .body("""{"error":{"code":"INVALID_CURSOR","message":"bad cursor"}}""").build(),
        )
        val error = (runBlocking { repository.members(cursor = "nope") } as AppResult.Failure).error
        assertThat(error.membershipCode()).isEqualTo("INVALID_CURSOR")
    }
}
