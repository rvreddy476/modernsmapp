package com.us.android.feature.commerce

import com.us.android.core.commerce.network.AddToCartRequest
import com.us.android.core.commerce.network.AddressDto
import com.us.android.core.commerce.network.AdjustStockRequest
import com.us.android.core.commerce.network.AttachOneProductMediaRequest
import com.us.android.core.commerce.network.AttachProductMediaRequest
import com.us.android.core.commerce.network.CancelOrderRequest
import com.us.android.core.commerce.network.CartDto
import com.us.android.core.commerce.network.CategoryDto
import com.us.android.core.commerce.network.CheckoutRequest
import com.us.android.core.commerce.network.CheckoutResultDto
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.network.CreateProductRequest
import com.us.android.core.commerce.network.HomeDto
import com.us.android.core.commerce.network.OrderDto
import com.us.android.core.commerce.network.OrderListDto
import com.us.android.core.commerce.network.PaymentHandleDto
import com.us.android.core.commerce.network.PaymentStatusDto
import com.us.android.core.commerce.network.PayoutRequest
import com.us.android.core.commerce.network.ProductDetailDto
import com.us.android.core.commerce.network.ProductListDto
import com.us.android.core.commerce.network.ProductMediaListDto
import com.us.android.core.commerce.network.QuoteDto
import com.us.android.core.commerce.network.QuoteRequest
import com.us.android.core.commerce.network.ReadinessDto
import com.us.android.core.commerce.network.SaveDocumentsRequest
import com.us.android.core.commerce.network.SellerAddressRequest
import com.us.android.core.commerce.network.SellerProductDto
import com.us.android.core.commerce.network.SellerProductsDto
import com.us.android.core.commerce.network.SellerProfileDto
import com.us.android.core.commerce.network.SellerVariantDto
import com.us.android.core.commerce.network.StartSellingRequest
import com.us.android.core.commerce.network.StockDto
import com.us.android.core.commerce.network.TaxClassListDto
import com.us.android.core.commerce.network.UpdateCartItemRequest
import com.us.android.core.commerce.network.UpdateVariantRequest
import com.us.android.core.commerce.network.VariantDto
import com.us.android.core.network.ApiEnvelope
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.Response

/**
 * Every commerce endpoint, answering "this case does not use me".
 *
 * The seam for a commerce test is [CommerceApi] — the Retrofit interface, the
 * last thing before the wire — so everything above it stays production code:
 * envelope unwrapping, DTO decoding, the domain mapping, the ViewModel. That
 * choice costs one thing: the interface is wide, and a test that cares about
 * two endpoints used to have to stub thirty.
 *
 * This base carries the thirty. A case overrides only what it is about, and
 * adding an endpoint to the API stops breaking every existing fake.
 */
open class FakeCommerceApi : CommerceApi {

    override suspend fun listProducts(
        query: String?,
        categoryId: String?,
        cursor: String?,
        limit: Int,
        sort: String?,
    ): Response<ApiEnvelope<ProductListDto>> = unused()

    override suspend fun getProduct(productId: String): Response<ApiEnvelope<ProductDetailDto>> = unused()

    override suspend fun categories(): Response<ApiEnvelope<List<CategoryDto>>> = unused()

    override suspend fun home(): Response<ApiEnvelope<HomeDto>> = unused()

    override suspend fun favourites(): Response<ApiEnvelope<ProductListDto>> = unused()

    override suspend fun addFavourite(productId: String): Response<ApiEnvelope<Unit>> = unused()

    override suspend fun removeFavourite(productId: String): Response<ApiEnvelope<Unit>> = unused()

    override suspend fun productMedia(productId: String): Response<ApiEnvelope<ProductMediaListDto>> = unused()

    override suspend fun attachProductMedia(
        productId: String,
        body: AttachProductMediaRequest,
    ): Response<ApiEnvelope<ProductMediaListDto>> = unused()

    override suspend fun attachOneProductMedia(
        productId: String,
        body: AttachOneProductMediaRequest,
    ): Response<ApiEnvelope<ProductMediaListDto>> = unused()

    override suspend fun getCart(): Response<ApiEnvelope<CartDto>> = unused()

    override suspend fun addToCart(body: AddToCartRequest): Response<ApiEnvelope<CartDto>> = unused()

    override suspend fun updateCartItem(
        variantId: String,
        body: UpdateCartItemRequest,
    ): Response<ApiEnvelope<CartDto>> = unused()

    override suspend fun removeCartItem(variantId: String): Response<ApiEnvelope<CartDto>> = unused()

    override suspend fun listAddresses(): Response<ApiEnvelope<List<AddressDto>>> = unused()

    override suspend fun addAddress(body: AddressDto): Response<ApiEnvelope<AddressDto>> = unused()

    override suspend fun quote(body: QuoteRequest): Response<ApiEnvelope<QuoteDto>> = unused()

    override suspend fun checkout(
        idempotencyKey: String,
        body: CheckoutRequest,
    ): Response<ApiEnvelope<CheckoutResultDto>> = unused()

    override suspend fun openPayment(orderId: String): Response<ApiEnvelope<PaymentHandleDto>> = unused()

    override suspend fun paymentStatus(orderId: String): Response<ApiEnvelope<PaymentStatusDto>> = unused()

    override suspend fun listOrders(cursor: String?, limit: Int): Response<ApiEnvelope<OrderListDto>> = unused()

    override suspend fun getOrder(orderId: String): Response<ApiEnvelope<OrderDto>> = unused()

    override suspend fun cancelOrder(
        orderId: String,
        body: CancelOrderRequest,
    ): Response<ApiEnvelope<Unit>> = unused()

    override suspend fun sellerProfile(): Response<ApiEnvelope<SellerProfileDto>> = unused()

    override suspend fun sellerProducts(
        status: String?,
        limit: Int,
        offset: Int,
    ): Response<ApiEnvelope<SellerProductsDto>> = unused()

    override suspend fun stock(variantId: String): Response<ApiEnvelope<StockDto>> = unused()

    override suspend fun adjustStock(
        variantId: String,
        body: AdjustStockRequest,
    ): Response<ApiEnvelope<StockDto>> = unused()

    override suspend fun startSelling(body: StartSellingRequest): Response<ApiEnvelope<SellerProfileDto>> = unused()

    override suspend fun taxClasses(): Response<ApiEnvelope<TaxClassListDto>> = unused()

    override suspend fun createProduct(body: CreateProductRequest): Response<ApiEnvelope<SellerProductDto>> = unused()

    override suspend fun sellerVariant(variantId: String): Response<ApiEnvelope<SellerVariantDto>> = unused()

    override suspend fun updateVariant(
        variantId: String,
        body: UpdateVariantRequest,
    ): Response<ApiEnvelope<VariantDto>> = unused()

    override suspend fun submitProduct(productId: String): Response<ApiEnvelope<Unit>> = unused()

    override suspend fun sellerReadiness(): Response<ApiEnvelope<ReadinessDto>> = unused()

    override suspend fun saveDocuments(body: SaveDocumentsRequest): Response<ApiEnvelope<Unit>> = unused()

    override suspend fun savePayout(body: PayoutRequest): Response<ApiEnvelope<Unit>> = unused()

    override suspend fun submitSellerApplication(): Response<ApiEnvelope<Unit>> = unused()

    override suspend fun saveSellerAddress(body: SellerAddressRequest): Response<ApiEnvelope<Unit>> = unused()
}

/**
 * A 500, not an exception.
 *
 * A case that reaches an endpoint it did not stub should fail on the
 * assertion it was actually making, with the whole state visible, rather than
 * on a thrown NotImplementedError from somewhere inside a coroutine.
 */
fun <T> unused(): Response<ApiEnvelope<T>> =
    Response.error(500, """{"error":{"code":"UNSTUBBED"}}""".toResponseBody("application/json".toMediaType()))

/** A 200 carrying [data] in the standard envelope. */
fun <T> envelope(data: T): Response<ApiEnvelope<T>> = Response.success(ApiEnvelope(data = data))

/** A 404 with no error code — the shape that means "this server has no such route". */
fun <T> notFound(): Response<ApiEnvelope<T>> =
    Response.error(404, """{"error":{}}""".toResponseBody("application/json".toMediaType()))
