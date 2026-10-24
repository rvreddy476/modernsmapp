package com.us.android.feature.commerce.navigation

import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.compose.navigation
import androidx.navigation.toRoute
import com.us.android.core.commerce.model.SellerAddressKind
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.feature.commerce.seller.DocumentScreen
import com.us.android.feature.commerce.seller.EditPriceScreen
import com.us.android.feature.commerce.seller.NewProductScreen
import com.us.android.feature.commerce.seller.PayoutScreen
import com.us.android.feature.commerce.seller.PickupAddressScreen
import com.us.android.feature.commerce.seller.ProductImagesScreen
import com.us.android.feature.commerce.seller.SellerEarningsScreen
import com.us.android.feature.commerce.seller.SellerHubActions
import com.us.android.feature.commerce.seller.SellerOrderDetailScreen
import com.us.android.feature.commerce.seller.SellerOrdersScreen
import com.us.android.feature.commerce.seller.SellerReturnsScreen
import com.us.android.feature.commerce.seller.SellerScreen
import com.us.android.feature.commerce.seller.SellScopeScreen
import com.us.android.feature.commerce.seller.StartSellingScreen
import com.us.android.feature.commerce.seller.StockScreen
import com.us.android.feature.commerce.seller.SubmitShopScreen
import com.us.android.feature.commerce.seller.SubmitShopActions
import kotlinx.serialization.Serializable

// ─── MSeller ─────────────────────────────────────────────────────────

/** The seller app, as a graph. */
@Serializable
data object MSellerGraph

/**
 * MSeller's hub.
 *
 * Nothing about it is reachable without a seller account, and the screen says
 * so plainly rather than erroring — most people using this app are buyers.
 */
@Serializable
data object MSellerRoute

/**
 * Stock for one variant.
 *
 * Carries the title so the screen has something to show in its bar before the
 * stock figures arrive.
 */
@Serializable
data class SellerStockRoute(val variantId: String, val title: String)

/** A listing's photos. */
@Serializable
data class SellerImagesRoute(val productId: String, val title: String)

/** The pickup point — the origin of every shipment this seller sends. */
@Serializable
data object SellerPickupAddressRoute

/** Opening a shop. */
@Serializable
data object StartSellingRoute

/** Where the seller is paid. */
@Serializable
data object SellerPayoutRoute

/** The seller's orders: what to pack and ship. */
@Serializable
data object SellerOrdersRoute

/** One order from the seller's side. The screen reads it fresh; nothing but the id travels. */
@Serializable
data class SellerOrderDetailRoute(val orderId: String)

/** The returns inbox. */
@Serializable
data object SellerReturnsRoute

/** What has been earned on delivered orders. */
@Serializable
data object SellerEarningsRoute

/** Sending the shop for review. */
@Serializable
data object SubmitShopRoute

/** Sending an identity document for review. */
@Serializable
data object SellerDocumentRoute

/** "What will you sell?" (migration 045): all categories with a GSTIN, or books only with a PAN. */
@Serializable
data object SellScopeRoute

/** A books-only seller's business address (migration 045). */
@Serializable
data object SellerBusinessAddressRoute

/** A books-only seller's proof of address: the document screen, offering the five kinds that prove one. */
@Serializable
data object SellerAddressProofRoute

/** Listing a product. */
@Serializable
data object NewProductRoute

/**
 * Changing a listing's price.
 *
 * Carries the title for the bar and NOT the price: the screen reads the
 * current price from the catalogue, because a figure carried through
 * navigation is a figure from whenever the previous screen loaded.
 */
@Serializable
data class SellerEditPriceRoute(val variantId: String, val title: String)


fun NavGraphBuilder.mSellerScreens(navController: NavController) {
    navigation<MSellerGraph>(startDestination = MSellerRoute) {
        composable<MSellerRoute> {
            // A listing's submit is the hub's own ViewModel's work
            // (2026-10-24): it used to be a second ViewModel called here with
            // an empty callback, whose progress and refusal nothing showed.
            SellerScreen(
                onBack = navController::popBackStack,
                actions = SellerHubActions(
                    openStock = { variantId, title ->
                        navController.navigate(SellerStockRoute(variantId, title))
                    },
                    openImages = { productId, title ->
                        navController.navigate(SellerImagesRoute(productId, title))
                    },
                    openPickupAddress = { navController.navigate(SellerPickupAddressRoute) },
                    listProduct = { navController.navigate(NewProductRoute) },
                    submitShop = { navController.navigate(SubmitShopRoute) },
                    openOrders = { navController.navigate(SellerOrdersRoute) },
                    openReturns = { navController.navigate(SellerReturnsRoute) },
                    openEarnings = { navController.navigate(SellerEarningsRoute) },
                ),
                onStartSelling = { navController.navigate(StartSellingRoute) },
            )
        }

        composable<SellerOrdersRoute> {
            SellerOrdersScreen(
                onBack = navController::popBackStack,
                onOpenOrder = { navController.navigate(SellerOrderDetailRoute(it)) },
            )
        }

        composable<SellerOrderDetailRoute> {
            SellerOrderDetailScreen(onBack = navController::popBackStack)
        }

        composable<SellerReturnsRoute> {
            SellerReturnsScreen(
                onBack = navController::popBackStack,
                // The seller's view of the order, not the buyer's: a return
                // is decided against what was packed and shipped.
                onOpenOrder = { navController.navigate(SellerOrderDetailRoute(it)) },
            )
        }

        composable<SellerEarningsRoute> {
            SellerEarningsScreen(
                onBack = navController::popBackStack,
                onOpenOrder = { navController.navigate(SellerOrderDetailRoute(it)) },
            )
        }

        composable<SellerStockRoute> { entry ->
            val route = entry.toRoute<SellerStockRoute>()
            StockScreen(
                title = route.title,
                onBack = navController::popBackStack,
                onEditPrice = {
                    navController.navigate(SellerEditPriceRoute(route.variantId, route.title))
                },
            )
        }

        composable<SellerImagesRoute> { entry ->
            val route = entry.toRoute<SellerImagesRoute>()
            ProductImagesScreen(
                productId = route.productId,
                title = route.title,
                onBack = navController::popBackStack,
                // Back to the hub, which re-reads the catalogue and now shows
                // the new cover on the row.
                onSaved = navController::popBackStack,
            )
        }

        composable<NewProductRoute> {
            NewProductScreen(
                onBack = navController::popBackStack,
                // Straight to the new listing's stock screen: the seller has
                // just stated an opening quantity and the next thing they
                // usually want is to check or correct it. popUpTo keeps the
                // create form off the back stack, so Back from there returns
                // to the hub rather than to a filled-in form that would list a
                // second product if resubmitted.
                //
                // The route takes the VARIANT id (2026-09-30), read from the
                // create response or `GET /products/:id/variants` — never the
                // product id, which the stock route cannot look up. When no
                // variant could be found the seller lands on the hub, whose
                // row resolves it again on tap.
                onCreated = { created ->
                    val variantId = created.variantId
                    if (variantId == null) {
                        navController.popBackStack()
                    } else {
                        navController.navigate(SellerStockRoute(variantId, created.title)) {
                            popUpTo(NewProductRoute) { inclusive = true }
                        }
                    }
                },
            )
        }

        composable<SellerPayoutRoute> {
            PayoutScreen(
                onBack = navController::popBackStack,
                onSaved = navController::popBackStack,
            )
        }

        composable<SellerDocumentRoute> {
            DocumentScreen(
                onBack = navController::popBackStack,
                // Back to the checklist, which re-reads readiness on resume
                // and will now show this requirement met.
                onAttached = navController::popBackStack,
            )
        }

        composable<SubmitShopRoute> {
            SubmitShopScreen(
                onBack = navController::popBackStack,
                actions = SubmitShopActions(
                    openSellScope = { navController.navigate(SellScopeRoute) },
                    openBusinessAddress = { navController.navigate(SellerBusinessAddressRoute) },
                    openAddressProof = { navController.navigate(SellerAddressProofRoute) },
                    openPickupAddress = { navController.navigate(SellerPickupAddressRoute) },
                    openPayout = { navController.navigate(SellerPayoutRoute) },
                    openDocument = { navController.navigate(SellerDocumentRoute) },
                ),
                // Back to the hub, which now shows the shop as submitted. The
                // seller has nothing further to do until a reviewer answers.
                onSubmitted = navController::popBackStack,
            )
        }

        composable<StartSellingRoute> {
            StartSellingScreen(
                onBack = navController::popBackStack,
                // Straight to "What will you sell?" (migration 045), the first
                // thing a new shop answers. The start form leaves the back
                // stack, so Back from the step returns to the hub, which now
                // has a shop to show, rather than to a form that opened one.
                onOpened = {
                    navController.navigate(SellScopeRoute) {
                        popUpTo(StartSellingRoute) { inclusive = true }
                    }
                },
            )
        }

        composable<SellerEditPriceRoute> { entry ->
            val route = entry.toRoute<SellerEditPriceRoute>()
            EditPriceScreen(
                title = route.title,
                onBack = navController::popBackStack,
            )
        }

        composable<SellScopeRoute> {
            SellScopeScreen(
                onBack = navController::popBackStack,
                // Back to whoever asked: the checklist re-reads readiness, the
                // hub re-reads the shop.
                onDone = navController::popBackStack,
            )
        }

        composable<SellerBusinessAddressRoute> {
            PickupAddressScreen(
                onBack = navController::popBackStack,
                onSaved = navController::popBackStack,
                kind = SellerAddressKind.BUSINESS,
            )
        }

        composable<SellerAddressProofRoute> {
            DocumentScreen(
                onBack = navController::popBackStack,
                onAttached = navController::popBackStack,
                addressProof = true,
            )
        }

        composable<SellerPickupAddressRoute> {
            PickupAddressScreen(
                onBack = navController::popBackStack,
                // Back to the hub, not forward: saving a pickup address is a
                // settings edit, and leaving the seller somewhere new
                // afterwards is a navigation surprise.
                onSaved = navController::popBackStack,
            )
        }
    }
}


fun NavController.navigateToMSeller() = navigate(MSellerGraph)
