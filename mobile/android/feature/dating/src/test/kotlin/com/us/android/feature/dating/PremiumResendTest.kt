package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.network.PremiumPurchaseResultDto
import com.us.android.feature.dating.premium.PremiumState
import com.us.android.feature.dating.premium.PremiumViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Rule
import org.junit.Test
import retrofit2.Response

/**
 * Dating audit L3: a purchase whose outcome is unknown (the network, a 5xx, a
 * gateway page, payments briefly down) may exist on the server. Its key is
 * kept, and the only thing the screen lets the person do is resend THAT pass
 * under THAT key. A definite refusal created nothing: the key goes, and any
 * pass may be bought under a new one.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PremiumResendTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val api = FakeDatingApi()

    private fun TestScope.ready(): PremiumViewModel {
        val model = PremiumViewModel(
            api.repository(),
            PaymentHandoff(),
            confirmingOnlyCoordinator(),
            SavedStateHandle(),
        )
        settle()
        assertThat(model.state.value).isInstanceOf(PremiumState.Ready::class.java)
        return model
    }

    private fun TestScope.settle() {
        advanceUntilIdle()
        runCurrent()
    }

    private fun TestScope.buy(model: PremiumViewModel, product: String) {
        model.buy(product)
        settle()
    }

    private fun repeat(): Response<ApiEnvelope<PremiumPurchaseResultDto>> =
        ok(fixture("premium_purchase_post_200_repeat.json", PremiumPurchaseResultDto.serializer()))

    private fun gatewayPage(status: Int): Response<ApiEnvelope<PremiumPurchaseResultDto>> =
        Response.error(status, "<html>Bad gateway</html>".toResponseBody("text/html".toMediaType()))

    private fun keys() = api.purchaseRequests.map { it.idempotencyKey }

    /** An unknown outcome: the resend is offered for this pass only, and the next tap reuses the key. */
    private fun TestScope.assertKeptFor(model: PremiumViewModel, product: String) {
        val state = model.state.value as PremiumState.Ready
        assertThat(state.resend).isEqualTo(product)
        assertThat(state.buying).isNull()
        assertThat(state.notice).isEqualTo(DatingCopy.PREMIUM_RESEND)

        // Another pass cannot be bought while that purchase may exist.
        val other = state.products.first { it.id != product }.id
        buy(model, other)
        assertThat(api.purchaseRequests).hasSize(1)

        api.purchaseResponse = { repeat() }
        buy(model, product)
        assertThat(api.purchaseRequests).hasSize(2)
        assertThat(keys().distinct()).hasSize(1)
        assertThat(api.purchaseRequests.map { it.product }.distinct()).containsExactly(product)
        assertThat(model.state.value).isInstanceOf(PremiumState.OpeningPayment::class.java)
    }

    @Test
    fun `503 PREMIUM_PAYMENTS_UNAVAILABLE keeps the key, as the server asks`() = runTest(dispatcher) {
        api.purchaseResponse = { refused(503, "PREMIUM_PAYMENTS_UNAVAILABLE") }
        val model = ready()

        buy(model, "pass_90d")

        assertKeptFor(model, "pass_90d")
    }

    @Test
    fun `a gateway 502 or 504 page after Buy keeps the key`() = runTest(dispatcher) {
        api.purchaseResponse = { gatewayPage(502) }
        val model = ready()
        buy(model, "pass_30d")
        assertKeptFor(model, "pass_30d")

        val again = ready()
        api.purchaseRequests.clear()
        api.purchaseResponse = { gatewayPage(504) }
        buy(again, "pass_90d")
        assertKeptFor(again, "pass_90d")
    }

    @Test
    fun `a 500 with a code keeps the key`() = runTest(dispatcher) {
        api.purchaseResponse = { refused(500, "QUERY_FAILED") }
        val model = ready()

        buy(model, "pass_30d")

        assertKeptFor(model, "pass_30d")
    }

    @Test
    fun `the network failing keeps the key`() = runTest(dispatcher) {
        api.purchaseResponse = { offline() }
        val model = ready()

        buy(model, "pass_30d")

        assertKeptFor(model, "pass_30d")
    }

    /** A definite refusal: nothing kept, every pass open again, the next purchase under a new key. */
    private fun TestScope.assertCleared(model: PremiumViewModel) {
        val state = model.state.value as PremiumState.Ready
        assertThat(state.resend).isNull()
        assertThat(state.buying).isNull()

        api.purchaseResponse = { repeat() }
        val other = state.products.first { it.id != api.purchaseRequests.single().product }.id
        buy(model, other)
        assertThat(api.purchaseRequests).hasSize(2)
        assertThat(keys().distinct()).hasSize(2)
    }

    @Test
    fun `502 PREMIUM_PAYMENTS_REFUSED created nothing`() = runTest(dispatcher) {
        api.purchaseResponse = { refused(502, "PREMIUM_PAYMENTS_REFUSED") }
        val model = ready()

        buy(model, "pass_30d")

        assertCleared(model)
    }

    @Test
    fun `a 4xx refusal created nothing`() = runTest(dispatcher) {
        api.purchaseResponse = { refusedWithFixture(400, "premium_purchase_post_400_client_price.json") }
        val model = ready()

        buy(model, "pass_30d")

        assertCleared(model)
    }

    @Test
    fun `409 IDEMPOTENCY_KEY_REUSED drops the key`() = runTest(dispatcher) {
        api.purchaseResponse = { refusedWithFixture(409, "premium_purchase_post_409_key_reused.json") }
        val model = ready()

        buy(model, "pass_30d")

        assertCleared(model)
    }

    @Test
    fun `503 PREMIUM_UNAVAILABLE is premium switched off, not a purchase in doubt`() = runTest(dispatcher) {
        api.purchaseResponse = { refusedWithFixture(503, "premium_purchase_post_503_unavailable.json") }
        val model = ready()

        buy(model, "pass_30d")

        assertThat(model.state.value).isEqualTo(PremiumState.Unavailable)
    }
}
