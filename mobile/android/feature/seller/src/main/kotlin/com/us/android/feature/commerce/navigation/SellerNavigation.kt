package com.us.android.feature.commerce.navigation

import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.compose.navigation
import androidx.navigation.toRoute
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
import com.us.android.feature.commerce.seller.StartSellingScreen
import com.us.android.feature.commerce.seller.StockScreen
import com.us.android.feature.commerce.seller.SubmitProductViewModel
import com.us.android.feature.commerce.seller.SubmitShopScreen
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
        composable<MSellerRoute> { entry ->
            // Scoped to this destination, so a submit in flight survives a
            // recomposition but does not outlive the screen that started it.
            val submitter: SubmitProductViewModel = hiltViewModel(entry)
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
                    submitProduct = { productId -> submitter.submit(productId) { } },
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
                onOpenPickupAddress = { navController.navigate(SellerPickupAddressRoute) },
                onOpenPayout = { navController.navigate(SellerPayoutRoute) },
                onOpenDocument = { navController.navigate(SellerDocumentRoute) },
                // Back to the hub, which now shows the shop as submitted. The
                // seller has nothing further to do until a reviewer answers.
                onSubmitted = navController::popBackStack,
            )
        }

        composable<StartSellingRoute> {
            StartSellingScreen(
                onBack = navController::popBackStack,
                // Back to the hub, which now has a shop to show. popBackStack
                // rather than navigate, so "open shop" does not stack a second
                // hub on top of the one the seller came from.
                onOpened = navController::popBackStack,
            )
        }

        composable<SellerEditPriceRoute> { entry ->
            val route = entry.toRoute<SellerEditPriceRoute>()
            EditPriceScreen(
                title = route.title,
                onBack = navController::popBackStack,
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
