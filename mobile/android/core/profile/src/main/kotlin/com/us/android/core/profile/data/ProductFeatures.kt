package com.us.android.core.profile.data

import com.us.android.core.common.di.ApplicationScope
import com.us.android.core.model.Product
import com.us.android.core.model.productOpen
import com.us.android.core.model.shouldReadFeatures
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import retrofit2.http.GET
import javax.inject.Inject
import javax.inject.Singleton

/**
 * `GET /v1/features` (api-gateway cmd/server/features.go, founder decision C,
 * 24 Oct 2026): which parked products this deployment serves. Public, no
 * authentication; the gateway caches it for 60 s.
 *
 *     {"data":{"products":{"broadcast_channels":{"enabled":false}, …}}}
 */
interface FeaturesApi {
    @GET("v1/features")
    suspend fun features(): JsonElement
}

/**
 * The open products in a features body, or null when the body is not the
 * features document at all (which the store treats like a failed read).
 *
 * Strict: a product is open only when its entry is an object whose `enabled`
 * is the JSON boolean `true`. Missing, `false`, `"true"`, `1`, `null` or a
 * non-object entry all read as closed. Keys this build does not know are
 * ignored.
 */
fun parseProductFeatures(body: JsonElement): Set<Product>? {
    val data = (body as? JsonObject)?.get("data") as? JsonObject ?: return null
    val products = data["products"] as? JsonObject ?: return null
    return Product.entries.filterTo(mutableSetOf()) { product ->
        val entry = products[product.wire] as? JsonObject
        val enabled = entry?.get("enabled") as? JsonPrimitive
        enabled != null && !enabled.isString && enabled.booleanOrNull == true
    }
}

/** What the store reads through; a seam so its tests answer without a server. */
fun interface ProductFeaturesSource {
    suspend fun read(): JsonElement
}

/** The clock the refresh rule reads; a seam for the tests. */
fun interface FeaturesClock {
    fun nowMs(): Long
}

/**
 * The app's one answer to `GET /v1/features`, shared by every surface.
 *
 * [open] is the last GOOD answer's open products, or null before there has
 * been one. A failed read, or a body that is not the features document,
 * keeps whatever was there: never reopens or closes anything by accident.
 * With no good answer at all the built-in default applies
 * ([productOpen]: broadcast channels and communities hidden, the rest open).
 *
 * Read once per app session ([refreshIfStale] on the process's first start)
 * and again on a resume that finds the last read ten minutes old
 * ([shouldReadFeatures]). One read at a time.
 */
@Singleton
class ProductFeaturesStore internal constructor(
    private val source: ProductFeaturesSource,
    private val scope: CoroutineScope,
    private val clock: FeaturesClock,
) {
    // The API is lazy so the OkHttp stack is built inside the read, off the
    // main thread, not when Application.onStart first asks for the store.
    @Inject
    constructor(api: dagger.Lazy<FeaturesApi>, @ApplicationScope scope: CoroutineScope) :
        this(ProductFeaturesSource { api.get().features() }, scope, FeaturesClock { System.currentTimeMillis() })

    private val _open = MutableStateFlow<Set<Product>?>(null)
    val open: StateFlow<Set<Product>?> = _open.asStateFlow()

    private val lock = Any()
    private var lastReadAtMs: Long? = null
    private var inFlight: CompletableDeferred<Unit>? = null

    /** Is [product] open right now? */
    fun isOpen(product: Product): Boolean = productOpen(product, _open.value)

    /** Starts a read when the session has none yet or the last one is ten minutes old. */
    fun refreshIfStale() {
        val started = synchronized(lock) {
            if (!shouldReadFeatures(lastReadAtMs, clock.nowMs(), inFlight != null)) return
            lastReadAtMs = clock.nowMs()
            CompletableDeferred<Unit>().also { inFlight = it }
        }
        scope.launch {
            try {
                val body = source.read()
                parseProductFeatures(body)?.let { _open.value = it }
            } catch (e: CancellationException) {
                throw e
            } catch (@Suppress("TooGenericExceptionCaught") e: Exception) {
                // Keep the last good answer (or the built-in default).
            } finally {
                synchronized(lock) { inFlight = null }
                started.complete(Unit)
            }
        }
    }

    /**
     * For a deep link at cold start: when the session's first read is still on
     * its way, wait up to [timeoutMs] for it, so a tap into a closed product is
     * not let through on the default. Returns at once otherwise.
     */
    suspend fun awaitFirstRead(timeoutMs: Long = FIRST_READ_WAIT_MS) {
        val pending = synchronized(lock) { inFlight.takeIf { _open.value == null } } ?: return
        withTimeoutOrNull(timeoutMs) { pending.await() }
    }

    private companion object {
        const val FIRST_READ_WAIT_MS = 3_000L
    }
}
