package com.us.android.core.profile.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.Product
import org.junit.Test

/**
 * Founder decision C (24 Oct 2026): the module picker (first-login onboarding
 * and Settings) does not offer a product the gateway has closed.
 */
class OfferedModulesTest {

    @Test
    fun `every gated module names its product`() {
        assertThat(AppModule.entries.associateWith { it.product }.filterValues { it != null }).containsExactly(
            AppModule.DATING, Product.DATING,
            AppModule.FOOD, Product.FOOD,
            AppModule.QA, Product.QA,
            AppModule.MOBILITY, Product.RIDER,
            AppModule.HOME_SERVICES, Product.DOORSTEP,
        )
    }

    @Test
    fun `with every product closed only the launch modules are offered`() {
        assertThat(offeredModules(emptySet())).containsExactly(
            AppModule.REELS, AppModule.COMMERCE, AppModule.CHAT, AppModule.POSTTUBE,
        ).inOrder()
    }

    @Test
    fun `an open product is offered again, and no answer offers everything`() {
        assertThat(offeredModules(setOf(Product.DATING))).contains(AppModule.DATING)
        assertThat(offeredModules(setOf(Product.DATING))).doesNotContain(AppModule.FOOD)
        assertThat(offeredModules(null)).isEqualTo(AppModule.selectable)
    }
}
